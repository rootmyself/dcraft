package secretref

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
)

// awsRunner answers the exact CLI shape dcraft uses. The returned stdout is
// the JSON string the real CLI prints for --query SecretString --output json.
func awsRunner(stdout string) *execxtest.Runner {
	return &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stdout: []byte(stdout + "\n")}, nil
	}}
}

func awsQuote(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func awsTestRuntime(t *testing.T, project, state string, runner execx.Runner) *Runtime {
	t.Helper()
	return NewRuntimeWithRunner(fakeDirs{root: state}, func(string) string { return "" }, runner)
}

func requireCategory(t *testing.T, err error, category core.Category) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error category %q, got nil", category)
	}
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v, want core.Error", err)
	}
	for _, diag := range ce.AllDiagnostics() {
		if diag.Category == category {
			return
		}
	}
	t.Fatalf("error %v has no category %q (%+v)", err, category, ce.AllDiagnostics())
}

func TestPrepareAWSFetchesOnlyNeededSecretsOnce(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	const secret = `{"database":{"username":"app-user-sentinel","password":"app-pass-sentinel"},"token":"token-sentinel"}`
	runner := awsRunner(awsQuote(t, secret))
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/db
    field: database.password
    region: ap-northeast-2
    profile: ops
    target: DB_PASSWORD
  - service: api
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/db
    field: database.username
    region: ap-northeast-2
    profile: ops
    target: DB_USER
  - service: worker
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/cache
    target: CACHE_URL
`)
	rt := awsTestRuntime(t, project, state, runner)
	session, err := rt.Prepare(context.Background(), targetFor(project), []string{"web", "api"}, ModeCreate)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	env := strings.Join(session.Env, "\n")
	for _, want := range []string{"DCRAFT_SECRET_1=app-pass-sentinel", "DCRAFT_SECRET_2=app-user-sentinel"} {
		if !strings.Contains(env, want) {
			t.Fatalf("env = %v, missing %q", session.Env, want)
		}
	}
	calls := runner.CallLines()
	if len(calls) != 1 {
		t.Fatalf("aws calls = %v, want exactly one for the shared secret", calls)
	}
	for _, want := range []string{"secretsmanager get-secret-value", "--secret-id prod/db", "--query SecretString", "--output json", "--region ap-northeast-2", "--profile ops"} {
		if !strings.Contains(calls[0], want) {
			t.Fatalf("aws call %q missing %q", calls[0], want)
		}
	}
	if strings.Contains(calls[0], "prod/cache") {
		t.Fatalf("dcraft fetched a declared but unneeded secret: %s", calls[0])
	}
	override := string(mustRead(t, session.Files[0]))
	for _, sentinel := range []string{"app-pass-sentinel", "app-user-sentinel"} {
		if strings.Contains(override, sentinel) {
			t.Fatalf("override leaked %q:\n%s", sentinel, override)
		}
	}
	redacted := session.Redact("out app-pass-sentinel and app-user-sentinel end")
	if strings.Contains(redacted, "app-pass-sentinel") || strings.Contains(redacted, "app-user-sentinel") {
		t.Fatalf("session redaction left a value: %q", redacted)
	}

	// The next operation is a new fetch scope: dcraft stores no value and
	// cannot compare or reuse an old one.
	if _, err := rt.Prepare(context.Background(), targetFor(project), []string{"web", "api"}, ModeCreate); err != nil {
		t.Fatalf("second Prepare: %v", err)
	}
	if got := len(runner.CallLines()); got != 2 {
		t.Fatalf("aws calls after a second operation = %d, want 2 (no cross-operation cache)", got)
	}
}

func TestPrepareAWSFileDeliveryPreservesExactBytesAndRedactsNewlines(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	value := "line-one-sentinel\nline-two-sentinel\n"
	runner := awsRunner(awsQuote(t, value))
	writeConfig(t, project, `secrets:
  - service: worker
    phase: runtime
    delivery: file
    source: aws
    ref: prod/token
    target: worker_token
`)
	rt := awsTestRuntime(t, project, state, runner)
	session, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	path, err := rt.MaterializedPath(targetFor(project), Reference{
		Service: "worker", Phase: PhaseRuntime, Delivery: DeliveryFile, Source: SourceAWS, Ref: "prod/token", Target: "worker_token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if data := string(mustRead(t, path)); data != value {
		t.Fatalf("materialized bytes = %q, want exact SecretString", data)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("materialized mode = %v, %v", info, err)
	}
	for _, text := range []string{value, strings.TrimRight(value, "\n"), "x\n" + value + "\n"} {
		if redacted := session.Redact(text); strings.Contains(redacted, "line-one-sentinel") || strings.Contains(redacted, "line-two-sentinel") {
			t.Fatalf("redaction left a trailing-newline variant: %q -> %q", text, redacted)
		}
	}
	override := string(mustRead(t, session.Files[0]))
	if strings.Contains(override, "line-one-sentinel") || strings.Contains(override, "line-two-sentinel") {
		t.Fatalf("override leaked the value:\n%s", override)
	}
}

func TestAWSFieldSelectionThroughDelivery(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		field    string
		want     string
		category core.Category
	}{
		{name: "nested string", payload: `{"a":{"b":"chosen-value"}}`, field: "a.b", want: "chosen-value"},
		{name: "array index", payload: `{"a":[{"b":"first"},{"b":"second"}]}`, field: "a.1.b", want: "second"},
		{name: "number scalar", payload: `{"port":5432}`, field: "port", want: "5432"},
		{name: "bool scalar", payload: `{"enabled":true}`, field: "enabled", want: "true"},
		{name: "missing key", payload: `{"a":{"b":"x"}}`, field: "a.c", category: core.CategorySecretAWSFieldInvalid},
		{name: "object value", payload: `{"a":{"b":{"nested":1}}}`, field: "a.b", category: core.CategorySecretAWSFieldInvalid},
		{name: "not JSON", payload: `plain-secret-text`, field: "a.b", category: core.CategorySecretAWSFieldInvalid},
		{name: "null field", payload: `{"a":null}`, field: "a", category: core.CategorySecretAWSValueInvalid},
		{name: "empty string", payload: ``, field: "", category: core.CategorySecretAWSValueInvalid},
		{name: "whitespace only", payload: `   `, field: "", category: core.CategorySecretAWSValueInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			state := t.TempDir()
			runner := awsRunner(awsQuote(t, tc.payload))
			cfg := "secrets:\n  - service: web\n    phase: runtime\n    delivery: environment\n    source: aws\n    ref: prod/db\n    target: DB_VALUE\n"
			if tc.field != "" {
				cfg += "    field: " + tc.field + "\n"
			}
			writeConfig(t, project, cfg)
			rt := awsTestRuntime(t, project, state, runner)
			session, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
			if tc.category != "" {
				requireCategory(t, err, tc.category)
				return
			}
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if !strings.Contains(strings.Join(session.Env, "\n"), "DCRAFT_SECRET_1="+tc.want) {
				t.Fatalf("env = %v, want value %q", session.Env, tc.want)
			}
		})
	}
}

func TestAWSFailureCategoriesNeverEchoValues(t *testing.T) {
	const stderrSentinel = "SEKRET-IN-STDERR-SENTINEL"
	cases := []struct {
		name     string
		lookErr  error
		runErr   error
		stdout   string
		stderr   string
		exit     int
		category core.Category
	}{
		{name: "cli missing", lookErr: errors.New("executable file not found"), category: core.CategorySecretAWSCLIMissing},
		{name: "credentials missing", stderr: "Unable to locate credentials. You can configure credentials by running \"aws configure\".", exit: 253, category: core.CategorySecretAWSCredentialsMissing},
		{name: "region missing", stderr: "You must specify a region. You can also configure your region by running \"aws configure\".", exit: 253, category: core.CategorySecretAWSRegionMissing},
		{name: "profile missing", stderr: "The config profile (missing) could not be found", exit: 253, category: core.CategorySecretAWSCredentialsMissing},
		{name: "unknown secret", stderr: "\nAn error occurred (ResourceNotFoundException) when calling the GetSecretValue operation: Secrets Manager can't find the specified secret.", exit: 254, category: core.CategorySecretAWSSecretNotFound},
		{name: "access denied", stderr: "\nAn error occurred (AccessDeniedException) when calling the GetSecretValue operation: " + stderrSentinel, exit: 254, category: core.CategorySecretAWSAccessDenied},
		{name: "kms denied", stderr: "An error occurred (KMSAccessDeniedException) when calling the GetSecretValue operation", exit: 254, category: core.CategorySecretAWSAccessDenied},
		{name: "network", stderr: "Could not connect to the endpoint URL: \"http://127.0.0.1:1/\" " + stderrSentinel, exit: 255, category: core.CategorySecretAWSNetwork},
		{name: "unexpected service error", stderr: "An error occurred (ThrottlingException) when calling the GetSecretValue operation", exit: 254, category: core.CategorySecretAWSFailed},
		{name: "absent SecretString", stdout: "null", exit: 0, category: core.CategorySecretAWSValueInvalid},
		{name: "empty SecretString", stdout: `""`, exit: 0, category: core.CategorySecretAWSValueInvalid},
		{name: "non-JSON field", stdout: `"plain-text"`, exit: 0, category: core.CategorySecretAWSFieldInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			state := t.TempDir()
			runner := &execxtest.Runner{
				LookPathErr: tc.lookErr,
				Handler: func(execx.Command) (execx.Result, error) {
					if tc.runErr != nil {
						return execx.Result{}, tc.runErr
					}
					return execx.Result{Stdout: []byte(tc.stdout + "\n"), Stderr: []byte(tc.stderr), ExitCode: tc.exit}, nil
				},
			}
			cfg := "secrets:\n  - service: web\n    phase: runtime\n    delivery: environment\n    source: aws\n    ref: prod/db\n    target: DB_VALUE\n"
			if tc.name == "missing key" {
				cfg += "    field: a.b\n"
			}
			if tc.category == core.CategorySecretAWSFieldInvalid {
				cfg += "    field: a.b\n"
			}
			writeConfig(t, project, cfg)
			rt := awsTestRuntime(t, project, state, runner)
			_, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
			requireCategory(t, err, tc.category)
			var ce *core.Error
			if !errors.As(err, &ce) {
				t.Fatalf("error = %v, want core.Error", err)
			}
			diag := ce.Diagnostics[0]
			if strings.TrimSpace(diag.Message) == "" || len(diag.NextActions) == 0 {
				t.Fatalf("diagnostic has no Korean guidance: %+v", diag)
			}
			if strings.Contains(err.Error(), stderrSentinel) || strings.Contains(diag.Detail, stderrSentinel) {
				t.Fatalf("diagnostic echoed raw tool output: %+v", diag)
			}
			overridePath := filepath.Join(state, "stacks", "demo", "compose.secrets.yaml")
			if _, statErr := os.Stat(overridePath); statErr == nil {
				t.Fatalf("a failed AWS fetch wrote the override")
			}
			entries, _ := os.ReadDir(filepath.Join(state, "stacks", "demo", "secrets"))
			if len(entries) > 0 {
				t.Fatalf("a failed AWS fetch left materialized files: %v", entries)
			}
		})
	}
}

func TestAWSCommandRejectsUnsafeArguments(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/db
    target: DB_VALUE
`)
	runner := awsRunner(awsQuote(t, "resolved-argument-sentinel"))
	rt := awsTestRuntime(t, project, state, runner)
	if _, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	call := runner.CallLines()[0]
	if strings.Contains(call, "--region") || strings.Contains(call, "--profile") {
		t.Fatalf("optional selectors were sent when not configured: %s", call)
	}
	// The command names exactly the executable and the flag pairs. No shell
	// interpolation, value argument or credential flag is involved.
	if !strings.HasPrefix(call, "aws secretsmanager get-secret-value --secret-id prod/db ") {
		t.Fatalf("unexpected command shape: %s", call)
	}
	if strings.Contains(call, "resolved-argument-sentinel") {
		t.Fatalf("command contains a resolved value: %s", call)
	}
}

func TestRedactTextDoesNotFetchAWSValues(t *testing.T) {
	project := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/db
    target: DB_VALUE
`)
	runner := awsRunner(awsQuote(t, "fetched-value"))
	rt := awsTestRuntime(t, project, t.TempDir(), runner)
	text := "diagnostic without a fetched value"
	if got := rt.RedactText(targetFor(project), text); got != text {
		t.Fatalf("RedactText changed text: %q", got)
	}
	if calls := runner.CallLines(); len(calls) != 0 {
		t.Fatalf("RedactText fetched AWS without a delivery need: %v", calls)
	}
}
