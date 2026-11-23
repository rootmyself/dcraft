package dist_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/rootmyself/dcraft/internal/core"
)

const (
	testVersion   = "0.0.0-test"
	testCommit    = "0123456789abcdef0123456789abcdef01234567"
	testBuildDate = "2026-11-18T00:00:00Z"
)

type artifact struct {
	Name   string `json:"name"`
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	SHA256 string `json:"sha256"`
}

type metadata struct {
	SchemaVersion int        `json:"schema_version"`
	Version       string     `json:"version"`
	Commit        string     `json:"commit"`
	BuildDate     string     `json:"build_date"`
	GoVersion     string     `json:"go_version"`
	Artifacts     []artifact `json:"artifacts"`
}

type processResult struct {
	stdout string
	stderr string
	code   int
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// buildEnv returns the current environment with toolchain overrides removed so
// the build script, not the developer shell, decides target and flags.
func buildEnv(overrides map[string]string) []string {
	drop := map[string]bool{
		"VERSION": true, "COMMIT": true, "BUILD_DATE": true, "DIST_DIR": true,
		"TARGETS": true, "SOURCE_DATE_EPOCH": true, "GOOS": true, "GOARCH": true,
		"CGO_ENABLED": true, "GOFLAGS": true, "GO": true, "TERM": true,
	}
	for key := range overrides {
		drop[key] = true
	}
	out := make([]string, 0, len(os.Environ())+len(overrides))
	for _, kv := range os.Environ() {
		key := strings.SplitN(kv, "=", 2)[0]
		if !drop[key] {
			out = append(out, kv)
		}
	}
	for key, value := range overrides {
		out = append(out, key+"="+value)
	}
	return out
}

func buildDist(t *testing.T, root string, env map[string]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(root, "scripts", "build.sh"))
	cmd.Dir = root
	cmd.Env = buildEnv(env)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scripts/build.sh: %v\n%s", err, out)
	}
}

func runProcess(t *testing.T, bin, dir string, env []string, stdin io.Reader, args ...string) processResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %s %v: %v", bin, args, err)
		}
		code = exitErr.ExitCode()
	}
	return processResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// artifactPlatform validates the executable container format and CPU
// architecture directly from the file header, independent of the build flags
// that produced it.
func artifactPlatform(t *testing.T, path string) (goos, goarch string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) >= 20 && bytes.Equal(data[0:4], []byte{0x7f, 'E', 'L', 'F'}) {
		if data[4] != 2 || data[5] != 1 {
			t.Fatalf("%s: unsupported ELF class/endianness %d/%d", path, data[4], data[5])
		}
		switch binary.LittleEndian.Uint16(data[18:20]) {
		case 62:
			return "linux", "amd64"
		case 183:
			return "linux", "arm64"
		}
		t.Fatalf("%s: unsupported ELF machine %d", path, binary.LittleEndian.Uint16(data[18:20]))
	}
	if len(data) >= 8 && bytes.Equal(data[0:4], []byte{0xcf, 0xfa, 0xed, 0xfe}) {
		switch binary.LittleEndian.Uint32(data[4:8]) {
		case 0x01000007:
			return "darwin", "amd64"
		case 0x0100000c:
			return "darwin", "arm64"
		}
		t.Fatalf("%s: unsupported Mach-O CPU %#x", path, binary.LittleEndian.Uint32(data[4:8]))
	}
	t.Fatalf("%s: not a Mach-O or ELF executable", path)
	return "", ""
}

func goVersionM(t *testing.T, path string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", "version", "-m", path).Output()
	if err != nil {
		t.Fatalf("go version -m %s: %v", path, err)
	}
	return string(out)
}

func readMetadata(t *testing.T, path string) metadata {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var meta metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, data)
	}
	return meta
}

var checksumLine = regexp.MustCompile(`^([0-9a-f]{64})  ([^ ]+)$`)

func readChecksums(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	sums := make(map[string]string, len(lines))
	for _, line := range lines {
		m := checksumLine.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("SHA256SUMS line is not `<64 hex>  <name>`: %q", line)
		}
		sums[m[2]] = m[1]
	}
	return sums
}

