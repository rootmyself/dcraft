package initconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".dcraft-init-") {
			out = append(out, entry.Name())
		}
	}
	return out
}

func TestFileSaverCreatesNewFileAtomicallyWithoutTemps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	saver := FileSaver{}
	if err := saver.Save(context.Background(), path, []byte("services: {}\n"), false); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "services: {}\n" {
		t.Fatalf("content = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o600 != 0o600 || info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("mode = %o, want an owner-readable, non-executable Compose file", info.Mode().Perm())
	}
	if temps := tempFiles(t, dir); len(temps) != 0 {
		t.Fatalf("temp files left: %v", temps)
	}
}

func TestFileSaverRefusesExistingFileWithoutOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("OLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := FileSaver{}.Save(context.Background(), path, []byte("NEW\n"), false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("error = %v, want ErrExists", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "OLD\n" {
		t.Fatalf("existing file changed: %q", data)
	}
	if temps := tempFiles(t, dir); len(temps) != 0 {
		t.Fatalf("temp files left: %v", temps)
	}
}

func TestFileSaverReplacesExistingFileWithOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("OLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (FileSaver{}).Save(context.Background(), path, []byte("NEW\n"), true); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "NEW\n" {
		t.Fatalf("content = %q", data)
	}
	if temps := tempFiles(t, dir); len(temps) != 0 {
		t.Fatalf("temp files left: %v", temps)
	}
}

func TestFileSaverNeverFollowsASymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.yaml")
	link := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(target, []byte("TARGET\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	err := FileSaver{}.Save(context.Background(), link, []byte("NEW\n"), true)
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("error = %v, want ErrSymlink", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "TARGET\n" {
		t.Fatalf("symlink target changed: %q", data)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was replaced: %v %v", info, err)
	}
}

func TestFileSaverCanceledBeforePublishLeavesTheOldFileIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("OLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := FileSaver{}.Save(ctx, path, []byte("NEW\n"), true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "OLD\n" {
		t.Fatalf("existing file changed: %q", data)
	}
	if temps := tempFiles(t, dir); len(temps) != 0 {
		t.Fatalf("temp files left: %v", temps)
	}
}

func TestFileSaverCleansTempWhenPublishFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("OLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	saver := FileSaver{publish: func(tmp, dest string, overwrite bool) error {
		return errors.New("simulated publish failure")
	}}
	err := saver.Save(context.Background(), path, []byte("NEW\n"), true)
	if err == nil || !strings.Contains(err.Error(), "simulated publish failure") {
		t.Fatalf("error = %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "OLD\n" {
		t.Fatalf("existing file changed: %q", data)
	}
	if temps := tempFiles(t, dir); len(temps) != 0 {
		t.Fatalf("temp files left: %v", temps)
	}
}

func TestFileSaverRefusesDirectoriesAndSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	if err := (FileSaver{}).Save(context.Background(), dir, []byte("NEW\n"), true); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("directory error = %v, want ErrNotRegular", err)
	}
}

func TestFileSaverPreservesAStricterExistingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("OLD\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (FileSaver{}).Save(context.Background(), path, []byte("NEW\n"), true); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("mode = %o, want the existing 0600 mode preserved", info.Mode().Perm())
	}
}
