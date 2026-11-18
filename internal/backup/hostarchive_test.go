package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func fileHash(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func TestArchiveHostDirPreservesContentModesLinksAndDotfiles(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, ".hidden"), []byte("dot"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "sub", "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(source, "sub", "file.txt"), filepath.Join(source, "hard")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/file.txt", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(source, "abs-link")); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), DataFile)
	if err := ArchiveHostDir(context.Background(), source, dest); err != nil {
		t.Fatalf("ArchiveHostDir: %v", err)
	}
	got, err := InspectArchive(dest)
	if err != nil {
		t.Fatalf("InspectArchive: %v", err)
	}
	byPath := map[string]Entry{}
	for _, entry := range got.Entries {
		byPath[entry.Path] = entry
	}
	for _, want := range []string{".", "./.hidden", "./sub", "./sub/file.txt", "./run.sh", "./hard", "./link", "./abs-link"} {
		if _, ok := byPath[want]; !ok {
			t.Fatalf("archive missing %q: %+v", want, got.Entries)
		}
	}
	if entry := byPath["./.hidden"]; entry.Mode != "0600" || entry.SHA256 != fileHash(t, filepath.Join(source, ".hidden")) {
		t.Fatalf(".hidden entry = %+v", entry)
	}
	if entry := byPath["./run.sh"]; entry.Mode != "0755" {
		t.Fatalf("run.sh entry = %+v", entry)
	}
	if entry := byPath["./hard"]; entry.Type != EntryHardlink || entry.LinkTarget != "./sub/file.txt" {
		if other := byPath["./sub/file.txt"]; other.Type != EntryHardlink || other.LinkTarget != "./hard" {
			t.Fatalf("hardlink entries = %+v / %+v", entry, other)
		}
	}
	if entry := byPath["./link"]; entry.Type != EntrySymlink || entry.LinkTarget != "sub/file.txt" {
		t.Fatalf("symlink entry = %+v", entry)
	}
	if entry := byPath["./abs-link"]; entry.Type != EntrySymlink || entry.LinkTarget != "/etc/hosts" {
		t.Fatalf("absolute symlink entry = %+v", entry)
	}
	if entry := byPath["./sub/file.txt"]; entry.Type == EntryFile && (entry.UID != os.Getuid() || entry.GID != os.Getgid()) {
		t.Fatalf("file ownership = %+v, want %d:%d", entry, os.Getuid(), os.Getgid())
	}
	if got.Totals.Files != 3 || got.Totals.Symlinks != 2 || got.Totals.Hardlinks != 1 || got.Totals.Dirs != 2 {
		t.Fatalf("totals = %+v", got.Totals)
	}
}

func TestArchiveHostDirFailsClearlyOnAnUnreadableSubdirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-000 folder")
	}
	source := t.TempDir()
	locked := filepath.Join(source, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	err := ArchiveHostDir(context.Background(), source, filepath.Join(t.TempDir(), DataFile))
	if err == nil {
		t.Fatal("unreadable subdirectory was archived silently")
	}
	if !errors.Is(err, ErrBindInvalid) || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("error = %v", err)
	}
}

func TestArchiveHostDirRefusesAPreexistingDestinationSymlink(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), DataFile)
	if err := os.Symlink(outside, dest); err != nil {
		t.Fatal(err)
	}

	if err := ArchiveHostDir(context.Background(), source, dest); err == nil {
		t.Fatal("pre-existing destination symlink was followed")
	}
	raw, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "keep" {
		t.Fatalf("outside file changed: %q", raw)
	}
}

func TestArchiveHostDirStopsOnCancellation(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ArchiveHostDir(ctx, source, filepath.Join(t.TempDir(), DataFile)); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestArchiveHostDirRefusesNamesV1CannotStore(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, `bad\name`), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ArchiveHostDir(context.Background(), source, filepath.Join(t.TempDir(), DataFile))
	if !errors.Is(err, ErrBindInvalid) || !strings.Contains(err.Error(), "backup format v1") {
		t.Fatalf("error = %v", err)
	}
}

func TestArchiveHostDirRecordsSpecialFilesAsOther(t *testing.T) {
	source := t.TempDir()
	fifo := filepath.Join(source, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo is unavailable: %v", err)
	}
	dest := filepath.Join(t.TempDir(), DataFile)
	if err := ArchiveHostDir(context.Background(), source, dest); err != nil {
		t.Fatalf("ArchiveHostDir: %v", err)
	}
	got, err := InspectArchive(dest)
	if err != nil {
		t.Fatalf("InspectArchive: %v", err)
	}
	for _, entry := range got.Entries {
		if entry.Path == "./pipe" {
			if entry.Type != EntryOther {
				t.Fatalf("fifo entry = %+v", entry)
			}
			return
		}
	}
	t.Fatalf("fifo entry missing: %+v", got.Entries)
}
