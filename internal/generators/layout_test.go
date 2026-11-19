package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests cover the path safety every generated host script shares:
// the destination is project data, the output is a script file, and neither
// may silently point at the project root, another path outside the project,
// or each other.

func canonical(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestResolveLayoutAcceptsProjectRelativePaths(t *testing.T) {
	project := canonical(t, t.TempDir())
	layout, err := ResolveLayout(project, "src", "scripts/git-sync.sh")
	if err != nil {
		t.Fatalf("ResolveLayout: %v", err)
	}
	if layout.Destination != filepath.Join(project, "src") {
		t.Fatalf("destination = %q", layout.Destination)
	}
	if layout.ScriptPath != filepath.Join(project, "scripts", "git-sync.sh") {
		t.Fatalf("script = %q", layout.ScriptPath)
	}
	if layout.DestinationRel != "src" || layout.ScriptRel != filepath.Join("scripts", "git-sync.sh") {
		t.Fatalf("relative values = %q / %q", layout.DestinationRel, layout.ScriptRel)
	}
}

func TestResolveLayoutRejectsEmptyInputs(t *testing.T) {
	project := t.TempDir()
	for _, tc := range []struct{ dest, output string }{
		{"", "scripts/a.sh"},
		{"src", ""},
	} {
		if _, err := ResolveLayout(project, tc.dest, tc.output); err == nil {
			t.Fatalf("empty input accepted: %+v", tc)
		}
	}
}

func TestResolveLayoutRejectsTraversalOutsideTheProject(t *testing.T) {
	project := t.TempDir()
	if _, err := ResolveLayout(project, "../elsewhere", "scripts/a.sh"); err == nil {
		t.Fatal("destination traversal accepted")
	}
	if _, err := ResolveLayout(project, "src", "../outside.sh"); err == nil {
		t.Fatal("script traversal accepted")
	}
	// An absolute path outside the project is the same decision.
	outside := t.TempDir()
	if _, err := ResolveLayout(project, outside, "scripts/a.sh"); err == nil {
		t.Fatal("absolute outside destination accepted")
	}
}

func TestResolveLayoutRejectsProjectRootAsDestination(t *testing.T) {
	project := t.TempDir()
	for _, dest := range []string{".", "./", project} {
		if _, err := ResolveLayout(project, dest, "scripts/a.sh"); err == nil {
			t.Fatalf("project root destination accepted: %q", dest)
		}
	}
}

func TestResolveLayoutRejectsScriptDirectoryAsDestination(t *testing.T) {
	project := t.TempDir()
	if _, err := ResolveLayout(project, "scripts", "scripts/a.sh"); err == nil {
		t.Fatal("destination equal to the script directory accepted")
	}
}

func TestResolveLayoutRejectsScriptInsideDestination(t *testing.T) {
	project := t.TempDir()
	if _, err := ResolveLayout(project, "src", "src/a.sh"); err == nil {
		t.Fatal("script inside the synced destination accepted")
	}
}

func TestResolveLayoutRejectsDotGitComponents(t *testing.T) {
	project := t.TempDir()
	for _, tc := range []struct{ dest, output string }{
		{".git/hooks", "scripts/a.sh"},
		{"src", ".git/a.sh"},
	} {
		if _, err := ResolveLayout(project, tc.dest, tc.output); err == nil {
			t.Fatalf("dot-git path accepted: %+v", tc)
		}
	}
}

func TestResolveLayoutAlwaysRejectsPathsOutsideTheProject(t *testing.T) {
	project := canonical(t, t.TempDir())
	outside := canonical(t, t.TempDir())
	if _, err := ResolveLayout(project, filepath.Join(outside, "data"), filepath.Join(outside, "scripts", "a.sh")); err == nil {
		t.Fatal("outside destination accepted; the removed --allow-outside flag left a bypass")
	}
	if _, err := ResolveLayout(project, "data", filepath.Join(outside, "scripts", "a.sh")); err == nil {
		t.Fatal("outside script path accepted")
	}
	if _, err := ResolveLayout(project, project, filepath.Join(project, "scripts", "a.sh")); err == nil {
		t.Fatal("project root accepted as the destination")
	}
}

func TestResolveLayoutRejectsSymlinkScriptAndNonDirectoryDestination(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(project, "real.sh")
	if err := os.WriteFile(target, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(project, "scripts", "link.sh")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLayout(project, "src", "scripts/link.sh"); err == nil {
		t.Fatal("symlink script accepted")
	}

	file := filepath.Join(project, "srcfile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLayout(project, "srcfile", "scripts/a.sh"); err == nil {
		t.Fatal("regular file accepted as destination")
	}
}

func TestResolveLayoutRejectsControlCharacters(t *testing.T) {
	project := t.TempDir()
	if _, err := ResolveLayout(project, "src\nnext", "scripts/a.sh"); err == nil {
		t.Fatal("control character in destination accepted")
	}
	if _, err := ResolveLayout(project, "src", "scripts/a\x7f.sh"); err == nil {
		t.Fatal("control character in script path accepted")
	}
}

func TestResolveLayoutRecordsHowFarTheProjectIsFromTheScript(t *testing.T) {
	project := canonical(t, t.TempDir())
	layout, err := ResolveLayout(project, "src", "scripts/nested/git.sh")
	if err != nil {
		t.Fatal(err)
	}
	if layout.ScriptToProject != filepath.Join("..", "..") {
		t.Fatalf("script-to-project = %q", layout.ScriptToProject)
	}
	if !strings.HasPrefix(layout.Destination, project+string(filepath.Separator)) {
		t.Fatalf("destination outside project: %q", layout.Destination)
	}
}
