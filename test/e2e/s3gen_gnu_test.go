package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The generated tar script is also executed inside a Debian container so the
// Linux GNU tar path is measured, not inferred from macOS bsdtar. The aws shim
// serves a local archive; no request reaches S3 and no credentials are used.
func TestIntegrationS3TarScriptRunsUnderLinuxGNU(t *testing.T) {
	requireIntegration(t)
	cases := []struct {
		name    string
		entries []linuxTarEntry
		wantOK  bool
	}{
		{
			name: "valid archive with an internal symlink",
			entries: []linuxTarEntry{
				{name: "app.txt", body: "linux-gnu\n"},
				{name: "sub/", dir: true},
				{name: "sub/b.txt", body: "b\n"},
				{name: "link", linkname: "app.txt", symlink: true},
			},
			wantOK: true,
		},
		{
			name:    "escaping symlink",
			entries: []linuxTarEntry{{name: "link", linkname: "../../outside/victim.txt", symlink: true}},
			wantOK:  false,
		},
		{
			name: "escaping hardlink",
			entries: []linuxTarEntry{
				{name: "victim.txt", body: "inside\n"},
				{name: "link", linkname: "../outside/victim.txt", hardlink: true},
			},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runS3TarLinuxCase(t, tc.entries, tc.wantOK)
		})
	}
}

func runS3TarLinuxCase(t *testing.T, entries []linuxTarEntry, wantOK bool) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	bin := filepath.Join(root, "bin")
	tmp := filepath.Join(root, "tmp")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{project, bin, tmp, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "artifact.tar.gz")
	writeLinuxTarGz(t, archive, entries)
	shim := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "${AWS_SHIM_LOG:?}"
case "$1 $2" in
  "s3 cp") cp "${AWS_SHIM_CP_SOURCE:?}" "$4" ;;
  *) echo "aws shim: unexpected invocation: $*" >&2; exit 9 ;;
esac
`
	log := filepath.Join(root, "aws.log")
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}

	generateEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": t.TempDir()})
	generated := runProcess(t, project, generateEnv, strings.NewReader(""),
		"generate-s3-hook", "--source", "s3://bucket/path/app.tar.gz", "--mode", "tar",
		"--dest", "deploy", "--output", "scripts/s3-deploy.sh",
		"--approve", "file_overwrite", "--json")
	if generated.code != 0 {
		t.Fatalf("generate exit = %d stderr=%q stdout=%s", generated.code, generated.stderr, generated.stdout)
	}
	dest := filepath.Join(project, "deploy")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "stale.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", root+":"+root,
		"-w", project,
		"-e", "AWS_SHIM_LOG="+log,
		"-e", "AWS_SHIM_CP_SOURCE="+archive,
		"-e", "TMPDIR="+tmp,
		"-e", "PATH="+bin+":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"debian:bookworm-slim",
		"/usr/bin/bash", filepath.Join(project, "scripts", "s3-deploy.sh"),
	)
	cmd.Env = buildEnv(nil)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("docker run: %v\n%s", err, out)
		}
		code = exitErr.ExitCode()
	}
	if wantOK && code != 0 {
		t.Fatalf("linux tar script exit = %d\n%s", code, out)
	}
	if !wantOK && code == 0 {
		t.Fatalf("hostile archive accepted under Linux:\n%s", out)
	}
	if wantOK {
		if data, err := os.ReadFile(filepath.Join(dest, "app.txt")); err != nil || string(data) != "linux-gnu\n" {
			t.Fatalf("app.txt = %q, %v", data, err)
		}
		if data, err := os.ReadFile(filepath.Join(dest, "stale.txt")); err != nil || string(data) != "keep\n" {
			t.Fatalf("stale file was removed: %q, %v", data, err)
		}
		if info, err := os.Lstat(filepath.Join(dest, "link")); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("internal symlink was not copied: %v, %v", info, err)
		}
	} else {
		assertOnlyFile(t, dest, "stale.txt")
		if data, _ := os.ReadFile(victim); string(data) != "outside\n" {
			t.Fatalf("outside file changed: %q", data)
		}
	}
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Fatalf("temporary folders left behind: %v, %v", entries, err)
	}
	if data, err := os.ReadFile(log); err != nil || !strings.Contains(string(data), "s3 cp s3://bucket/path/app.tar.gz") {
		t.Fatalf("aws shim call missing: %q, %v", data, err)
	}
}

type linuxTarEntry struct {
	name     string
	body     string
	linkname string
	symlink  bool
	hardlink bool
	dir      bool
}

func writeLinuxTarGz(t *testing.T, path string, entries []linuxTarEntry) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o644, Format: tar.FormatPAX}
		switch {
		case entry.dir:
			header.Typeflag = tar.TypeDir
			header.Mode = 0o755
		case entry.symlink:
			header.Typeflag = tar.TypeSymlink
			header.Linkname = entry.linkname
			header.Mode = 0o777
		case entry.hardlink:
			header.Typeflag = tar.TypeLink
			header.Linkname = entry.linkname
		default:
			header.Typeflag = tar.TypeReg
			header.Size = int64(len(entry.body))
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg && len(entry.body) > 0 {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("destination = %v, want only %q", names, name)
	}
}