func goToolchain(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Fatalf("go env GOVERSION: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func expectedNames() []string {
	return []string{
		"dcraft_" + testVersion + "_darwin_amd64",
		"dcraft_" + testVersion + "_darwin_arm64",
		"dcraft_" + testVersion + "_linux_amd64",
		"dcraft_" + testVersion + "_linux_arm64",
	}
}

func TestBuildProducesFourArtifactsWithIntegrityMetadata(t *testing.T) {
	root := moduleRoot(t)
	dist := t.TempDir()
	buildDist(t, root, map[string]string{
		"VERSION":    testVersion,
		"COMMIT":     testCommit,
		"BUILD_DATE": testBuildDate,
		"DIST_DIR":   dist,
	})

	names := expectedNames()
	found := map[string]artifact{}
	for _, name := range names {
		path := filepath.Join(dist, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("missing artifact %s: %v", name, err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("%s is not executable (mode %v)", name, info.Mode())
		}
		goos, goarch := artifactPlatform(t, path)
		if want := strings.Split(strings.TrimPrefix(name, "dcraft_"+testVersion+"_"), "_"); goos != want[0] || goarch != want[1] {
			t.Fatalf("%s: file header is %s/%s, want %s/%s", name, goos, goarch, want[0], want[1])
		}
		buildInfo := goVersionM(t, path)
		if !strings.Contains(buildInfo, "\tpath\tgithub.com/rootmyself/dcraft/cmd/dcraft") {
			t.Fatalf("%s: build info lacks the command path:\n%s", name, buildInfo)
		}
		if !strings.Contains(buildInfo, "\tmod\tgithub.com/rootmyself/dcraft") {
			t.Fatalf("%s: build info lacks the module:\n%s", name, buildInfo)
		}
		for _, setting := range []string{
			"\tbuild\t-trimpath=true",
			"\tbuild\tCGO_ENABLED=0",
			"\tbuild\tGOOS=" + goos,
			"\tbuild\tGOARCH=" + goarch,
		} {
			if !strings.Contains(buildInfo, setting) {
				t.Fatalf("%s: build info lacks %q:\n%s", name, setting, buildInfo)
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, embedded := range []string{testVersion, testCommit, testBuildDate} {
			if !bytes.Contains(data, []byte(embedded)) {
				t.Fatalf("%s: ldflags value %q is not embedded in the binary", name, embedded)
			}
		}
		found[name] = artifact{Name: name, GOOS: goos, GOARCH: goarch, SHA256: sha256File(t, path)}
	}

	sums := readChecksums(t, filepath.Join(dist, "SHA256SUMS"))
	if len(sums) != len(names) {
		t.Fatalf("SHA256SUMS has %d entries, want %d: %v", len(sums), len(names), sums)
	}
	for _, name := range names {
		if sums[name] != found[name].SHA256 {
			t.Fatalf("SHA256SUMS[%s] = %q, want %q", name, sums[name], found[name].SHA256)
		}
	}

	meta := readMetadata(t, filepath.Join(dist, "build-info.json"))
	if meta.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", meta.SchemaVersion)
	}
	if meta.Version != testVersion || meta.Commit != testCommit || meta.BuildDate != testBuildDate {
		t.Fatalf("metadata identification = %+v", meta)
	}
	if meta.GoVersion != goToolchain(t) {
		t.Fatalf("metadata go_version = %q, want %q", meta.GoVersion, goToolchain(t))
	}
	if len(meta.Artifacts) != len(names) {
		t.Fatalf("metadata has %d artifacts, want %d", len(meta.Artifacts), len(names))
	}
	for _, want := range meta.Artifacts {
		got, ok := found[want.Name]
		if !ok {
			t.Fatalf("metadata lists unexpected artifact %q", want.Name)
		}
		if want.GOOS != got.GOOS || want.GOARCH != got.GOARCH || want.SHA256 != got.SHA256 {
			t.Fatalf("metadata artifact %q = %+v, want %+v", want.Name, want, got)
		}
	}

	// Native process checks: run the artifact built for this machine.
	native := "dcraft_" + testVersion + "_" + runtime.GOOS + "_" + runtime.GOARCH
	nativePath := filepath.Join(dist, native)
	if _, err := os.Stat(nativePath); err == nil {
		verifyNativeArtifact(t, nativePath)
	}
}

func verifyNativeArtifact(t *testing.T, bin string) {
	t.Helper()
	env := buildEnv(map[string]string{
		"PATH":             "/usr/bin:/bin",
		"DCRAFT_STATE_DIR": t.TempDir(),
	})
	wantVersion := fmt.Sprintf(
		"dcraft %s (commit %s, built %s, %s/%s, %s)\n",
		testVersion, testCommit, testBuildDate, runtime.GOOS, runtime.GOARCH, goToolchain(t),
	)
	res := runProcess(t, bin, t.TempDir(), env, strings.NewReader(""), "--version")
	if res.code != 0 || res.stdout != wantVersion || res.stderr != "" {
		t.Fatalf("--version: exit=%d stdout=%q stderr=%q, want %q", res.code, res.stdout, res.stderr, wantVersion)
	}

	res = runProcess(t, bin, t.TempDir(), env, strings.NewReader(""), "--help")
	if res.code != 0 {
		t.Fatalf("--help exit = %d, stderr=%q", res.code, res.stderr)
	}
	for _, want := range []string{"사용법:", "사용 가능한 명령:", "status"} {
		if !strings.Contains(res.stdout, want) {
			t.Fatalf("--help missing %q:\n%s", want, res.stdout)
		}
	}

	empty := t.TempDir()
	res = runProcess(t, bin, empty, env, strings.NewReader(""), "status", "--path", empty, "--json")
	if res.code != 1 {
		t.Fatalf("status in empty folder exit = %d, want 1 (stderr=%q)", res.code, res.stderr)
	}
	if res.stderr != "" {
		t.Fatalf("JSON mode wrote stderr: %q", res.stderr)
	}
	var status struct {
		Outcome     core.Outcome      `json:"outcome"`
		Diagnostics []core.Diagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &status); err != nil {
		t.Fatalf("parse status JSON: %v\n%s", err, res.stdout)
	}
	if status.Outcome != core.OutcomeFailure {
		t.Fatalf("status outcome = %q, want failure", status.Outcome)
	}
	foundCategory := false
	for _, d := range status.Diagnostics {
		if d.Category == core.CategoryNoConfiguration {
			foundCategory = true
			if len(d.NextActions) == 0 {
				t.Fatal("no_configuration has no Korean next actions")
			}
		}
	}
	if !foundCategory {
		t.Fatalf("status diagnostics = %+v", status.Diagnostics)
	}

	res = runProcess(t, bin, empty, env, strings.NewReader(""), "status", "--path", empty)
	if res.code != 1 || !strings.Contains(res.stderr, "Compose 설정 찾지 못함") || !strings.Contains(res.stderr, "다음 행동") {
		t.Fatalf("human status failure: exit=%d stderr=%q", res.code, res.stderr)
	}

	res = runProcess(t, bin, empty, env, strings.NewReader(""))
	if res.code != 2 {
		t.Fatalf("non-interactive no-args exit = %d, want 2 (stderr=%q)", res.code, res.stderr)
	}
	if strings.Contains(res.stdout, "\x1b[") {
		t.Fatalf("non-interactive invocation rendered TUI escapes: %q", res.stdout)
	}
	for _, want := range []string{"대화형 터미널이 아니어서", "dcraft status"} {
		if !strings.Contains(res.stderr, want) {
			t.Fatalf("non-interactive stderr missing %q:\n%s", want, res.stderr)
		}
	}

	verifyNativeTUI(t, bin, env, empty)
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func verifyNativeTUI(t *testing.T, bin string, env []string, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, env...), "TERM=xterm-256color")
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	defer f.Close()
	_ = pty.Setsize(f, &pty.Winsize{Rows: 40, Cols: 120})

	out := &syncBuffer{}
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(out, f)
		close(readDone)
	}()
	waitText := "Compose 설정 찾지 못함"
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(out.String(), waitText) {
		if time.Now().After(deadline) {
			t.Fatalf("TUI did not render %q; output:\n%s", waitText, out.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := f.Write([]byte("q")); err != nil {
		t.Fatalf("send quit: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("TUI exit: %v", err)
		}
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("TUI did not quit; output:\n%s", out.String())
	}
}

func TestRebuildProducesIdenticalArtifactsAndIdentification(t *testing.T) {
	root := moduleRoot(t)
	first := t.TempDir()
	second := t.TempDir()
	env := map[string]string{
		"VERSION":    testVersion,
		"COMMIT":     testCommit,
		"BUILD_DATE": testBuildDate,
		"TARGETS":    "darwin/arm64",
	}
	env["DIST_DIR"] = first
	buildDist(t, root, env)
	env["DIST_DIR"] = second
	buildDist(t, root, env)

	for _, file := range []string{"SHA256SUMS", "build-info.json"} {
		a, err := os.ReadFile(filepath.Join(first, file))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(second, file))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Fatalf("%s differs between two builds of the same commit:\n%s\n---\n%s", file, a, b)
		}
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		bin := filepath.Join(first, "dcraft_"+testVersion+"_darwin_arm64")
		env := buildEnv(map[string]string{"PATH": "/usr/bin:/bin", "DCRAFT_STATE_DIR": t.TempDir()})
		a := runProcess(t, bin, t.TempDir(), env, strings.NewReader(""), "--version")
		b := runProcess(t, bin, t.TempDir(), env, strings.NewReader(""), "--version")
		if a != b || a.code != 0 {
			t.Fatalf("identification differs: %+v vs %+v", a, b)
		}
	}
}

func TestPlainBuildKeepsDevelopmentDefaults(t *testing.T) {
	root := moduleRoot(t)
	bin := filepath.Join(t.TempDir(), "dcraft")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/dcraft")
	cmd.Dir = root
	cmd.Env = buildEnv(map[string]string{"CGO_ENABLED": "0"})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	want := fmt.Sprintf(
		"dcraft dev (commit none, built unknown, %s/%s, %s)\n",
		runtime.GOOS, runtime.GOARCH, goToolchain(t),
	)
	res := runProcess(t, bin, t.TempDir(), buildEnv(nil), strings.NewReader(""), "--version")
	if res.code != 0 || res.stdout != want || res.stderr != "" {
		t.Fatalf("plain build --version: exit=%d stdout=%q stderr=%q, want %q", res.code, res.stdout, res.stderr, want)
	}
}
