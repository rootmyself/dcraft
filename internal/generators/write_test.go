package generators

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteScriptCreatesAnExecutableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scripts", "git-sync.sh")
	created, err := WriteScript(path, []byte("#!/usr/bin/env bash\necho hi\n"))
	if err != nil {
		t.Fatalf("WriteScript: %v", err)
	}
	if !created {
		t.Fatal("new file reported as replacement")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if string(data) != "#!/usr/bin/env bash\necho hi\n" {
		t.Fatalf("content = %q", data)
	}
}

func TestWriteScriptReplacesOnlyRegularFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.sh")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := WriteScript(path, []byte("new\n"))
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if created {
		t.Fatal("replacement reported as new file")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new\n" {
		t.Fatalf("content = %q", data)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}

	target := filepath.Join(dir, "target.sh")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.sh")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteScript(link, []byte("bad\n")); err == nil {
		t.Fatal("symlink target was replaced")
	}
	if data, _ := os.ReadFile(target); string(data) != "keep\n" {
		t.Fatalf("symlink target changed: %q", data)
	}

	if _, err := WriteScript(dir, []byte("bad\n")); err == nil {
		t.Fatal("directory was replaced")
	}
}
