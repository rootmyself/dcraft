package artifacts_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/artifacts"
)

func TestInstallAtomicWritesExecutableFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "download")
	if err := os.WriteFile(src, []byte("compose-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "plugins", "docker-compose")
	installer := artifacts.OSInstaller{}
	if err := installer.InstallAtomic(src, dest, 0o755); err != nil {
		t.Fatalf("InstallAtomic: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "compose-binary" {
		t.Fatalf("content = %q", data)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestInstallAtomicNeverOverwritesAnExistingInstallation(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "download")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "docker-compose")
	if err := os.WriteFile(dest, []byte("existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := (artifacts.OSInstaller{}).InstallAtomic(src, dest, 0o755)
	if !errors.Is(err, artifacts.ErrExists) {
		t.Fatalf("error = %v, want ErrExists", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing" {
		t.Fatalf("existing installation was modified: %q", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".docker-compose-") {
			t.Fatalf("temporary install file left behind: %s", e.Name())
		}
	}
}

func TestInstallAtomicNeverOverwritesASymlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "download")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "real-compose")
	if err := os.WriteFile(target, []byte("real"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "docker-compose")
	if err := os.Symlink(target, dest); err != nil {
		t.Fatal(err)
	}
	err := (artifacts.OSInstaller{}).InstallAtomic(src, dest, 0o755)
	if !errors.Is(err, artifacts.ErrExists) {
		t.Fatalf("error = %v, want ErrExists", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "real" {
		t.Fatalf("symlink target modified: %q", data)
	}
}

func TestInstallAtomicLeavesNothingOnSourceFailure(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "plugins", "docker-compose")
	err := (artifacts.OSInstaller{}).InstallAtomic(filepath.Join(dir, "missing"), dest, 0o755)
	if err == nil {
		t.Fatal("expected an error")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("destination was created: %v", statErr)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "plugins"))
	if len(entries) != 0 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestExistsSeesFilesAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	installer := artifacts.OSInstaller{}
	exists, err := installer.Exists(filepath.Join(dir, "docker-compose"))
	if err != nil || exists {
		t.Fatalf("missing path: exists=%v err=%v", exists, err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	exists, err = installer.Exists(file)
	if err != nil || !exists {
		t.Fatalf("file: exists=%v err=%v", exists, err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	exists, err = installer.Exists(link)
	if err != nil || !exists {
		t.Fatalf("symlink: exists=%v err=%v", exists, err)
	}
}

func TestHTTPDownloaderGetsMetadataAndDownloadsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Errorf("missing User-Agent")
		}
		switch r.URL.Path {
		case "/meta":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/asset":
			_, _ = w.Write([]byte("binary"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	downloader := artifacts.HTTPDownloader{}
	body, err := downloader.Get(context.Background(), server.URL+"/meta")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q", body)
	}
	dest := filepath.Join(t.TempDir(), "asset")
	if err := downloader.Download(context.Background(), server.URL+"/asset", dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "binary" {
		t.Fatalf("downloaded = %q", data)
	}
}

func TestHTTPDownloaderReportsHTTPAndTransportFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	downloader := artifacts.HTTPDownloader{}
	if err := downloader.Download(context.Background(), server.URL+"/missing", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("404 did not fail")
	}
	if _, err := downloader.Get(context.Background(), server.URL+"/missing"); err == nil {
		t.Fatal("404 metadata did not fail")
	}
}

func TestHTTPDownloaderRemovesPartialFileOnTruncatedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "asset")
	err := (artifacts.HTTPDownloader{}).Download(context.Background(), server.URL+"/asset", dest)
	if err == nil {
		t.Fatal("truncated download did not fail")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("partial file left behind: %v", statErr)
	}
}
