package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// writeAWSShim installs a controlled `aws` executable first on PATH. It logs
// only the argument vector (never a value), mimics the verified CLI exit
// conventions and prints an already JSON-encoded SecretString.
func writeAWSShim(t *testing.T) (dir, logPath string) {
	t.Helper()
	dir = t.TempDir()
	logPath = filepath.Join(dir, "aws-calls.log")
	script := `#!/bin/sh
log="${AWS_SHIM_LOG:?}"
printf '%s\n' "$*" >> "$log"
if [ -n "${AWS_SHIM_EXIT:-}" ]; then
  printf '\n%s\n' "${AWS_SHIM_STDERR:-shim failure}" >&2
  exit "$AWS_SHIM_EXIT"
fi
if [ -n "${AWS_SHIM_STDOUT:-}" ]; then
  printf '%s\n' "$AWS_SHIM_STDOUT"
  exit 0
fi
id=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--secret-id" ]; then id="$a"; fi
  prev="$a"
done
case "$id" in
  prod/db) printf '%s\n' "${AWS_SHIM_PROD_DB:?}" ;;
  prod/token) printf '%s\n' "${AWS_SHIM_PROD_TOKEN:?}" ;;
  build/token) printf '%s\n' "${AWS_SHIM_BUILD_TOKEN:?}" ;;
  *)
    printf '\nAn error occurred (ResourceNotFoundException) when calling the GetSecretValue operation: missing\n' >&2
    exit 254 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, logPath
}

// awsEnv builds the dcraft process environment with the shim first on PATH and
// the requested shim variables. The docker fake stays available.
func awsEnv(t *testing.T, fake *fakeDocker, stateDir, shimDir, logPath string, extra map[string]string) []string {
	t.Helper()
	values := map[string]string{
		"PATH":         shimDir + ":" + fake.dir + ":" + os.Getenv("PATH"),
		"AWS_SHIM_LOG": logPath,
	}
	for k, v := range extra {
		values[k] = v
	}
	return fake.envExtra(t, stateDir, values)
}

func awsCalls(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var calls []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			calls = append(calls, line)
		}
	}
	return calls
}

func jsonQuote(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeAWSProject(t *testing.T, project, config string) string {
	t.Helper()
	composePath := writeProject(t, project)
	if err := os.WriteFile(filepath.Join(project, ".dcraft.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return composePath
}

func TestSecretsCLIAWSRoundTripIsValueFree(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	res := runProcess(t, project, env, strings.NewReader(""), "secrets", "--add",
		"-s", "web", "--delivery", "environment", "--source", "aws", "--ref", "prod/db",
		"--field", "database.password", "--region", "ap-northeast-2", "--aws-profile", "ops-admin",
		"--target", "DB_PASSWORD", "--json")
	if res.code != 0 {
		t.Fatalf("add exit = %d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	if res.stderr != "" {
		t.Fatalf("JSON mode wrote stderr: %q", res.stderr)
	}
	parsed := parseSecrets(t, res.stdout)
	if parsed.Data.Secrets.Action != "add" || len(parsed.Data.Secrets.References) != 1 {
		t.Fatalf("payload = %+v", parsed.Data.Secrets)
	}
	ref := parsed.Data.Secrets.References[0]
	if ref.Source != "aws" || ref.Ref != "prod/db" || ref.Field != "database.password" ||
		ref.Region != "ap-northeast-2" || ref.Profile != "ops-admin" || ref.Target != "DB_PASSWORD" {
		t.Fatalf("reference = %+v", ref)
	}
	for _, want := range []string{"AWS Secrets Manager", "자격 증명", "한 번만"} {
		if !strings.Contains(strings.Join(parsed.Data.Secrets.Notices, "\n"), want) {
			t.Fatalf("notices missing %q: %v", want, parsed.Data.Secrets.Notices)
		}
	}
	config := readConfig(t, project)
	for _, want := range []string{"source: aws", "ref: prod/db", "field: database.password", "region: ap-northeast-2", "profile: ops-admin"} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
	for _, forbidden := range []string{"value:", "aws_access_key", "aws_secret_access_key", "session_token"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("config contains %q:\n%s", forbidden, config)
		}
	}
	if calls := awsCalls(t, filepath.Join(fake.dir, "unused.log")); len(calls) != 0 {
		t.Fatalf("adding a reference fetched AWS: %v", calls)
	}

	missingTarget := runProcess(t, project, env, strings.NewReader(""), "secrets", "--add",
		"-s", "web", "--delivery", "environment", "--source", "aws", "--ref", "prod/db", "--json")
	if missingTarget.code != 2 {
		t.Fatalf("missing target exit = %d, want 2\n%s", missingTarget.code, missingTarget.stdout)
	}
	if !hasCategory(secretsStatus(parseSecrets(t, missingTarget.stdout)), core.CategorySecretInputInvalid) {
		t.Fatalf("missing target diagnostics = %+v", parseSecrets(t, missingTarget.stdout).Diagnostics)
	}
}

func TestUpAWSFetchesOnlySelectedSecretsOnceAndRestartDoesNotRefetchFiles(t *testing.T) {
	fake := newFakeDocker(t)
	fake.writeConfig(t, `{"name":"fake-project","services":{"web":{"image":"nginx:alpine"},"api":{"image":"api:local"},"worker":{"image":"busybox"}}}`)
	shimDir, logPath := writeAWSShim(t)
	project := t.TempDir()
	composePath := writeAWSProject(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/db
    field: password
    region: ap-northeast-2
    target: DB_PASSWORD
  - service: api
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/db
    field: username
    region: ap-northeast-2
    target: DB_USER
  - service: api
    phase: runtime
    delivery: file
    source: aws
    ref: prod/token
    target: api_token
  - service: worker
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/cache
    target: CACHE_URL
`)
	composeBefore := sha256File(t, composePath)
	state := t.TempDir()
	envSentinel := "aws-env-sentinel-" + randomHex(t, 6)
	userSentinel := "aws-user-sentinel-" + randomHex(t, 6)
	fileSentinel := "aws-file-sentinel-" + randomHex(t, 6)
	dbPayload := `{"username":"` + userSentinel + `","password":"` + envSentinel + `"}`
	env := awsEnv(t, fake, state, shimDir, logPath, map[string]string{
		"AWS_SHIM_PROD_DB":    jsonQuote(t, dbPayload),
		"AWS_SHIM_PROD_TOKEN": jsonQuote(t, fileSentinel),
	})

	res := runProcess(t, project, env, strings.NewReader(""), "up", "-s", "web", "-s", "api", "--json")
	if res.code != 0 {
		t.Fatalf("up exit = %d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	assertNoSentinelIn(t, "up stdout", res.stdout, envSentinel, userSentinel, fileSentinel)
	assertNoSentinelIn(t, "up stderr", res.stderr, envSentinel, userSentinel, fileSentinel)
	calls := awsCalls(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("aws calls = %v, want one for prod/db (shared) and one for prod/token", calls)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "--secret-id prod/db") {
		t.Fatalf("calls = %v, want prod/db", calls)
	}
	if strings.Contains(strings.Join(calls, "\n"), "prod/cache") {
		t.Fatalf("up fetched an unselected service's reference: %v", calls)
	}
	for _, call := range calls {
		if strings.Contains(call, envSentinel) || strings.Contains(call, userSentinel) || strings.Contains(call, fileSentinel) {
			t.Fatalf("aws argument vector contains a value: %s", call)
		}
	}
	if sha256File(t, composePath) != composeBefore {
		t.Fatal("up modified the user Compose file")
	}
	var upCall string
	for _, line := range fake.calls(t) {
		if strings.Contains(line, " up --detach") {
			upCall = line
		}
	}
	if upCall == "" || !strings.Contains(upCall, "compose.secrets.yaml") {
		t.Fatalf("up call = %q, want the managed override", upCall)
	}
	scanSecretMetadata(t, state, project, envSentinel, userSentinel, fileSentinel)

	// Restart is not a configuration update: it reuses the already
	// materialized files and never re-fetches a source, so a rotated AWS value
	// cannot reach a restarting container.
	restart := runProcess(t, project, env, strings.NewReader(""), "restart", "-s", "api", "--json")
	if restart.code != 0 {
		t.Fatalf("restart exit = %d\n%s\n%s", restart.code, restart.stdout, restart.stderr)
	}
	assertNoSentinelIn(t, "restart stdout", restart.stdout, envSentinel, userSentinel, fileSentinel)
	after := awsCalls(t, logPath)
	if len(after) != 2 {
		t.Fatalf("aws calls after restart = %v, want no additional fetch", after)
	}
	scanSecretMetadata(t, state, project, envSentinel, userSentinel, fileSentinel)
}

func TestUpAWSErrorCategoriesAndNoComposeChange(t *testing.T) {
	cases := []struct {
		name     string
		extra    map[string]string
		category core.Category
	}{
		{
			name:     "access denied",
			extra:    map[string]string{"AWS_SHIM_EXIT": "254", "AWS_SHIM_STDERR": "An error occurred (AccessDeniedException) when calling the GetSecretValue operation: denied"},
			category: core.CategorySecretAWSAccessDenied,
		},
		{
			name:     "unknown secret",
			extra:    map[string]string{"AWS_SHIM_EXIT": "254", "AWS_SHIM_STDERR": "An error occurred (ResourceNotFoundException) when calling the GetSecretValue operation: missing"},
			category: core.CategorySecretAWSSecretNotFound,
		},
		{
			name:     "region missing",
			extra:    map[string]string{"AWS_SHIM_EXIT": "253", "AWS_SHIM_STDERR": "You must specify a region. You can also configure your region by running \"aws configure\"."},
			category: core.CategorySecretAWSRegionMissing,
		},
		{
			name:     "credentials missing",
			extra:    map[string]string{"AWS_SHIM_EXIT": "253", "AWS_SHIM_STDERR": "Unable to locate credentials. You can configure credentials by running \"aws configure\"."},
			category: core.CategorySecretAWSCredentialsMissing,
		},
		{
			name:     "network",
			extra:    map[string]string{"AWS_SHIM_EXIT": "255", "AWS_SHIM_STDERR": "Could not connect to the endpoint URL: \"http://127.0.0.1:1/\""},
			category: core.CategorySecretAWSNetwork,
		},
		{
			name:     "absent value",
			extra:    map[string]string{"AWS_SHIM_STDOUT": "null"},
			category: core.CategorySecretAWSValueInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeDocker(t)
			shimDir, logPath := writeAWSShim(t)
			project := t.TempDir()
			composePath := writeAWSProject(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/db
    target: DB_PASSWORD
`)
			before := sha256File(t, composePath)
			state := t.TempDir()
			env := awsEnv(t, fake, state, shimDir, logPath, tc.extra)
			res := runProcess(t, project, env, strings.NewReader(""), "up", "--json")
			if res.code != 1 {
				t.Fatalf("up exit = %d, want 1\n%s\n%s", res.code, res.stdout, res.stderr)
			}
			parsed := parseStatus(t, res.stdout)
			if !hasCategory(parsed, tc.category) {
				t.Fatalf("diagnostics = %+v, want %q", parsed.Diagnostics, tc.category)
			}
			for _, line := range fake.calls(t) {
				if strings.Contains(line, " up --detach") {
					t.Fatalf("Compose up ran despite the AWS failure: %s", line)
				}
			}
			if sha256File(t, composePath) != before {
				t.Fatal("a failed AWS fetch changed the Compose file")
			}
			scanSecretMetadata(t, state, project)
		})
	}
}

func TestUpAWSMissingCLIIsDistinct(t *testing.T) {
	// Build a PATH with the docker fake and the base system directories but
	// without the directory that holds the real aws CLI.
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	shimDir, _ := writeAWSShim(t)
	state := t.TempDir()
	env := fake.envExtra(t, state, map[string]string{
		"PATH": fake.dir + ":/bin:/usr/bin",
	})
	check := exec.Command("sh", "-c", "command -v aws")
	check.Env = env
	if out, err := check.CombinedOutput(); err == nil {
		t.Skipf("aws is still resolvable on the reduced PATH (%s); cannot simulate a missing CLI", strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(shimDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".dcraft.yaml"), []byte(`secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/db
    target: DB_PASSWORD
`), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runProcess(t, project, env, strings.NewReader(""), "up", "--json")
	if res.code != 1 {
		t.Fatalf("up exit = %d, want 1\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	if !hasCategory(parseStatus(t, res.stdout), core.CategorySecretAWSCLIMissing) {
		t.Fatalf("diagnostics = %+v", parseStatus(t, res.stdout).Diagnostics)
	}
}

func TestUpAWSValueIsRedactedFromCapturedOutput(t *testing.T) {
	fake := newFakeDocker(t)
	shimDir, logPath := writeAWSShim(t)
	project := t.TempDir()
	writeAWSProject(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/db
    target: DB_PASSWORD
`)
	sentinel := "aws-redact-sentinel-" + randomHex(t, 6)
	env := awsEnv(t, fake, t.TempDir(), shimDir, logPath, map[string]string{
		"AWS_SHIM_PROD_DB":     jsonQuote(t, sentinel),
		"DOCKER_FAKE_ECHO_ENV": "DCRAFT_SECRET_1",
	})
	jsonRes := runProcess(t, project, env, strings.NewReader(""), "up", "--json")
	if jsonRes.code != 0 {
		t.Fatalf("up exit = %d\n%s\n%s", jsonRes.code, jsonRes.stdout, jsonRes.stderr)
	}
	assertNoSentinelIn(t, "up JSON", jsonRes.stdout+jsonRes.stderr, sentinel)

	// A failing Compose command surfaces its captured output in the failure
	// diagnostic. The known fetched value must be marked there.
	failingEnv := awsEnv(t, fake, t.TempDir(), shimDir, logPath, map[string]string{
		"AWS_SHIM_PROD_DB":     jsonQuote(t, sentinel),
		"DOCKER_FAKE_ECHO_ENV": "DCRAFT_SECRET_1",
		"DOCKER_FAKE_FAIL_UP":  "1",
	})
	failed := runProcess(t, project, failingEnv, strings.NewReader(""), "up", "--json")
	if failed.code != 1 {
		t.Fatalf("failing up exit = %d, want 1\n%s\n%s", failed.code, failed.stdout, failed.stderr)
	}
	assertNoSentinelIn(t, "failing up JSON", failed.stdout+failed.stderr, sentinel)
	if !strings.Contains(failed.stdout, "[redacted]") {
		t.Fatalf("captured tool output was not redacted:\n%s", failed.stdout)
	}

	human := runProcess(t, project, failingEnv, strings.NewReader(""), "up")
	if human.code != 1 {
		t.Fatalf("human up exit = %d, want 1\n%s\n%s", human.code, human.stdout, human.stderr)
	}
	assertNoSentinelIn(t, "human up", human.stdout+human.stderr, sentinel)
	if !strings.Contains(human.stdout+human.stderr, "[redacted]") {
		t.Fatalf("human output did not mark the redaction:\n%s\n%s", human.stdout, human.stderr)
	}
}

func TestSecretsTUIWizardOffersAWSAndSavesTheShape(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "[↑↓] 이동", send: menuDown(t, "secrets")},
		{wait: "▸ 비밀값 연결", send: "\r"},
		{wait: "서비스 선택", send: "\r"}, // api is the first sorted service
		{wait: "전달 시점 선택", send: "\r"},
		{wait: "전달 방식 선택", send: "\r"},
		{wait: "원본 종류 선택", send: "j"},
		{wait: "원본 종류 선택", send: "j"},
		{wait: "원본 종류 선택", send: "\r"},
		{wait: "원본 참조 입력", send: "prod/db\r"},
		{wait: "전달 대상 이름 입력", send: "DB_PASSWORD\r"},
		{wait: "AWS field 선택", send: "database.password\r"},
		{wait: "AWS 리전 선택", send: "ap-northeast-2\r"},
		{wait: "AWS profile 선택", send: "\r"}, // skip
		{wait: "저장할 비밀값 연결", send: ""},
		{wait: "AWS Secrets Manager", send: "\r"},
		{wait: "비밀값 참조 저장", send: ""},
	})
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{
		"AWS Secrets Manager", "database.password", "ap-northeast-2", "자격 증명", "비밀값 참조 저장",
	} {
		if !screenContains(output, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, output)
		}
	}
	config := readConfig(t, project)
	for _, want := range []string{"source: aws", "ref: prod/db", "field: database.password", "region: ap-northeast-2", "target: DB_PASSWORD"} {
		if !strings.Contains(config, want) {
			t.Fatalf("saved config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "value:") || strings.Contains(config, "profile:") {
		t.Fatalf("saved config contains a value or an empty profile:\n%s", config)
	}
}
