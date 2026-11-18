package compose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/state"
)

func secretProject(t *testing.T, config string) (string, core.Target) {
	t.Helper()
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services:\n  web:\n    image: busybox\n  worker:\n    image: busybox\n  other:\n    image: busybox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".dcraft.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, core.Target{ProjectDir: dir, Files: []string{composePath}, ProjectName: "dcraft-test-secrets"}
}

func secretRunner(t *testing.T, upOutput string) *execxtest.Runner {
	t.Helper()
	return &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		line := strings.Join(cmd.Args, " ")
		switch {
		case strings.Contains(line, " config --format json"):
			return execx.Result{Stdout: []byte(`{"name":"dcraft-test-secrets","services":{"web":{"image":"busybox"},"worker":{"image":"busybox","depends_on":{"web":{"condition":"service_started"}}},"other":{"image":"busybox"}}}`)}, nil
		case strings.Contains(line, " up --detach"):
			return execx.Result{Stdout: []byte(upOutput)}, nil
		case strings.Contains(line, " restart"):
			return execx.Result{Stdout: []byte("restarted")}, nil
		case strings.Contains(line, " down"):
			return execx.Result{Stdout: []byte("removed")}, nil
		default:
			return execx.Result{Stderr: []byte("unexpected " + line), ExitCode: 9}, nil
		}
	}}
}

func secretCompose(t *testing.T, project string, runner *execxtest.Runner, env map[string]string) (*Compose, *secretref.Runtime) {
	t.Helper()
	_ = project
	store := state.New(t.TempDir())
	rt := secretref.NewRuntime(store, func(name string) string { return env[name] })
	c := New(runner, nil)
	c.SetSecretRuntime(rt)
	return c, rt
}

