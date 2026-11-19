package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
	"github.com/rootmyself/dcraft/internal/features/all"
)

type installResultJSON struct {
	Action           string            `json:"action"`
	Outcome          core.Outcome      `json:"outcome"`
	RequiredApproval core.ApprovalKind `json:"required_approval"`
	Diagnostics      []core.Diagnostic `json:"diagnostics"`
	Data             struct {
		Install struct {
			Changed          bool   `json:"changed"`
			AlreadyAvailable bool   `json:"already_available"`
			Version          string `json:"version"`
			Source           string `json:"source"`
			Destination      string `json:"destination"`
			ChecksumVerified bool   `json:"checksum_verified"`
			SizeBytes        int64  `json:"size_bytes"`
			RecheckOutput    string `json:"recheck_output"`
		} `json:"install"`
	} `json:"data"`
}

func parseInstallResult(t *testing.T, out string) installResultJSON {
	t.Helper()
	var res installResultJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse install JSON: %v\n%s", err, out)
	}
	return res
}

func findDiagnostic(res installResultJSON, cat core.Category) (core.Diagnostic, bool) {
	for _, d := range res.Diagnostics {
		if d.Category == cat {
			return d, true
		}
	}
	return core.Diagnostic{}, false
}

func joinActions(d core.Diagnostic) string {
	return strings.Join(d.NextActions, " ")
}

func TestInstallWithoutApprovalExitsTwoWithoutChanges(t *testing.T) {
	pluginDir := t.TempDir()
	// An empty folder and an empty PATH prove the command resolves its target
	// and stops at the approval gate before Docker or the network is touched.
	env := buildEnv(map[string]string{
		"PATH":                       "/usr/bin:/bin",
		"DCRAFT_STATE_DIR":           t.TempDir(),
		"DCRAFT_COMPOSE_PLUGIN_DIR":  pluginDir,
		"DCRAFT_COMPOSE_RELEASE_API": "http://127.0.0.1:1/latest",
	})
	res := runProcess(t, t.TempDir(), env, strings.NewReader(""), "install-compose", "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q)", res.code, res.stderr)
	}
	if res.stderr != "" {
		t.Fatalf("JSON mode wrote stderr: %q", res.stderr)
	}
	parsed := parseInstallResult(t, res.stdout)
	if parsed.Outcome != core.OutcomeFailure || parsed.RequiredApproval != core.ApprovalInstall {
		t.Fatalf("result = %+v", parsed)
	}
	d, ok := findDiagnostic(parsed, core.CategoryApprovalRequired)
	if !ok {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	actions := joinActions(d)
	if !strings.Contains(actions, "--approve install") {
		t.Fatalf("approval actions = %v", d.NextActions)
	}
	for _, forbidden := range []string{"sudo", "install-compose --approve install &&"} {
		if strings.Contains(actions, forbidden) {
			t.Fatalf("unsafe approval action %q: %v", forbidden, d.NextActions)
		}
	}
	if _, err := os.Stat(filepath.Join(pluginDir, "docker-compose")); !os.IsNotExist(err) {
		t.Fatalf("plugin was created without approval: %v", err)
	}

	// A human run computes and prints the plan before the approval check, so
	// the user sees the target and effect that are being refused. The JSON
	// run above still keeps stdout pure and makes no plan call.
	server := newFakeReleaseServer(t, []byte("fake asset"))
	defer server.Close()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte("#!/bin/sh\necho 'Docker version 27.0.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	humanEnv := buildEnv(map[string]string{
		"PATH":                       binDir + ":/usr/bin:/bin",
		"DCRAFT_STATE_DIR":           t.TempDir(),
		"DCRAFT_COMPOSE_PLUGIN_DIR":  pluginDir,
		"DCRAFT_COMPOSE_RELEASE_API": server.URL,
	})
	human := runProcess(t, t.TempDir(), humanEnv, strings.NewReader(""), "install-compose")
	if human.code != 2 {
		t.Fatalf("human exit = %d, want 2 (stderr=%q)", human.code, human.stderr)
	}
	if human.stdout != "" {
		t.Fatalf("human failure wrote stdout: %q", human.stdout)
	}
	for _, want := range []string{
		"승인", "Docker CLI·Engine·Desktop은 변경하지 않으며", "--approve install",
		"출처·버전·설치 경로", "Compose 설치 계획", "버전:", "대상 경로:", "변경 내용:",
	} {
		if !strings.Contains(human.stderr, want) {
			t.Fatalf("human stderr missing %q:\n%s", want, human.stderr)
		}
	}
}

