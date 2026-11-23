package secretref

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func buildHints(project string, services ...string) map[string]BuildServiceHint {
	hints := map[string]BuildServiceHint{}
	for _, name := range services {
		hints[name] = BuildServiceHint{
			HasBuild:    true,
			Dockerfiles: []string{filepath.Join(project, "Dockerfile."+name)},
		}
	}
	return hints
}

func writeDockerfile(t *testing.T, project, name, content string) string {
	t.Helper()
	path := filepath.Join(project, "Dockerfile."+name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrepareBuildMaterializesSourcesAndWritesMountOverride(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	envSentinel := "sentinel-build-env-4a1c"
	fileSentinel := "sentinel-build-file-9d2e"
	if err := os.MkdirAll(filepath.Join(project, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "secrets", "token.txt"), []byte(fileSentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: env
    ref: BUILD_TOKEN
    target: web_token
  - service: worker
    phase: build
    delivery: file
    source: file
    ref: secrets/token.txt
    target: worker_token
`)
	writeDockerfile(t, project, "web", "FROM busybox\nRUN --mount=type=secret,id=web_token cat /run/secrets/web_token > /out\n")
	writeDockerfile(t, project, "worker", "FROM busybox\nRUN --mount=type=secret,id=worker_token cat /run/secrets/worker_token > /out\n")
	rt := newTestRuntime(t, project, state, map[string]string{"BUILD_TOKEN": envSentinel})

	session, err := rt.PrepareBuild(context.Background(), targetFor(project), nil, buildHints(project, "web", "worker"))
	if err != nil {
		t.Fatalf("PrepareBuild: %v", err)
	}
	if len(session.Files) != 1 || !strings.HasSuffix(session.Files[0], buildOverrideName) {
		t.Fatalf("files = %v", session.Files)
	}
	override, err := os.ReadFile(session.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(override)
	for _, want := range []string{"services:", "build:", "secrets:", "web_token", "worker_token", "dcraft_build_secret_"} {
		if !strings.Contains(text, want) {
			t.Fatalf("override missing %q:\n%s", want, text)
		}
	}
	for _, sentinel := range []string{envSentinel, fileSentinel} {
		if strings.Contains(text, sentinel) {
			t.Fatalf("override leaked %q:\n%s", sentinel, text)
		}
	}
	info, err := os.Stat(session.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("override mode = %o, want 600", info.Mode().Perm())
	}

	envPath, err := rt.BuildMaterializedPath(targetFor(project), Reference{
		Service: "web", Phase: PhaseBuild, Delivery: DeliveryFile, Source: SourceEnv, Ref: "BUILD_TOKEN", Target: "web_token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(envPath); err != nil || string(data) != envSentinel {
		t.Fatalf("env materialization = %q, %v", data, err)
	}
	if info, err := os.Stat(envPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("env materialization mode = %v, %v", info, err)
	}
	filePath, err := rt.BuildMaterializedPath(targetFor(project), Reference{
		Service: "worker", Phase: PhaseBuild, Delivery: DeliveryFile, Source: SourceFile, Ref: "secrets/token.txt", Target: "worker_token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filePath); err != nil || string(data) != fileSentinel+"\n" {
		t.Fatalf("file materialization = %q, %v", data, err)
	}

	for _, sentinel := range []string{envSentinel, fileSentinel} {
		if redacted := session.Redact("log: " + sentinel); strings.Contains(redacted, sentinel) {
			t.Fatalf("redaction left %q: %q", sentinel, redacted)
		}
	}

	// Environment-source files exist only for the build command.
	if err := session.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatalf("environment materialization survived the build: %v", err)
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("file materialization was removed before the safe point: %v", err)
	}
	if _, err := os.Stat(session.Files[0]); err != nil {
		t.Fatalf("build override was removed: %v", err)
	}
	session.Commit()
	if err := rt.CleanupStack(targetFor(project)); err != nil {
		t.Fatalf("CleanupStack: %v", err)
	}
	for _, path := range []string{session.Files[0], filePath, filepath.Dir(filePath)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("teardown left %s: %v", path, err)
		}
	}
}

func TestPrepareBuildOnlyResolvesSelectedServices(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: env
    ref: WEB_BUILD_TOKEN
  - service: worker
    phase: build
    delivery: file
    source: env
    ref: WORKER_BUILD_TOKEN
`)
	writeDockerfile(t, project, "web", "FROM busybox\nRUN --mount=type=secret,id=WEB_BUILD_TOKEN true\n")
	writeDockerfile(t, project, "worker", "FROM busybox\nRUN --mount=type=secret,id=WORKER_BUILD_TOKEN true\n")
	accessed := map[string]int{}
	rt := NewRuntime(fakeDirs{root: state}, func(name string) string {
		accessed[name]++
		if name == "WEB_BUILD_TOKEN" {
			return "web-value"
		}
		return ""
	})
	session, err := rt.PrepareBuild(context.Background(), targetFor(project), []string{"web"}, buildHints(project, "web", "worker"))
	if err != nil {
		t.Fatalf("PrepareBuild: %v", err)
	}
	if accessed["WORKER_BUILD_TOKEN"] != 0 {
		t.Fatalf("unselected build reference was resolved: %v", accessed)
	}
	override, _ := os.ReadFile(session.Files[0])
	if strings.Contains(string(override), "worker") {
		t.Fatalf("override contains an unselected service:\n%s", override)
	}
	if err := session.Finish(); err != nil {
		t.Fatal(err)
	}
	session.Commit()
}

func TestPrepareBuildFailsBeforeWritingOnMissingSource(t *testing.T) {
	cases := []struct {
		name   string
		config string
		env    map[string]string
	}{
		{
			name: "missing environment value",
			config: `secrets:
  - service: web
    phase: build
    delivery: file
    source: env
    ref: MISSING_BUILD_TOKEN
`,
		},
		{
			name: "missing file",
			config: `secrets:
  - service: web
    phase: build
    delivery: file
    source: file
    ref: secrets/does-not-exist.txt
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			state := t.TempDir()
			writeConfig(t, project, tc.config)
			writeDockerfile(t, project, "web", "FROM busybox\nRUN --mount=type=secret,id=token true\n")
			rt := newTestRuntime(t, project, state, tc.env)
			_, err := rt.PrepareBuild(context.Background(), targetFor(project), nil, buildHints(project, "web"))
			if err == nil {
				t.Fatal("PrepareBuild succeeded with a missing source")
			}
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategorySecretSource {
				t.Fatalf("error = %v, want secret_source_unavailable", err)
			}
			stackDir := filepath.Join(state, "stacks", "demo")
			for _, path := range []string{
				filepath.Join(stackDir, buildOverrideName),
				filepath.Join(stackDir, buildSecretsDir),
			} {
				if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
					t.Fatalf("missing source left %s: %v", path, statErr)
				}
			}
		})
	}
}

func TestPrepareBuildRejectsNonBuildAndUnknownServices(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: env
    ref: BUILD_TOKEN
`)
	rt := newTestRuntime(t, project, state, map[string]string{"BUILD_TOKEN": "value"})

	hints := map[string]BuildServiceHint{"web": {HasBuild: false}}
	_, err := rt.PrepareBuild(context.Background(), targetFor(project), nil, hints)
	if err == nil {
		t.Fatal("PrepareBuild accepted a non-build service")
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategoryBuildServiceInvalid {
		t.Fatalf("error = %v, want build_service_invalid", err)
	}

	hints = map[string]BuildServiceHint{}
	_, err = rt.PrepareBuild(context.Background(), targetFor(project), nil, hints)
	if err == nil {
		t.Fatal("PrepareBuild accepted an unknown service")
	}
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategoryServiceUnknown {
		t.Fatalf("error = %v, want service_unknown", err)
	}
}

func TestPrepareBuildWarnsWithoutEditingDockerfile(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: env
    ref: BUILD_TOKEN
    target: web_token
`)
	dockerfile := writeDockerfile(t, project, "web", "FROM busybox\nRUN echo no-secret-here\n")
	before, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, project, state, map[string]string{"BUILD_TOKEN": "value"})
	session, err := rt.PrepareBuild(context.Background(), targetFor(project), nil, buildHints(project, "web"))
	if err != nil {
		t.Fatalf("PrepareBuild: %v", err)
	}
	notices := strings.Join(session.Notices, "\n")
	for _, want := range []string{"Dockerfile", dockerfile, "web_token", "--mount=type=secret,id=web_token", "자동으로 수정하지 않음"} {
		if !strings.Contains(notices, want) {
			t.Fatalf("notices missing %q:\n%s", want, notices)
		}
	}
	after, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("dcraft modified the Dockerfile")
	}
	if err := session.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareBuildDisclosesCacheAndDockerfileLimits(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: env
    ref: BUILD_TOKEN
`)
	writeDockerfile(t, project, "web", "FROM busybox\nRUN --mount=type=secret,id=BUILD_TOKEN true\n")
	rt := newTestRuntime(t, project, state, map[string]string{"BUILD_TOKEN": "value"})
	session, err := rt.PrepareBuild(context.Background(), targetFor(project), nil, buildHints(project, "web"))
	if err != nil {
		t.Fatal(err)
	}
	notices := strings.Join(session.Notices, "\n")
	for _, want := range []string{"캐시", "--no-cache", "ARG", "ENV", "출력"} {
		if !strings.Contains(notices, want) {
			t.Fatalf("notices missing %q:\n%s", want, notices)
		}
	}
	if err := session.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareBuildHandlesInlineDockerfileWithoutMount(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: env
    ref: BUILD_TOKEN
    target: web_token
`)
	rt := newTestRuntime(t, project, state, map[string]string{"BUILD_TOKEN": "value"})
	hints := map[string]BuildServiceHint{
		"web": {HasBuild: true, InlineDockerfiles: []string{"FROM busybox\nRUN echo no-secret\n"}},
	}
	session, err := rt.PrepareBuild(context.Background(), targetFor(project), nil, hints)
	if err != nil {
		t.Fatalf("PrepareBuild: %v", err)
	}
	notices := strings.Join(session.Notices, "\n")
	if !strings.Contains(notices, "dockerfile_inline") || !strings.Contains(notices, "--mount=type=secret,id=web_token") {
		t.Fatalf("inline Dockerfile warning missing:\n%s", notices)
	}
	if err := session.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareBuildRecognizesInlineDockerfileMount(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: env
    ref: BUILD_TOKEN
    target: web_token
`)
	rt := newTestRuntime(t, project, state, map[string]string{"BUILD_TOKEN": "value"})
	hints := map[string]BuildServiceHint{
		"web": {HasBuild: true, InlineDockerfiles: []string{"FROM busybox\nRUN --mount=type=secret,id=web_token cat /run/secrets/web_token > /out\n"}},
	}
	session, err := rt.PrepareBuild(context.Background(), targetFor(project), nil, hints)
	if err != nil {
		t.Fatalf("PrepareBuild: %v", err)
	}
	if strings.Contains(strings.Join(session.Notices, "\n"), "참조가 없음") {
		t.Fatalf("inline mount was not recognized: %v", session.Notices)
	}
	if err := session.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareBuildRefusesStateInsideProject(t *testing.T) {
	project := t.TempDir()
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: env
    ref: BUILD_TOKEN
`)
	writeDockerfile(t, project, "web", "FROM busybox\nRUN --mount=type=secret,id=BUILD_TOKEN true\n")
	dirs := fakeDirs{root: filepath.Join(project, "state")}
	rt := NewRuntime(dirs, func(string) string { return "value" })
	_, err := rt.PrepareBuild(context.Background(), targetFor(project), nil, buildHints(project, "web"))
	if err == nil {
		t.Fatal("PrepareBuild accepted a state directory inside the project")
	}
	if !strings.Contains(err.Error(), "프로젝트") {
		t.Fatalf("error = %v", err)
	}
}