func TestUpDeliversAWSReferencesThroughTheSameSecretSeam(t *testing.T) {
	_, target := secretProject(t, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/db
    field: password
    region: ap-northeast-2
    target: DB_PASSWORD
  - service: worker
    phase: runtime
    delivery: file
    source: aws
    ref: prod/token
    target: worker_token
`)
	const envSentinel = "compose-aws-env-sentinel-31b"
	const fileSentinel = "compose-aws-file-sentinel-92d"
	runner := secretRunner(t, "Container started")
	base := runner.Handler
	var awsCalls []string
	runner.Handler = func(cmd execx.Command) (execx.Result, error) {
		if cmd.Name == "aws" {
			line := strings.Join(cmd.Args, " ")
			awsCalls = append(awsCalls, line)
			switch {
			case strings.Contains(line, "--secret-id prod/db"):
				return execx.Result{Stdout: []byte(`"{\"password\":\"` + envSentinel + `\"}"` + "\n")}, nil
			case strings.Contains(line, "--secret-id prod/token"):
				return execx.Result{Stdout: []byte(`"` + fileSentinel + `"` + "\n")}, nil
			}
		}
		return base(cmd)
	}
	store := state.New(t.TempDir())
	rt := secretref.NewRuntimeWithRunner(store, func(string) string { return "" }, runner)
	c := New(runner, nil)
	c.SetSecretRuntime(rt)

	res, err := c.Up(context.Background(), target, nil)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(awsCalls) != 2 {
		t.Fatalf("aws calls = %v, want one per distinct runtime secret", awsCalls)
	}
	notices := strings.Join(res.Notices, "\n")
	if !strings.Contains(notices, "AWS") || !strings.Contains(notices, "자격 증명") {
		t.Fatalf("notices = %v, want AWS guidance", res.Notices)
	}
	if strings.Contains(res.Output, envSentinel) || strings.Contains(res.Output, fileSentinel) {
		t.Fatalf("Compose output leaked a value: %q", res.Output)
	}
	override, err := rt.OverridePath(target)
	if err != nil {
		t.Fatal(err)
	}
	overrideData, err := os.ReadFile(override)
	if err != nil {
		t.Fatal(err)
	}
	text := string(overrideData)
	for _, want := range []string{"DB_PASSWORD", "worker_token"} {
		if !strings.Contains(text, want) {
			t.Fatalf("override missing %q:\n%s", want, text)
		}
	}
	for _, sentinel := range []string{envSentinel, fileSentinel} {
		if strings.Contains(text, sentinel) {
			t.Fatalf("override leaked %q:\n%s", sentinel, text)
		}
	}
}

func TestUpAppliesOnlySelectedServiceClosureAndPassesEnv(t *testing.T) {
	_, target := secretProject(t, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: WEB_SECRET
  - service: other
    phase: runtime
    delivery: environment
    source: env
    ref: OTHER_SECRET
`)
	runner := secretRunner(t, "Container started")
	var upOverride string
	base := runner.Handler
	runner.Handler = func(cmd execx.Command) (execx.Result, error) {
		line := strings.Join(cmd.Args, " ")
		if strings.Contains(line, " up --detach") {
			for i := 0; i+1 < len(cmd.Args); i++ {
				if cmd.Args[i] == "-f" && strings.HasSuffix(cmd.Args[i+1], "compose.secrets.yaml") {
					if data, readErr := os.ReadFile(cmd.Args[i+1]); readErr == nil {
						upOverride = string(data)
					}
				}
			}
		}
		return base(cmd)
	}
	accessed := map[string]int{}
	store := state.New(t.TempDir())
	rt := secretref.NewRuntime(store, func(name string) string {
		accessed[name]++
		return "value-of-" + name
	})
	c := New(runner, nil)
	c.SetSecretRuntime(rt)

	res, err := c.Up(context.Background(), target, []string{"worker"})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if accessed["WEB_SECRET"] != 1 || accessed["OTHER_SECRET"] != 0 {
		t.Fatalf("env access = %v, want only the dependency closure", accessed)
	}
	if strings.Contains(res.Output, "value-of-WEB_SECRET") {
		t.Fatal("output leaked the secret value")
	}
	if len(res.Notices) == 0 {
		t.Fatal("up did not report applied secret notices")
	}
	overrideNotice := false
	for _, notice := range res.Notices {
		if strings.Contains(notice, "compose.secrets.yaml") {
			overrideNotice = true
		}
	}
	if !overrideNotice {
		t.Fatalf("up did not make the managed override visible: %v", res.Notices)
	}
	for _, notice := range res.Notices {
		if strings.Contains(notice, "value-of-WEB_SECRET") {
			t.Fatalf("notice leaked the value: %q", notice)
		}
	}
	var up execx.Command
	for _, call := range runner.Calls() {
		if strings.Contains(strings.Join(call.Args, " "), " up --detach") {
			up = call
		}
	}
	if up.Name != "docker" {
		t.Fatal("up was not invoked")
	}
	if !containsArgs(up.Args, "-f", filepath.Join(store.Root(), "stacks")) {
		t.Fatalf("up args do not include the managed override: %v", up.Args)
	}
	if !envHas(up.Env, "DCRAFT_SECRET_1=value-of-WEB_SECRET") {
		t.Fatalf("up env missing the web value: %v", up.Env)
	}
	if envHas(up.Env, "DCRAFT_SECRET_2=value-of-OTHER_SECRET") {
		t.Fatal("up env contains an unneeded service value")
	}
	if !strings.Contains(res.Output, "Container started") {
		t.Fatalf("output = %q", res.Output)
	}
	override := ""
	for _, arg := range up.Args {
		if strings.HasSuffix(arg, "compose.secrets.yaml") {
			override = arg
		}
	}
	if override == "" {
		t.Fatal("managed override missing from effective inputs")
	}
	data, err := os.ReadFile(override)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "web") || !strings.Contains(string(data), "DCRAFT_SECRET_1") {
		t.Fatalf("override = %s", data)
	}
	// The Compose call itself used only the selected closure.
	if strings.Contains(upOverride, "other") || !strings.Contains(upOverride, "web") {
		t.Fatalf("up used the wrong override:\n%s", upOverride)
	}
	// After a successful up, the on-disk override reflects the full catalog
	// (references only) so the effective inputs stay reproducible.
	if !strings.Contains(string(data), "other") {
		t.Fatalf("full override was not regenerated after up:\n%s", data)
	}
	if strings.Contains(string(data), "value-of-OTHER_SECRET") {
		t.Fatalf("regenerated override leaked a value:\n%s", data)
	}
}