func TestInstallWithMissingDockerCLIReportsDockerNotCompose(t *testing.T) {
	pluginDir := t.TempDir()
	env := buildEnv(map[string]string{
		"PATH":                       "/usr/bin:/bin",
		"DCRAFT_STATE_DIR":           t.TempDir(),
		"DCRAFT_COMPOSE_PLUGIN_DIR":  pluginDir,
		"DCRAFT_COMPOSE_RELEASE_API": "http://127.0.0.1:1/latest",
	})
	res := runProcess(t, t.TempDir(), env, strings.NewReader(""), "install-compose", "--approve", "install", "--json")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)", res.code, res.stderr)
	}
	parsed := parseInstallResult(t, res.stdout)
	if parsed.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", parsed.Outcome)
	}
	d, ok := findDiagnostic(parsed, core.CategoryDockerMissing)
	if !ok {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	actions := joinActions(d)
	if strings.Contains(actions, "install-compose") {
		t.Fatalf("Docker missing was misread as Compose install: %v", d.NextActions)
	}
	if !strings.Contains(actions, "Docker") {
		t.Fatalf("Docker missing actions = %v", d.NextActions)
	}
	if _, err := os.Stat(filepath.Join(pluginDir, "docker-compose")); !os.IsNotExist(err) {
		t.Fatalf("plugin created without Docker: %v", err)
	}
}

func TestInstallWithBlockedSourceFailsCleanly(t *testing.T) {
	fake := newInstallFakeDocker(t)
	pluginDir := t.TempDir()
	configDir := t.TempDir()
	env := buildEnv(map[string]string{
		"PATH":                       fake.dir + ":" + os.Getenv("PATH"),
		"DOCKER_FAKE_LOG":            fake.logPath,
		"DCRAFT_STATE_DIR":           t.TempDir(),
		"DCRAFT_COMPOSE_PLUGIN_DIR":  filepath.Join(pluginDir, "cli-plugins"),
		"DOCKER_CONFIG":              configDir,
		"DCRAFT_COMPOSE_RELEASE_API": "http://127.0.0.1:1/releases/latest",
	})
	res := runProcess(t, t.TempDir(), env, strings.NewReader(""), "install-compose", "--approve", "install", "--json")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)\n%s", res.code, res.stderr, res.stdout)
	}
	parsed := parseInstallResult(t, res.stdout)
	if parsed.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", parsed.Outcome)
	}
	if _, ok := findDiagnostic(parsed, core.CategoryComposeReleaseUnavailable); !ok {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(pluginDir, "cli-plugins", "docker-compose")); !os.IsNotExist(err) {
		t.Fatalf("plugin left behind after a failed resolution: %v", err)
	}
	assertNoMutatingCalls(t, fake.calls(t))
}

func TestStatusOffersInstallOnlyWhenComposeIsMissing(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("real docker CLI is not available")
	}
	project := t.TempDir()
	writeProject(t, project)

	// A Docker config without a cli-plugins directory reproduces a genuinely
	// missing Compose while using the real Docker CLI and engine.
	missingEnv := buildEnv(map[string]string{
		"PATH":             os.Getenv("PATH"),
		"DCRAFT_STATE_DIR": t.TempDir(),
		"DOCKER_CONFIG":    t.TempDir(),
	})
	missing := runProcess(t, project, missingEnv, strings.NewReader(""), "status", "--json")
	if missing.code == 0 {
		t.Skip("this Docker CLI still finds a Compose plugin without DOCKER_CONFIG")
	}
	missingRes := parseStatus(t, missing.stdout)
	d, ok := diagnosticByCategory(missingRes, core.CategoryComposeMissing)
	if !ok {
		t.Fatalf("diagnostics = %+v", missingRes.Diagnostics)
	}
	if !strings.Contains(joinActions(d), "dcraft install-compose") {
		t.Fatalf("compose_missing actions = %v", d.NextActions)
	}

	presentEnv := buildEnv(map[string]string{
		"PATH":             os.Getenv("PATH"),
		"DCRAFT_STATE_DIR": t.TempDir(),
	})
	present := runProcess(t, project, presentEnv, strings.NewReader(""), "status", "--json")
	if present.code != 0 {
		t.Skipf("real Docker engine is not available: %s", present.stderr)
	}
	presentRes := parseStatus(t, present.stdout)
	for _, diag := range presentRes.Diagnostics {
		if strings.Contains(joinActions(diag), "dcraft install-compose") {
			t.Fatalf("install offered when Compose works (%s): %v", diag.Category, diag.NextActions)
		}
	}
}

func diagnosticByCategory(res statusJSON, cat core.Category) (core.Diagnostic, bool) {
	for _, d := range res.Diagnostics {
		if d.Category == cat {
			return d, true
		}
	}
	return core.Diagnostic{}, false
}

