package fsidentity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyUsesFilesystemIdentityForAnExistingFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "app")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "app-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if Key(dir) != Key(link) {
		t.Fatalf("symlink spelling has a separate identity: %q vs %q", Key(dir), Key(link))
	}
	if Key(dir) != Key(dir+string(filepath.Separator)+".") {
		t.Fatalf("dot spelling has a separate identity")
	}
}

func TestKeySeparatesGenuinelyDifferentFolders(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	for _, dir := range []string{a, b} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if Key(a) == Key(b) {
		t.Fatalf("different folders share identity %q", Key(a))
	}
}

func TestKeyFallsBackToCanonicalPathForAMissingFolder(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if !strings.HasPrefix(Key(missing), "path:") {
		t.Fatalf("missing folder key = %q, want documented path fallback", Key(missing))
	}
	if Key(missing) != Key(filepath.Join(missing, "..", "missing")) {
		t.Fatal("path fallback is not canonical")
	}
}

func TestSameUsesFilesystemIdentity(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "app")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "app-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if !Same(dir, link) {
		t.Fatal("symlink and target are not the same folder")
	}
	if Same(dir, filepath.Join(root, "other")) {
		t.Fatal("a missing folder compared equal")
	}
}

func TestCaseVariantSpellingsShareIdentityWhenTheFilesystemDoes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "CaseProject")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	variant := filepath.Join(root, "caseproject")
	info, err := os.Stat(variant)
	if err != nil {
		t.Skip("filesystem is case-sensitive; case variants are different folders")
	}
	base, _ := os.Stat(dir)
	if !os.SameFile(base, info) {
		t.Skip("case variants are not the same folder on this filesystem")
	}
	if Key(dir) != Key(variant) {
		t.Fatalf("case variants have separate identities: %q vs %q", Key(dir), Key(variant))
	}
}

func TestUnicodeVariantSpellingsShareIdentityWhenTheFilesystemDoes(t *testing.T) {
	root := t.TempDir()
	nfc := filepath.Join(root, "한글")
	if err := os.Mkdir(nfc, 0o700); err != nil {
		t.Skipf("Unicode folder name unavailable: %v", err)
	}
	nfd := filepath.Join(root, "한글")
	info, err := os.Stat(nfd)
	if err != nil {
		t.Skip("filesystem distinguishes NFC/NFD names")
	}
	base, _ := os.Stat(nfc)
	if !os.SameFile(base, info) {
		t.Skip("NFC/NFD spellings are not the same folder on this filesystem")
	}
	if Key(nfc) != Key(nfd) {
		t.Fatalf("NFC/NFD spellings have separate identities: %q vs %q", Key(nfc), Key(nfd))
	}
}
