package secretref

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type fakeDirs struct{ root string }

func (f fakeDirs) StackDir(core.Target) (string, error) {
	return filepath.Join(f.root, "stacks", "demo"), nil
}

func newTestRuntime(t *testing.T, project, state string, env map[string]string) *Runtime {
	t.Helper()
	return NewRuntime(fakeDirs{root: state}, func(name string) string { return env[name] })
}

func targetFor(project string) core.Target {
	return core.Target{ProjectDir: project, Files: []string{filepath.Join(project, "compose.yaml")}}
}

func TestPrepareIgnoresBuildPhaseReferences(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	sentinel := "sentinel-build-only-91ab"
	if err := os.MkdirAll(filepath.Join(project, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "secrets", "build.txt"), []byte(sentinel+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: file
    ref: secrets/build.txt
    target: build_token
`)
	rt := newTestRuntime(t, project, state, nil)
	session, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(session.Files) != 0 || len(session.Env) != 0 || session.HasValues() {
		t.Fatalf("runtime prepared build references: files=%v env=%v values=%v", session.Files, session.Env, session.HasValues())
	}
	if _, err := os.Stat(filepath.Join(state, "stacks", "demo", "compose.secrets.yaml")); !os.IsNotExist(err) {
		t.Fatalf("runtime override was created for a build-only catalog: %v", err)
	}
	session.Commit()
}

func TestRegenerateOverrideKeepsRuntimeAndBuildSeparate(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	sentinel := "sentinel-split-3f2c"
	if err := os.MkdirAll(filepath.Join(project, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "secrets", "runtime.txt"), []byte(sentinel+"-runtime\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "secrets", "build.txt"), []byte(sentinel+"-build\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: secrets/runtime.txt
    target: app_token
  - service: web
    phase: build
    delivery: file
    source: file
    ref: secrets/build.txt
    target: build_token
`)
	rt := newTestRuntime(t, project, state, nil)
	if err := rt.RegenerateOverride(targetFor(project)); err != nil {
		t.Fatalf("RegenerateOverride: %v", err)
	}
	stackDir := filepath.Join(state, "stacks", "demo")
	runtimeOverride, err := os.ReadFile(filepath.Join(stackDir, "compose.secrets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(runtimeOverride)
	if !strings.Contains(text, "app_token") || strings.Contains(text, "build_token") || strings.Contains(text, "build:") {
		t.Fatalf("runtime override contains build delivery:\n%s", text)
	}
	if strings.Contains(text, sentinel) {
		t.Fatalf("runtime override leaked a value:\n%s", text)
	}
	buildOverride, err := os.ReadFile(filepath.Join(stackDir, "compose.build-secrets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	buildText := string(buildOverride)
	if !strings.Contains(buildText, "build:") || !strings.Contains(buildText, "build_token") {
		t.Fatalf("build override missing the BuildKit mount:\n%s", buildText)
	}
	if strings.Contains(buildText, "app_token") || strings.Contains(buildText, sentinel) {
		t.Fatalf("build override leaked runtime delivery or a value:\n%s", buildText)
	}
	for _, path := range []string{"compose.secrets.yaml", "compose.build-secrets.yaml"} {
		info, err := os.Stat(filepath.Join(stackDir, path))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
		}
	}
}

func TestPrepareEnvironmentDeliveryBuildsOverrideWithoutValues(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: TEST_WEB_PASSWORD
`)
	sentinel := "sentinel-env-018f3e2a"
	rt := newTestRuntime(t, project, state, map[string]string{"TEST_WEB_PASSWORD": sentinel})

	session, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(session.Files) != 1 || !strings.HasSuffix(session.Files[0], "compose.secrets.yaml") {
		t.Fatalf("files = %v", session.Files)
	}
	if len(session.Env) != 1 || !strings.HasPrefix(session.Env[0], "DCRAFT_SECRET_1=sentinel-") {
		t.Fatalf("env = %v", session.Env)
	}
	override, err := os.ReadFile(session.Files[0])
	if err != nil {
		t.Fatalf("read override: %v", err)
	}
	text := string(override)
	if strings.Contains(text, sentinel) {
		t.Fatalf("override leaked the value:\n%s", text)
	}
	for _, want := range []string{"services:", "web:", "DB", "TEST"} {
		_ = want
	}
	if !strings.Contains(text, "DCRAFT_SECRET_1") || !strings.Contains(text, "TEST_WEB_PASSWORD") {
		t.Fatalf("override missing environment reference:\n%s", text)
	}
	if redacted := session.Redact("error: " + sentinel + " failed"); strings.Contains(redacted, sentinel) {
		t.Fatalf("redaction left the value: %q", redacted)
	}
	if !strings.Contains(session.Redact("error: "+sentinel), msgRedacted) {
		t.Fatal("redaction marker missing")
	}
	session.Commit()
}

func TestPrepareFileDeliveryMaterializesPrivateFile(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	sentinel := "sentinel-file-77c1"
	if err := os.MkdirAll(filepath.Join(project, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(project, "secrets", "token.txt")
	if err := os.WriteFile(source, []byte(sentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, project, `secrets:
  - service: worker
    phase: runtime
    delivery: file
    source: file
    ref: secrets/token.txt
    target: worker_token
`)
	rt := newTestRuntime(t, project, state, nil)
	session, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	path, err := rt.MaterializedPath(targetFor(project), Reference{
		Service: "worker", Phase: PhaseRuntime, Delivery: DeliveryFile,
		Source: SourceFile, Ref: "secrets/token.txt", Target: "worker_token",
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat materialized: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if string(data) != sentinel+"\n" {
		t.Fatalf("content = %q", data)
	}
	if strings.Contains(string(mustRead(t, session.Files[0])), sentinel) {
		t.Fatal("override leaked the file content")
	}
	override := string(mustRead(t, session.Files[0]))
	if !strings.Contains(override, path) || !strings.Contains(override, "worker_token") {
		t.Fatalf("override missing materialized path/target:\n%s", override)
	}
	session.Commit()
}

func TestPrepareOnlyNeededServices(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: WEB_SECRET
  - service: worker
    phase: runtime
    delivery: environment
    source: env
    ref: WORKER_SECRET
`)
	accessed := map[string]int{}
	rt := NewRuntime(fakeDirs{root: state}, func(name string) string {
		accessed[name]++
		return "value-" + name
	})
	session, err := rt.Prepare(context.Background(), targetFor(project), []string{"web"}, ModeCreate)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if accessed["WEB_SECRET"] != 1 || accessed["WORKER_SECRET"] != 0 {
		t.Fatalf("env access = %v, want only WEB_SECRET", accessed)
	}
	override := string(mustRead(t, session.Files[0]))
	if strings.Contains(override, "worker") || strings.Contains(override, "WORKER") {
		t.Fatalf("override contains an unneeded service:\n%s", override)
	}
	session.Commit()
}

func TestPrepareFailsBeforeWritingOnBadSources(t *testing.T) {
	cases := []struct {
		name string
		text string
		env  map[string]string
		msg  string
	}{
		{
			name: "missing environment",
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: environment\n    source: env\n    ref: MISSING_SECRET\n",
			msg:  "MISSING_SECRET",
		},
		{
			name: "empty environment",
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: environment\n    source: env\n    ref: EMPTY_SECRET\n",
			env:  map[string]string{"EMPTY_SECRET": "   "},
			msg:  "EMPTY_SECRET",
		},
		{
			name: "missing file",
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: file\n    source: file\n    ref: missing.txt\n",
			msg:  "missing.txt",
		},
		{
			name: "empty file",
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: file\n    source: file\n    ref: empty.txt\n",
			msg:  "empty.txt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			state := t.TempDir()
			if tc.name == "empty file" {
				if err := os.WriteFile(filepath.Join(project, "empty.txt"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			writeConfig(t, project, tc.text)
			rt := newTestRuntime(t, project, state, tc.env)
			_, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
			if err == nil {
				t.Fatal("Prepare succeeded, want error")
			}
			var ce *core.Error
			if !asCoreError(err, &ce) {
				t.Fatalf("error = %v", err)
			}
			if ce.Diagnostics[0].Category != core.CategorySecretSource {
				t.Fatalf("category = %q (%+v)", ce.Diagnostics[0].Category, ce.Diagnostics[0])
			}
			if !strings.Contains(ce.Diagnostics[0].Message, tc.msg) {
				t.Fatalf("message %q does not identify %q", ce.Diagnostics[0].Message, tc.msg)
			}
			override, _ := rt.OverridePath(targetFor(project))
			if _, statErr := os.Stat(override); statErr == nil {
				t.Fatalf("override was written after a source failure: %s", override)
			}
			secretsDir := filepath.Join(state, "stacks", "demo", "secrets")
			if entries, readErr := os.ReadDir(secretsDir); readErr == nil && len(entries) > 0 {
				t.Fatalf("partial materialization left behind: %v", entries)
			}
		})
	}
}

func TestPrepareSkipsEnvValuesInRestartMode(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "token.txt"), []byte("file-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: UNUSED_ENV
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: token.txt
`)
	accessed := 0
	rt := NewRuntime(fakeDirs{root: state}, func(name string) string {
		accessed++
		return ""
	})
	session, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeFiles)
	if err != nil {
		t.Fatalf("Prepare files: %v", err)
	}
	if accessed != 0 {
		t.Fatalf("restart read environment values %d times", accessed)
	}
	if len(session.Files) != 0 || len(session.Env) != 0 {
		t.Fatalf("restart session = %+v", session)
	}
	path, _ := rt.MaterializedPath(targetFor(project), Reference{
		Service: "web", Delivery: DeliveryFile, Target: "token",
	})
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("restart did not ensure the file: %v", err)
	}
}

func TestCommitRemovesStaleMaterializedFiles(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte("value-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: a.txt
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: b.txt
`)
	rt := newTestRuntime(t, project, state, nil)
	session, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	session.Commit()
	aPath, _ := rt.MaterializedPath(targetFor(project), Reference{Service: "web", Delivery: DeliveryFile, Target: "a"})
	bPath, _ := rt.MaterializedPath(targetFor(project), Reference{Service: "web", Delivery: DeliveryFile, Target: "b"})
	for _, path := range []string{aPath, bPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("materialized %s missing: %v", path, err)
		}
	}

	// Removing the "a" reference and running a successful up again must clean
	// the now-unreferenced file but keep the live one.
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: b.txt
`)
	session, err = rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err != nil {
		t.Fatalf("Prepare second: %v", err)
	}
	// The file must survive until the Compose call succeeds.
	if _, err := os.Stat(aPath); err != nil {
		t.Fatalf("stale file removed before commit: %v", err)
	}
	session.Commit()
	if _, err := os.Stat(aPath); !os.IsNotExist(err) {
		t.Fatalf("stale file not removed: %v", err)
	}
	if _, err := os.Stat(bPath); err != nil {
		t.Fatalf("live file removed: %v", err)
	}
}

func TestCleanupStackRemovesEverything(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "token.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: token.txt
`)
	rt := newTestRuntime(t, project, state, nil)
	session, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	session.Commit()
	if err := rt.CleanupStack(targetFor(project)); err != nil {
		t.Fatalf("CleanupStack: %v", err)
	}
	override, _ := rt.OverridePath(targetFor(project))
	if _, err := os.Stat(override); !os.IsNotExist(err) {
		t.Fatalf("override remains after cleanup: %v", err)
	}
	secretsDir := filepath.Join(state, "stacks", "demo", "secrets")
	if _, err := os.Stat(secretsDir); !os.IsNotExist(err) {
		t.Fatalf("secrets dir remains after cleanup: %v", err)
	}
}

func TestPrepareRejectsStateInsideProject(t *testing.T) {
	project := t.TempDir()
	state := filepath.Join(project, ".state")
	if err := os.WriteFile(filepath.Join(project, "token.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: token.txt
`)
	rt := newTestRuntime(t, project, state, nil)
	_, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err == nil {
		t.Fatal("Prepare accepted a state dir inside the project")
	}
	var ce *core.Error
	if !asCoreError(err, &ce) || !strings.Contains(ce.Diagnostics[0].Message, "상태 폴더") {
		t.Fatalf("error = %v", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRestartModeRewritesCurrentSourceAndNeverCaches(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	source := filepath.Join(project, "token.txt")
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: token.txt
`)
	if err := os.WriteFile(source, []byte("value-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, project, state, nil)
	path, _ := rt.MaterializedPath(targetFor(project), Reference{Service: "web", Delivery: DeliveryFile, Target: "token"})
	if _, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeFiles); err != nil {
		t.Fatalf("first restart prepare: %v", err)
	}
	if data := string(mustRead(t, path)); data != "value-v1" {
		t.Fatalf("first materialized = %q", data)
	}
	// Simulate the source changing while no dcraft process is caching the
	// previous value. The next operation must rewrite, not reuse the file.
	if err := os.WriteFile(source, []byte("value-v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeFiles); err != nil {
		t.Fatalf("second restart prepare: %v", err)
	}
	if data := string(mustRead(t, path)); data != "value-v2" {
		t.Fatalf("restart reused a stale value: %q", data)
	}
}

func TestPrepareAfterCrashRewritesFromCurrentSource(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	source := filepath.Join(project, "token.txt")
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: token.txt
`)
	if err := os.WriteFile(source, []byte("crash-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, project, state, nil)
	path, _ := rt.MaterializedPath(targetFor(project), Reference{Service: "web", Delivery: DeliveryFile, Target: "token"})
	// Prepare writes the file, then the process dies before Compose starts and
	// before Commit. Nothing stores the fetched value.
	if _, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate); err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if err := os.WriteFile(source, []byte("crash-v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	session.Commit()
	if data := string(mustRead(t, path)); data != "crash-v2" {
		t.Fatalf("next operation reused an unverifiable stale value: %q", data)
	}
}

func TestEnvironmentValueIsNeverCachedAcrossPrepares(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: DB_PASSWORD
`)
	value := "env-v1"
	rt := NewRuntime(fakeDirs{root: state}, func(string) string { return value })
	session, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if !strings.Contains(strings.Join(session.Env, " "), "env-v1") {
		t.Fatalf("first env = %v", session.Env)
	}
	value = "env-v2"
	session, err = rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if !strings.Contains(strings.Join(session.Env, " "), "env-v2") {
		t.Fatalf("second env = %v", session.Env)
	}
	if strings.Contains(strings.Join(session.Env, " "), "env-v1") {
		t.Fatalf("old value was reused: %v", session.Env)
	}
}

func TestPartialAbortReportsOverwrittenExistingFile(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte("source-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: a.txt
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: b.txt
`)
	rt := newTestRuntime(t, project, state, nil)
	session, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	session.Commit()
	bPath, _ := rt.MaterializedPath(targetFor(project), Reference{Service: "web", Delivery: DeliveryFile, Target: "b"})
	// Make the second target unwritable so the first (existing) file is
	// overwritten before the failure. The old value cannot be restored and
	// must be reported.
	if err := os.Chmod(bPath, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bPath, 0o600) })
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte("source-v2-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err = rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err == nil {
		t.Fatal("Prepare succeeded, want a write failure")
	}
	var ce *core.Error
	if !asCoreError(err, &ce) {
		t.Fatalf("error = %v", err)
	}
	var found bool
	for _, diag := range ce.AllDiagnostics() {
		if diag.Message == msgPartialOverwrite {
			found = true
		}
	}
	if !found {
		t.Fatalf("partial overwrite was not reported: %+v", ce.AllDiagnostics())
	}
}

func TestCommitOnlyCleansStaleFilesOfSelectedServices(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte("source-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: a.txt
  - service: worker
    phase: runtime
    delivery: file
    source: file
    ref: b.txt
`)
	rt := newTestRuntime(t, project, state, nil)
	session, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	session.Commit()
	aPath, _ := rt.MaterializedPath(targetFor(project), Reference{Service: "web", Delivery: DeliveryFile, Target: "a"})
	bPath, _ := rt.MaterializedPath(targetFor(project), Reference{Service: "worker", Delivery: DeliveryFile, Target: "b"})

	// web's reference disappears; a partial up of web is the safe cleanup
	// point for web only. worker's file must survive an unrelated operation.
	writeConfig(t, project, `secrets:
  - service: worker
    phase: runtime
    delivery: file
    source: file
    ref: b.txt
`)
	session, err = rt.Prepare(context.Background(), targetFor(project), []string{"web"}, ModeCreate)
	if err != nil {
		t.Fatalf("Prepare partial: %v", err)
	}
	session.Commit()
	if _, err := os.Stat(aPath); !os.IsNotExist(err) {
		t.Fatalf("stale web file was not cleaned by the web operation: %v", err)
	}
	if _, err := os.Stat(bPath); err != nil {
		t.Fatalf("unrelated worker file was removed: %v", err)
	}
}

func TestPrepareReferencesUsesTheRecordedListNotCurrentConfig(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: SECRET_B
    target: APP_TOKEN
`)
	rt := newTestRuntime(t, project, state, map[string]string{
		"SECRET_A": "value-a",
		"SECRET_B": "value-b",
	})
	recorded := []Reference{{
		Service: "web", Phase: PhaseRuntime,
		Delivery: DeliveryEnvironment, Source: SourceEnv,
		Ref: "SECRET_A", Target: "APP_TOKEN",
	}}
	session, err := rt.PrepareReferences(context.Background(), targetFor(project), recorded, []string{"web"}, ModeCreate)
	if err != nil {
		t.Fatalf("PrepareReferences: %v", err)
	}
	if session.Redact("value-a value-b") != "[redacted] value-b" {
		t.Fatalf("redaction = %q; the recorded value was not the one delivered", session.Redact("value-a value-b"))
	}
	if len(session.Env) != 1 || !strings.HasPrefix(session.Env[0], "DCRAFT_SECRET_1=value-a") {
		t.Fatalf("delivered environment = %v, want the recorded SECRET_A value", session.Env)
	}
	override, err := os.ReadFile(filepath.Join(state, "stacks", "demo", "compose.secrets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(override), "APP_TOKEN") || strings.Contains(string(override), "value-b") {
		t.Fatalf("override does not reflect the recorded reference:\n%s", override)
	}
}
