package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/generators"
)

// These tests run the generated script against a local bare Git repository.
// They use no network and no credentials. They prove the safe-update rules the
// unit tests can only inspect as text.

type gitFixture struct {
	t       *testing.T
	root    string
	work    string
	bare    string
	env     []string
	commits []string
}

func gitEnv(root string) []string {
	return append(os.Environ(),
		"HOME="+filepath.Join(root, "home"),
		"XDG_CONFIG_HOME="+filepath.Join(root, "home", ".config"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=dcraft test",
		"GIT_AUTHOR_EMAIL=dcraft@example.invalid",
		"GIT_COMMITTER_NAME=dcraft test",
		"GIT_COMMITTER_EMAIL=dcraft@example.invalid",
	)
}

func newGitFixture(t *testing.T) *gitFixture {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &gitFixture{t: t, root: root, work: filepath.Join(root, "work"), bare: filepath.Join(root, "repo.git"), env: gitEnv(root)}
	f.git(root, "init", "--initial-branch=main", f.work)
	f.git(root, "init", "--bare", f.bare)
	f.commit("one")
	f.git(f.work, "tag", "-a", "v1.0", "-m", "release one")
	f.commit("two")
	f.git(f.work, "remote", "add", "origin", f.bare)
	f.git(f.work, "push", "origin", "main", "refs/tags/v1.0")
	_ = git
	return f
}

func (f *gitFixture) git(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *gitFixture) commit(content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.work, "app.txt"), []byte(content+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.work, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git(f.work, "add", ".")
	f.git(f.work, "commit", "-m", content)
	f.commits = append(f.commits, f.git(f.work, "rev-parse", "HEAD"))
}

func (f *gitFixture) project() string {
	f.t.Helper()
	project := filepath.Join(f.root, "project-"+strings.ReplaceAll(f.t.Name(), "/", "-"))
	if err := os.MkdirAll(project, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return project
}

func (f *gitFixture) createScript(project, ref, dest, output string) string {
	f.t.Helper()
	layout, err := generators.ResolveLayout(project, dest, output)
	if err != nil {
		f.t.Fatal(err)
	}
	gen, err := Build(Input{Layout: layout, Repository: f.bare, Ref: ref, Stage: "pre"})
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := generators.WriteScript(layout.ScriptPath, gen.Content); err != nil {
		f.t.Fatal(err)
	}
	return layout.ScriptPath
}

func (f *gitFixture) run(script string) (string, int) {
	f.t.Helper()
	cmd := exec.Command("bash", script)
	cmd.Dir = f.root // deliberately not the project folder
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			f.t.Fatalf("run script: %v", err)
		}
	}
	return string(out), code
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGeneratedScriptClonesBranchTagAndFullSHA(t *testing.T) {
	f := newGitFixture(t)
	project := f.project()

	branchScript := f.createScript(project, "main", "src-branch", "scripts/branch.sh")
	if out, code := f.run(branchScript); code != 0 {
		t.Fatalf("branch script exit = %d\n%s", code, out)
	}
	if got := readFile(t, filepath.Join(project, "src-branch", "app.txt")); got != "two\n" {
		t.Fatalf("branch content = %q", got)
	}

	tagScript := f.createScript(project, "v1.0", "src-tag", "scripts/tag.sh")
	if out, code := f.run(tagScript); code != 0 {
		t.Fatalf("tag script exit = %d\n%s", code, out)
	}
	if got := readFile(t, filepath.Join(project, "src-tag", "app.txt")); got != "one\n" {
		t.Fatalf("tag content = %q", got)
	}
	head := f.git(filepath.Join(project, "src-tag"), "rev-parse", "HEAD")
	if head != f.commits[0] {
		t.Fatalf("annotated tag was not peeled: HEAD=%s want %s", head, f.commits[0])
	}

	shaScript := f.createScript(project, f.commits[1], "src-sha", "scripts/sha.sh")
	if out, code := f.run(shaScript); code != 0 {
		t.Fatalf("SHA script exit = %d\n%s", code, out)
	}
	if got := readFile(t, filepath.Join(project, "src-sha", "app.txt")); got != "two\n" {
		t.Fatalf("SHA content = %q", got)
	}
}

func TestGeneratedScriptUpdatesAFastForwardBranchWithoutLosingChanges(t *testing.T) {
	f := newGitFixture(t)
	project := f.project()
	script := f.createScript(project, "main", "src", "scripts/git.sh")
	if out, code := f.run(script); code != 0 {
		t.Fatalf("first run exit = %d\n%s", code, out)
	}
	f.commit("three")
	f.git(f.work, "push", "origin", "main")
	if out, code := f.run(script); code != 0 {
		t.Fatalf("update exit = %d\n%s", code, out)
	}
	if got := readFile(t, filepath.Join(project, "src", "app.txt")); got != "three\n" {
		t.Fatalf("updated content = %q", got)
	}
	if previous := f.git(filepath.Join(project, "src"), "rev-parse", "refs/dcraft/previous"); previous != f.commits[1] {
		t.Fatalf("previous ref = %s want %s", previous, f.commits[1])
	}

	local := filepath.Join(project, "src", "app.txt")
	if err := os.WriteFile(local, []byte("local work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := f.run(script)
	if code == 0 {
		t.Fatalf("dirty update succeeded:\n%s", out)
	}
	if got := readFile(t, local); got != "local work\n" {
		t.Fatalf("local change was discarded: %q", got)
	}

	// A committed local change moves HEAD away from the recorded sync commit;
	// the next run must refuse rather than make that commit hard to find.
	f.git(filepath.Join(project, "src"), "add", "app.txt")
	f.git(filepath.Join(project, "src"), "commit", "-m", "local commit")
	localHead := f.git(filepath.Join(project, "src"), "rev-parse", "HEAD")
	out, code = f.run(script)
	if code == 0 {
		t.Fatalf("local commit was replaced:\n%s", out)
	}
	if !strings.Contains(out, "마지막으로 받은 커밋") {
		t.Fatalf("local commit guidance missing:\n%s", out)
	}
	if head := f.git(filepath.Join(project, "src"), "rev-parse", "HEAD"); head != localHead {
		t.Fatalf("local HEAD changed: %s -> %s", localHead, head)
	}
	if got := readFile(t, local); got != "local work\n" {
		t.Fatalf("committed local content changed: %q", got)
	}
}

func TestGeneratedScriptRefusesNonEmptyOrForeignDestinations(t *testing.T) {
	f := newGitFixture(t)
	project := f.project()

	nonEmpty := filepath.Join(project, "existing")
	if err := os.MkdirAll(nonEmpty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := f.createScript(project, "main", "existing", "scripts/nonempty.sh")
	if out, code := f.run(script); code == 0 {
		t.Fatalf("non-empty destination accepted:\n%s", out)
	}
	if got := readFile(t, filepath.Join(nonEmpty, "keep.txt")); got != "keep\n" {
		t.Fatalf("existing file changed: %q", got)
	}

	foreign := f.createScript(project, "main", "foreign", "scripts/foreign.sh")
	if out, code := f.run(foreign); code != 0 {
		t.Fatalf("foreign setup exit = %d\n%s", code, out)
	}
	f.git(filepath.Join(project, "foreign"), "remote", "set-url", "origin", filepath.Join(f.root, "other.git"))
	if out, code := f.run(foreign); code == 0 {
		t.Fatalf("foreign origin accepted:\n%s", out)
	}
	if got := readFile(t, filepath.Join(project, "foreign", "app.txt")); got != "two\n" {
		t.Fatalf("foreign destination changed: %q", got)
	}
}

func TestGeneratedScriptFailsWithGuidanceWhenGitIsMissing(t *testing.T) {
	f := newGitFixture(t)
	project := f.project()
	script := f.createScript(project, "main", "src-missing-git", "scripts/missing-git.sh")
	emptyPath := t.TempDir()
	env := make([]string, 0, len(f.env))
	for _, kv := range f.env {
		if strings.HasPrefix(kv, "PATH=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "PATH="+emptyPath)
	cmd := exec.Command("bash", script)
	cmd.Dir = f.root
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script succeeded without git:\n%s", out)
	}
	if !strings.Contains(string(out), "git CLI를 찾지 못했습니다") {
		t.Fatalf("missing git guidance:\n%s", out)
	}
	// The guidance must be reachable without any external command: an empty
	// PATH has no dirname either. macOS bash happens to survive a failed
	// `cd ""` (it keeps the current directory), so without this assertion the
	// bug only shows on Linux runners.
	if strings.Contains(string(out), "dirname: command not found") {
		t.Fatalf("script depends on an external dirname:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(project, "src-missing-git")); !os.IsNotExist(err) {
		t.Fatalf("script created a destination without git: %v", err)
	}
}

func TestGeneratedScriptTreatsAnExplicitHexTagAsATag(t *testing.T) {
	f := newGitFixture(t)
	hexTag := "0123456789abcdef0123456789abcdef01234567"
	f.git(f.work, "tag", "-a", hexTag, "-m", "hex tag", f.commits[0])
	f.git(f.work, "push", "origin", "refs/tags/"+hexTag)
	project := f.project()
	script := f.createScript(project, "refs/tags/"+hexTag, "src-hex", "scripts/hex.sh")
	if out, code := f.run(script); code != 0 {
		t.Fatalf("hex tag script exit = %d\n%s", code, out)
	}
	if got := readFile(t, filepath.Join(project, "src-hex", "app.txt")); got != "one\n" {
		t.Fatalf("hex tag content = %q", got)
	}
}

func TestGeneratedScriptRefusesAmbiguousBranchAndTagNames(t *testing.T) {
	f := newGitFixture(t)
	f.git(f.work, "branch", "release")
	f.git(f.work, "tag", "release")
	f.git(f.work, "push", "origin", "refs/heads/release", "refs/tags/release")
	project := f.project()
	script := f.createScript(project, "release", "src", "scripts/ambiguous.sh")
	out, code := f.run(script)
	if code == 0 {
		t.Fatalf("ambiguous short name accepted:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(project, "src")); !os.IsNotExist(err) {
		t.Fatalf("ambiguous ref created a destination: %v", err)
	}
	if !strings.Contains(out, "refs/heads/release") {
		t.Fatalf("ambiguity guidance missing:\n%s", out)
	}
}
