package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/rootmyself/dcraft/internal/stack"
)

func uniqueInitProject(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	project := filepath.Join(base, "dcraft-test-init-"+randomHex(t, 6))
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return project
}

func plainCompose(ctx context.Context, env []string, dir string, args ...string) *exec.Cmd {
	cmdArgs := append([]string{"compose", "-f", "compose.yaml"}, args...)
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd
}

func initCleanup(t *testing.T, env []string, project string, builtImageIDs ...string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if out, err := plainCompose(ctx, env, project, "down", "-v", "--remove-orphans").CombinedOutput(); err != nil {
			t.Logf("init test cleanup down failed: %v\n%s", err, out)
		}
		for _, id := range builtImageIDs {
			if id == "" {
				continue
			}
			if out, err := exec.Command("docker", "image", "rm", "-f", id).CombinedOutput(); err != nil {
				t.Logf("init test cleanup image %s failed: %v\n%s", id, err, out)
			}
		}
	})
}

func builtImageID(t *testing.T, env []string, project, service string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := plainCompose(ctx, env, project, "images", "-q", service).Output()
	if err != nil {
		t.Fatalf("compose images -q %s: %v", service, err)
	}
	return strings.TrimSpace(strings.Split(strings.TrimSpace(string(out)), "\n")[0])
}