func TestTUIConfirmationShowsPlanDeclinesAndInstallsAfterApproval(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("pty flow is verified on macOS/Linux only")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("unsupported test architecture")
	}
	asset := hostExecutableHeader(runtime.GOOS, runtime.GOARCH)
	if asset == nil {
		t.Skip("cannot construct a native header for this platform")
	}
	server := newFakeReleaseServer(t, asset)
	defer server.Close()

	fake := newInstallFakeDocker(t)
	configDir := t.TempDir()
	pluginDir := filepath.Join(configDir, "cli-plugins")
	project := t.TempDir()
	writeProject(t, project)
	env := buildEnv(map[string]string{
		"PATH":                       fake.dir + ":" + os.Getenv("PATH"),
		"DOCKER_FAKE_LOG":            fake.logPath,
		"DCRAFT_STATE_DIR":           t.TempDir(),
		"DCRAFT_COMPOSE_PLUGIN_DIR":  pluginDir,
		"DOCKER_CONFIG":              configDir,
		"DCRAFT_COMPOSE_RELEASE_API": server.URL + "/releases/latest",
		"TERM":                       "xterm-256color",
	})

	session := startInstallPTY(t, project, env)
	defer session.stop()

	session.waitFor("Docker Compose를 사용할 수 없습니다", 20*time.Second)
	presses := installComposeMenuSteps(t)
	for i := 0; i < presses; i++ {
		session.send("\x1b[B") // down arrow
	}
	session.send("\r")
	session.waitFor("승인 확인", 20*time.Second)
	session.waitFor("출처", 20*time.Second)
	session.waitFor("버전: v9.9.9", 20*time.Second)
	if _, err := os.Stat(filepath.Join(pluginDir, "docker-compose")); !os.IsNotExist(err) {
		t.Fatalf("plugin appeared before approval: %v", err)
	}

	session.send("\x1b") // esc = decline
	session.waitFor("아무것도 실행하지 않았습니다", 20*time.Second)
	if _, err := os.Stat(filepath.Join(pluginDir, "docker-compose")); !os.IsNotExist(err) {
		t.Fatalf("declining created a plugin: %v", err)
	}

	session.send("\r") // re-plan the active feature
	session.waitFor("승인 확인", 20*time.Second)
	session.send("\r") // accept
	session.waitFor("설치됨", 20*time.Second)
	info, err := os.Stat(filepath.Join(pluginDir, "docker-compose"))
	if err != nil {
		t.Fatalf("plugin was not installed after approval: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed plugin is not executable: %v", info.Mode())
	}
	session.send("q")
	if code := session.waitExit(20 * time.Second); code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, session.output())
	}
	if !strings.Contains(strings.Join(fake.calls(t), "\n"), "compose version") {
		t.Fatalf("post-install compose re-check was not executed: %v", fake.calls(t))
	}
	assertNoMutatingCalls(t, fake.calls(t))
}

// installComposeMenuSteps computes how many menu-down presses are needed from
// the primary feature to install-compose, so the test survives new registered
// features from other tickets.
func installComposeMenuSteps(t *testing.T) int {
	t.Helper()
	reg := core.NewRegistry()
	all.Register(reg, &deps.Deps{
		Getenv:     func(string) string { return "" },
		Runner:     &execxtest.Runner{},
		Downloader: &noopDownloader{},
		Installer:  &noopInstaller{},
	})
	features := reg.All()
	statusIdx, installIdx := -1, -1
	for i, f := range features {
		switch f.Descriptor().Name {
		case "status":
			statusIdx = i
		case "install-compose":
			installIdx = i
		}
	}
	if statusIdx < 0 || installIdx < 0 {
		t.Fatalf("features = %v", features)
	}
	return (installIdx - statusIdx + len(features)) % len(features)
}

type noopDownloader struct{}

func (noopDownloader) Get(context.Context, string) ([]byte, error) { return nil, errors.New("noop") }
func (noopDownloader) Download(context.Context, string, string) error {
	return errors.New("noop")
}

type noopInstaller struct{}

func (noopInstaller) Exists(string) (bool, error)                     { return false, nil }
func (noopInstaller) InstallAtomic(string, string, os.FileMode) error { return errors.New("noop") }

func newFakeReleaseServer(t *testing.T, asset []byte) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(asset)
	checksum := hex.EncodeToString(sum[:])
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			assetName := hostAssetName(runtime.GOOS, runtime.GOARCH)
			body := fmt.Sprintf(`{
  "tag_name": "v9.9.9",
  "prerelease": false,
  "draft": false,
  "assets": [
    {"name": "checksums.txt", "browser_download_url": "%s/checksums.txt", "size": %d, "digest": ""},
    {"name": %q, "browser_download_url": "%s/%s", "size": %d, "digest": ""}
  ]
}`, server.URL, len(checksum)+len(assetName)+3, assetName, server.URL, assetName, len(asset))
			_, _ = w.Write([]byte(body))
		case "/checksums.txt":
			_, _ = fmt.Fprintf(w, "%s *%s\n", checksum, hostAssetName(runtime.GOOS, runtime.GOARCH))
		case "/" + hostAssetName(runtime.GOOS, runtime.GOARCH):
			_, _ = w.Write(asset)
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