func TestUpFailsBeforeComposeWhenSourceMissing(t *testing.T) {
	project, target := secretProject(t, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: MISSING_SECRET
`)
	runner := secretRunner(t, "Container started")
	c, rt := secretCompose(t, project, runner, nil)
	_, err := c.Up(context.Background(), target, nil)
	if err == nil {
		t.Fatal("Up succeeded with a missing source")
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategorySecretSource {
		t.Fatalf("error = %v", err)
	}
	for _, call := range runner.Calls() {
		if strings.Contains(strings.Join(call.Args, " "), " up --detach") {
			t.Fatalf("up ran despite the missing source: %v", call.Args)
		}
	}
	override, _ := rt.OverridePath(target)
	if _, statErr := os.Stat(override); statErr == nil {
		t.Fatalf("override exists after failure: %s", override)
	}
}

func TestUpRedactsKnownValueFromComposeOutput(t *testing.T) {
	_, target := secretProject(t, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: WEB_SECRET
`)
	const sentinel = "sentinel-up-output-9911"
	runner := secretRunner(t, "container printed "+sentinel)
	c, _ := secretCompose(t, target.ProjectDir, runner, map[string]string{"WEB_SECRET": sentinel})
	res, err := c.Up(context.Background(), target, nil)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if strings.Contains(res.Output, sentinel) {
		t.Fatalf("output leaked the value: %q", res.Output)
	}
	if !strings.Contains(res.Output, "[redacted]") {
		t.Fatalf("output was not redacted: %q", res.Output)
	}
}

func TestRestartRecreatesMissingFileSecretWithoutEnvValues(t *testing.T) {
	project, target := secretProject(t, `secrets:
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: token.txt
`)
	if err := os.WriteFile(filepath.Join(project, "token.txt"), []byte("file-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := secretRunner(t, "")
	c, rt := secretCompose(t, project, runner, nil)
	path, _ := rt.MaterializedPath(target, secretref.Reference{
		Service: "web", Delivery: secretref.DeliveryFile, Target: "token",
	})
	_, err := c.Restart(context.Background(), target, []string{"web"})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("materialized file missing after restart: %v", err)
	}
	if string(data) != "file-value" {
		t.Fatalf("materialized content = %q", data)
	}
	var restart execx.Command
	for _, call := range runner.Calls() {
		if strings.Contains(strings.Join(call.Args, " "), " restart ") {
			restart = call
		}
	}
	if restart.Name != "docker" {
		t.Fatal("restart was not invoked")
	}
	if len(restart.Env) > 0 {
		t.Fatalf("restart passed secret environment values: %v", restart.Env)
	}
	if strings.Contains(strings.Join(restart.Args, " "), "compose.secrets.yaml") {
		t.Fatal("restart passed the secrets override")
	}
}

func TestDownCleansUpOnlyAfterSuccess(t *testing.T) {
	project, target := secretProject(t, `secrets:
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: token.txt
`)
	if err := os.WriteFile(filepath.Join(project, "token.txt"), []byte("file-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := secretRunner(t, "")
	c, rt := secretCompose(t, project, runner, nil)
	if _, err := c.Up(context.Background(), target, nil); err != nil {
		t.Fatalf("Up: %v", err)
	}
	path, _ := rt.MaterializedPath(target, secretref.Reference{Service: "web", Delivery: secretref.DeliveryFile, Target: "token"})
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("materialized file missing: %v", err)
	}
	if _, err := c.Down(context.Background(), target); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("materialized file remains after down: %v", err)
	}
	// A failing down must keep the files a restart may need.
	if _, err := c.Up(context.Background(), target, nil); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	failing := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		line := strings.Join(cmd.Args, " ")
		if strings.Contains(line, " config --format json") {
			return execx.Result{Stdout: []byte(`{"name":"p","services":{"web":{}}}`)}, nil
		}
		if strings.Contains(line, " down") {
			return execx.Result{Stderr: []byte("down failed"), ExitCode: 1}, nil
		}
		return execx.Result{}, nil
	}}
	c2 := New(failing, nil)
	c2.SetSecretRuntime(rt)
	if _, err := c2.Down(context.Background(), target); err == nil {
		t.Fatal("Down succeeded, want failure")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("failed down removed the materialized file: %v", err)
	}
}

func TestConfigInfoReportsDependencies(t *testing.T) {
	project, target := secretProject(t, "")
	runner := secretRunner(t, "")
	c := New(runner, nil)
	cfg, err := c.ConfigInfo(context.Background(), target)
	if err != nil {
		t.Fatalf("ConfigInfo: %v", err)
	}
	if len(cfg.Services) != 3 {
		t.Fatalf("services = %v", cfg.Services)
	}
	if deps := cfg.Dependencies["worker"]; len(deps) != 1 || deps[0] != "web" {
		t.Fatalf("worker deps = %v", deps)
	}
	_ = project
}

func containsArgs(args []string, flag, prefix string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && strings.HasPrefix(args[i+1], prefix) {
			return true
		}
	}
	return false
}

func envHas(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}