func httpGetEventually(t *testing.T, url string) string {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		res, err := client.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(res.Body)
			res.Body.Close()
			if readErr == nil && res.StatusCode == http.StatusOK {
				return string(body)
			}
			lastErr = fmt.Errorf("status %d", res.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("GET %s did not succeed: %v", url, lastErr)
	return ""
}

func TestIntegrationInitDockerfileModeRunsWithPlainComposeAndStatusIdentifiesIt(t *testing.T) {
	env := requireIntegration(t)
	project := uniqueInitProject(t)
	state := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": state})
	port := freeHostPort(t)
	dockerfile := `FROM busybox:1.36
RUN mkdir -p /www && echo hello-from-dcraft > /www/index.html
EXPOSE 80
CMD ["httpd", "-f", "-p", "8080", "-h", "/www"]
`
	if err := os.WriteFile(filepath.Join(project, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}

	res := runProcess(t, project, dcraftEnv, strings.NewReader(""),
		"init", "--from", "dockerfile", "--service-name", "app",
		"--port", fmt.Sprintf("127.0.0.1:%d:8080", port), "--restart", "unless-stopped", "--json")
	if res.code != 0 {
		t.Fatalf("dcraft init exit=%d stderr=%q\n%s", res.code, res.stderr, res.stdout)
	}
	report := parseInit(t, res.stdout).Data.Init
	var generated struct {
		Services map[string]struct {
			Ports  []string `yaml:"ports"`
			Expose []string `yaml:"expose"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(report.Content), &generated); err != nil {
		t.Fatalf("parse generated compose: %v\n%s", err, report.Content)
	}
	app, ok := generated.Services["app"]
	if !ok {
		t.Fatalf("generated file has no app service:\n%s", report.Content)
	}
	wantPort := fmt.Sprintf("127.0.0.1:%d:8080", port)
	if len(app.Ports) != 1 || app.Ports[0] != wantPort {
		t.Fatalf("ports = %v, want exactly [%s] (Dockerfile EXPOSE must not be imported)\n%s", app.Ports, wantPort, report.Content)
	}
	if len(app.Expose) != 0 {
		t.Fatalf("expose = %v, want empty (Dockerfile EXPOSE must not be imported)", app.Expose)
	}
	if strings.Contains(report.Content, "command:") {
		t.Fatalf("Dockerfile CMD was imported:\n%s", report.Content)
	}
	composePath := filepath.Join(project, "compose.yaml")
	if _, err := os.Stat(composePath); err != nil {
		t.Fatalf("compose.yaml missing: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if out, err := plainCompose(ctx, env, project, "up", "-d", "--wait", "--wait-timeout", "90").CombinedOutput(); err != nil {
		t.Fatalf("plain compose up: %v\n%s", err, out)
	}
	builtImage := builtImageID(t, env, project, "app")
	initCleanup(t, env, project, builtImage)

	psCmd := plainCompose(ctx, env, project, "ps", "-q")
	psOut, err := psCmd.Output()
	if err != nil {
		t.Fatalf("compose ps: %v", err)
	}
	running := strings.TrimSpace(string(psOut))
	if running == "" {
		t.Fatal("plain compose did not start a container")
	}
	assertRunning(t, strings.Split(running, "\n")[0])

	if body := httpGetEventually(t, fmt.Sprintf("http://127.0.0.1:%d/", port)); !strings.Contains(body, "hello-from-dcraft") {
		t.Fatalf("service body = %q", body)
	}

	statusRes := runProcess(t, project, dcraftEnv, strings.NewReader(""), "status", "--json")
	if statusRes.code != 0 {
		t.Fatalf("dcraft status exit=%d\n%s", statusRes.code, statusRes.stdout)
	}
	snap := parseStatus(t, statusRes.stdout).Data.Stack
	if snap.ProjectName == "" || snap.ProjectName == "compose" {
		t.Fatalf("status project name = %q (plain Compose project not identified)", snap.ProjectName)
	}
	service := findService(t, snap, "app")
	if service.State != stack.StateRunning {
		t.Fatalf("service = %+v, want running", service)
	}
	foundPort := false
	for _, connection := range service.Connections {
		if connection.PublishedPort == port && connection.TargetPort == 8080 {
			foundPort = true
		}
	}
	if !foundPort {
		t.Fatalf("connections = %+v, want the promised %d->8080", service.Connections, port)
	}
	if service.AppVerified {
		t.Fatalf("no healthcheck was generated, so app_verified must stay false: %+v", service)
	}
}

func TestIntegrationInitImageModeWorksWithPlainComposeAndDcraftUp(t *testing.T) {
	env := requireIntegration(t)
	project := uniqueInitProject(t)
	state := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": state})
	port := freeHostPort(t)
	www := filepath.Join(project, "www")
	if err := os.MkdirAll(www, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(www, "index.html"), []byte("hello-from-dcraft-image"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := runProcess(t, project, dcraftEnv, strings.NewReader(""),
		"init", "--from", "image", "--service-name", "web", "--image", "busybox:1.36",
		"--port", fmt.Sprintf("127.0.0.1:%d:8080", port),
		"--volume", "./www:/www", "--command", "httpd -f -p 8080 -h /www", "--json")
	if res.code != 0 {
		t.Fatalf("dcraft init exit=%d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	report := parseInit(t, res.stdout).Data.Init
	if !strings.Contains(report.Content, "./www:/www") {
		t.Fatalf("relative bind source was not preserved:\n%s", report.Content)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if out, err := plainCompose(ctx, env, project, "up", "-d", "--wait", "--wait-timeout", "90").CombinedOutput(); err != nil {
		t.Fatalf("plain compose up: %v\n%s", err, out)
	}
	initCleanup(t, env, project)
	if body := httpGetEventually(t, fmt.Sprintf("http://127.0.0.1:%d/", port)); !strings.Contains(body, "hello-from-dcraft-image") {
		t.Fatalf("plain Compose service body = %q", body)
	}
	if out, err := plainCompose(ctx, env, project, "down", "-v", "--remove-orphans").CombinedOutput(); err != nil {
		t.Fatalf("plain compose down: %v\n%s", err, out)
	}

	up := runProcess(t, project, dcraftEnv, strings.NewReader(""), "up", "--json")
	if up.code != 0 {
		t.Fatalf("dcraft up exit=%d\n%s\n%s", up.code, up.stdout, up.stderr)
	}
	if body := httpGetEventually(t, fmt.Sprintf("http://127.0.0.1:%d/", port)); !strings.Contains(body, "hello-from-dcraft-image") {
		t.Fatalf("dcraft up service body = %q", body)
	}
	statusRes := runProcess(t, project, dcraftEnv, strings.NewReader(""), "status", "--json")
	if statusRes.code != 0 {
		t.Fatalf("dcraft status exit=%d\n%s", statusRes.code, statusRes.stdout)
	}
	service := findService(t, parseStatus(t, statusRes.stdout).Data.Stack, "web")
	if service.State != stack.StateRunning {
		t.Fatalf("service after dcraft up = %+v", service)
	}
	down := runProcess(t, project, dcraftEnv, strings.NewReader(""), "down", "--json")
	if down.code != 0 {
		t.Fatalf("dcraft down exit=%d\n%s\n%s", down.code, down.stdout, down.stderr)
	}
}

func TestIntegrationInitKeepsExistingFilesAndOtherFoldersUntouched(t *testing.T) {
	env := requireIntegration(t)
	project := uniqueInitProject(t)
	other := uniqueInitProject(t)

	existing := filepath.Join(project, "compose.yaml")
	oldContent := "services:\n  old:\n    image: busybox:1.36\n    command: [\"sleep\", \"180\"]\n"
	if err := os.WriteFile(existing, []byte(oldContent), 0o644); err != nil {
		t.Fatal(err)
	}
	otherCompose := filepath.Join(other, "compose.yaml")
	if err := os.WriteFile(otherCompose, []byte(oldContent), 0o644); err != nil {
		t.Fatal(err)
	}
	before := sha256File(t, existing)
	otherBefore := sha256File(t, otherCompose)
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})

	refused := runProcess(t, project, dcraftEnv, strings.NewReader(""),
		"init", "--from", "image", "--service-name", "web", "--image", "busybox:1.36", "--json")
	if refused.code != 2 {
		t.Fatalf("refused init exit=%d\n%s", refused.code, refused.stdout)
	}
	if sha256File(t, existing) != before {
		t.Fatal("refused init changed compose.yaml")
	}

	// Overwriting with the independent approval produces the new file even
	// while an unrelated folder's stack stays byte-identical.
	overwrite := runProcess(t, project, dcraftEnv, strings.NewReader(""),
		"init", "--from", "image", "--service-name", "web", "--image", "busybox:1.36",
		"--output", "compose.yaml", "--approve", "file_overwrite", "--json")
	if overwrite.code != 0 {
		t.Fatalf("overwrite exit=%d\n%s\n%s", overwrite.code, overwrite.stdout, overwrite.stderr)
	}
	if sha256File(t, existing) == before {
		t.Fatal("approved overwrite did not replace compose.yaml")
	}
	if sha256File(t, otherCompose) != otherBefore {
		t.Fatal("init changed another folder's Compose file")
	}
	// The overwritten file is ordinary, runnable Compose: Compose itself
	// accepts it without dcraft.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if out, err := plainCompose(ctx, env, project, "config", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("plain compose config rejected the overwritten file: %v\n%s", err, out)
	}
	if out, err := plainCompose(ctx, env, other, "config", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("other folder config changed: %v\n%s", err, out)
	}
}
