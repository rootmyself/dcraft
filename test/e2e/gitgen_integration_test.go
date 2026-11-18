package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

// gitgenRepo is a local bare Git source used by the real integration test. It
// needs no network and no credentials.
type gitgenRepo struct {
	work string
	bare string
	env  []string
}

func newGitgenRepo(t *testing.T, root string) *gitgenRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := filepath.Join(root, "source-work")
	bare := filepath.Join(root, "source.git")
	home := filepath.Join(root, "git-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=dcraft test",
		"GIT_AUTHOR_EMAIL=dcraft@example.invalid",
		"GIT_COMMITTER_NAME=dcraft test",
		"GIT_COMMITTER_EMAIL=dcraft@example.invalid",
	)
	r := &gitgenRepo{work: work, bare: bare, env: env}
	r.git(t, root, "init", "--initial-branch=main", work)
	r.git(t, root, "init", "--bare", bare)
	r.writeAndPush(t, "one", "one")
	r.writeAndPush(t, "two", "two")
	r.git(t, work, "remote", "add", "origin", bare)
	r.git(t, work, "push", "origin", "main")
	return r
}

func (r *gitgenRepo) git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *gitgenRepo) writeAndPush(t *testing.T, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.work, "app.txt"), []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM busybox:1.36\nCOPY app.txt /app/app.txt\nCMD [\"sleep\", \"600\"]\n"
	if err := os.WriteFile(filepath.Join(r.work, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	r.git(t, r.work, "add", ".")
	r.git(t, r.work, "commit", "-m", message)
}

func TestIntegrationGeneratedGitHookFeedsTheBuildInputAndProtectsChanges(t *testing.T) {
	env := requireIntegration(t)
	root := t.TempDir()
	source := newGitgenRepo(t, root)
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	projectName := "dcraft-gitgen-" + randomHex(t, 6)
	compose := fmt.Sprintf(`name: %s
services:
  app:
    build:
      context: ./src
    image: %s:test
    command: ["sleep", "600"]
`, projectName, projectName)
	composePath := filepath.Join(project, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupLifecycleCompose(t, env, project, projectName, composePath, true) })
	state := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": state})

	generated := runProcess(t, project, dcraftEnv, strings.NewReader(""),
		"generate-git-hook",
		"--repo", source.bare,
		"--ref", "main",
		"--dest", "src",
		"--output", "scripts/git-sync.sh",
		"--approve", "file_overwrite",
		"--json",
	)
	if generated.code != 0 {
		t.Fatalf("generate exit = %d stderr=%q stdout=%s", generated.code, generated.stderr, generated.stdout)
	}
	scriptPath := filepath.Join(project, "scripts", "git-sync.sh")
	if info, err := os.Stat(scriptPath); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("generated script = %v, %v", info, err)
	}

	register := runProcess(t, project, dcraftEnv, strings.NewReader(""),
		"hooks", "--add", "--stage", "pre", "--script", "scripts/git-sync.sh", "--json")
	if register.code != 0 {
		t.Fatalf("hooks add exit = %d stderr=%q stdout=%s", register.code, register.stderr, register.stdout)
	}
	if _, err := os.Stat(filepath.Join(project, ".dcraft.yaml")); err != nil {
		t.Fatalf("registration missing: %v", err)
	}

	// Registration is not trust: the script must not run and no stack or
	// destination may be created.
	untrusted := runProcess(t, project, dcraftEnv, strings.NewReader(""), "up", "--json")
	if untrusted.code != 2 {
		t.Fatalf("untrusted up exit = %d stderr=%q stdout=%s", untrusted.code, untrusted.stderr, untrusted.stdout)
	}
	untrustedRes := parseAnyResult(t, untrusted.stdout)
	if !hasResultCategory(untrustedRes, core.CategoryApprovalRequired) {
		t.Fatalf("untrusted diagnostics = %+v", untrustedRes.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(project, "src")); !os.IsNotExist(err) {
		t.Fatalf("untrusted hook created the destination: %v", err)
	}
	if ids := strings.TrimSpace(composeInspectAll(t, env, projectName, composePath)); ids != "" {
		t.Fatalf("untrusted up changed the stack: %s", ids)
	}

	up := runProcess(t, project, dcraftEnv, strings.NewReader(""), "up", "--json", "--approve", "script_trust")
	if up.code != 0 {
		t.Fatalf("up exit = %d stderr=%q stdout=%s", up.code, up.stderr, up.stdout)
	}
	if data, err := os.ReadFile(filepath.Join(project, "src", "app.txt")); err != nil || string(data) != "two\n" {
		t.Fatalf("pre-hook checkout = %q, %v", data, err)
	}
	idsAfterUp := strings.TrimSpace(composeInspectAll(t, env, projectName, composePath))
	if idsAfterUp == "" {
		t.Fatal("up created no containers")
	}
	if got := gitgenExec(t, env, project, projectName, composePath); got != "two" {
		t.Fatalf("image was not built from the pre-hook destination: %q", got)
	}

	// A local change in the managed destination must block the next update
	// instead of discarding the user's work.
	local := filepath.Join(project, "src", "app.txt")
	if err := os.WriteFile(local, []byte("local work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocked := runProcess(t, project, dcraftEnv, strings.NewReader(""), "up", "--json", "--approve", "script_trust")
	if blocked.code == 0 {
		t.Fatalf("up with a dirty destination succeeded:\n%s", blocked.stdout)
	}
	blockedRes := parseAnyResult(t, blocked.stdout)
	if !hasResultCategory(blockedRes, core.CategoryHookFailed) {
		t.Fatalf("blocked diagnostics = %+v", blockedRes.Diagnostics)
	}
	if data, _ := os.ReadFile(local); string(data) != "local work\n" {
		t.Fatalf("local change was discarded: %q", data)
	}
	if ids := strings.TrimSpace(composeInspectAll(t, env, projectName, composePath)); ids != idsAfterUp {
		t.Fatalf("blocked up changed the stack: %s -> %s", idsAfterUp, ids)
	}
}

func gitgenExec(t *testing.T, env []string, dir, project, file string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := composeCommand(ctx, env, dir, project, []string{file}, "exec", "-T", "app", "cat", "/app/app.txt")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compose exec: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}