func hostAssetName(goos, goarch string) string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	return "docker-compose-" + goos + "-" + arch
}

func hostExecutableHeader(goos, goarch string) []byte {
	cpuType := map[string]uint32{"arm64": 0x0100000c, "amd64": 0x01000007}[goarch]
	if cpuType == 0 {
		return nil
	}
	switch goos {
	case "darwin":
		b := make([]byte, 32)
		copy(b, []byte{0xcf, 0xfa, 0xed, 0xfe})
		binary.LittleEndian.PutUint32(b[4:8], cpuType)
		binary.LittleEndian.PutUint32(b[12:16], 2)
		return b
	case "linux":
		machine := map[string]uint16{"arm64": 183, "amd64": 62}[goarch]
		b := make([]byte, 32)
		copy(b, []byte{0x7f, 'E', 'L', 'F'})
		b[4] = 2
		b[5] = 1
		binary.LittleEndian.PutUint16(b[16:18], 2)
		binary.LittleEndian.PutUint16(b[18:20], machine)
		return b
	default:
		return nil
	}
}

type installFakeDocker struct {
	dir     string
	logPath string
}

// newInstallFakeDocker simulates a Docker CLI whose Compose plugin is missing
// until the expected plugin file exists. It never contacts an engine for
// version checks and records every invocation.
func newInstallFakeDocker(t *testing.T) *installFakeDocker {
	t.Helper()
	dir := t.TempDir()
	f := &installFakeDocker{dir: dir, logPath: filepath.Join(dir, "calls.log")}
	script := `#!/bin/sh
LOG="${DOCKER_FAKE_LOG:?}"
printf '%s\n' "$*" >> "$LOG"
case "$*" in
  "--version") echo "Docker version 29.10.0, build fake"; exit 0 ;;
  "compose version --short")
    if [ -x "${DOCKER_CONFIG}/cli-plugins/docker-compose" ]; then echo "9.9.9"; exit 0; fi
    echo "docker: unknown command: docker compose" >&2; exit 1 ;;
  "compose version")
    if [ -x "${DOCKER_CONFIG}/cli-plugins/docker-compose" ]; then echo "Docker Compose version v9.9.9"; exit 0; fi
    echo "docker: unknown command: docker compose" >&2; exit 1 ;;
  "context ls --format json") printf '%s\n' '{"Current":true,"DockerEndpoint":"unix:///fake/docker.sock","Name":"desktop-linux"}'; exit 0 ;;
  "info --format {{.ServerVersion}}") echo "29.9.3"; exit 0 ;;
esac
echo "fake docker: unexpected invocation: $*" >&2
exit 9
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

type installPTY struct {
	t    *testing.T
	cmd  *exec.Cmd
	file *os.File
	out  *syncBuffer
	done chan error
}

func startInstallPTY(t *testing.T, dir string, env []string) *installPTY {
	t.Helper()
	cmd := exec.Command(binaryPath)
	cmd.Dir = dir
	cmd.Env = env
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	_ = pty.Setsize(f, &pty.Winsize{Rows: 50, Cols: 140})
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		_, _ = io.Copy(out, f)
		done <- cmd.Wait()
	}()
	return &installPTY{t: t, cmd: cmd, file: f, out: out, done: done}
}

func (p *installPTY) output() string { return p.out.String() }

func (p *installPTY) send(text string) {
	p.t.Helper()
	if _, err := p.file.Write([]byte(text)); err != nil {
		p.t.Fatalf("send %q: %v", text, err)
	}
	time.Sleep(80 * time.Millisecond)
}

func (p *installPTY) waitFor(text string, timeout time.Duration) {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	for !strings.Contains(p.output(), text) {
		if time.Now().After(deadline) {
			p.t.Fatalf("TUI did not render %q within %s; output:\n%s", text, timeout, p.output())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (p *installPTY) waitExit(timeout time.Duration) int {
	p.t.Helper()
	select {
	case err := <-p.done:
		if err == nil {
			return 0
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		p.t.Fatalf("wait: %v", err)
		return -1
	case <-time.After(timeout):
		_ = p.cmd.Process.Kill()
		p.t.Fatalf("TUI did not exit; output:\n%s", p.output())
		return -1
	}
}

func (p *installPTY) stop() {
	_ = p.file.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

func (f *installFakeDocker) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
