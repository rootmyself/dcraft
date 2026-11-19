package s3

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/generators"
)

// These tests run the generated script against a PATH "aws" shim. No request
// ever reaches real S3 and no credentials are used: the shim copies local
// fixtures or serves a local .tar.gz, so the tests prove the script's own
// validation, cleanup and copy behavior.

type awsShim struct {
	dir  string
	log  string
	env  []string
	tmp  string
	proj string
}

func newAWSShim(t *testing.T) *awsShim {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "bin")
	tmp := filepath.Join(root, "tmp")
	proj := filepath.Join(root, "project")
	for _, d := range []string{dir, tmp, proj} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(root, "aws.log")
	script := `#!/bin/sh
set -eu
log="${AWS_SHIM_LOG:?}"
printf '%s\n' "$*" >> "$log"
case "$1 $2" in
  "s3 sync")
    mkdir -p "$4"
    cp -R "${AWS_SHIM_SYNC_FIXTURE:?}"/. "$4"/
    ;;
  "s3 cp")
    cp "${AWS_SHIM_CP_SOURCE:?}" "$4"
    ;;
  *)
    echo "aws shim: unexpected invocation: $*" >&2
    exit 9
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AWS_SHIM_LOG="+log,
		"TMPDIR="+tmp,
		"AWS_CONFIG_FILE=/dev/null",
		"AWS_SHARED_CREDENTIALS_FILE=/dev/null",
	)
	return &awsShim{dir: dir, log: log, env: env, tmp: tmp, proj: proj}
}

func (s *awsShim) script(t *testing.T, source string, mode Mode, dest, output string) string {
	t.Helper()
	layout, err := generators.ResolveLayout(s.proj, dest, output)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := Build(Input{Layout: layout, Source: source, Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generators.WriteScript(layout.ScriptPath, gen.Content); err != nil {
		t.Fatal(err)
	}
	return layout.ScriptPath
}

func (s *awsShim) run(t *testing.T, script string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", script)
	cmd.Dir = s.proj
	cmd.Env = s.env
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatalf("run script: %v", err)
		}
	}
	return string(out), code
}

func (s *awsShim) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(s.log)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func (s *awsShim) assertTempClean(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(s.tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("temporary folders left behind: %v", names)
	}
}

func TestSyncScriptCopiesTheFixtureAndKeepsStaleFiles(t *testing.T) {
	shim := newAWSShim(t)
	fixture := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fixture, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "app.txt"), []byte("synced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "sub", "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shim.env = append(shim.env, "AWS_SHIM_SYNC_FIXTURE="+fixture)
	dest := filepath.Join(shim.proj, "deploy")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "stale.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	script := shim.script(t, "s3://bucket/deploy", ModeSync, "deploy", "scripts/s3-sync.sh")
	out, code := shim.run(t, script)
	if code != 0 {
		t.Fatalf("sync script exit = %d\n%s", code, out)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "app.txt")); err != nil || string(data) != "synced\n" {
		t.Fatalf("app.txt = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "sub", "b.txt")); err != nil || string(data) != "b\n" {
		t.Fatalf("sub/b.txt = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "stale.txt")); err != nil || string(data) != "keep\n" {
		t.Fatalf("stale file was removed: %q, %v", data, err)
	}
	destReal, err := filepath.EvalSymlinks(dest)
	if err != nil {
		t.Fatal(err)
	}
	calls := shim.calls(t)
	if len(calls) != 1 || !strings.Contains(calls[0], "s3 sync s3://bucket/deploy "+destReal) {
		t.Fatalf("aws calls = %v", calls)
	}
	for _, call := range calls {
		if strings.Contains(call, "--delete") {
			t.Fatalf("sync used --delete: %s", call)
		}
	}
	if !strings.Contains(out, "삭제 동기화는 사용하지 않습니다") {
		t.Fatalf("sync output does not state the non-delete behavior:\n%s", out)
	}
}

func TestSyncScriptRefusesAnOutsideDestinationEvenIfTheScriptWasEdited(t *testing.T) {
	shim := newAWSShim(t)
	fixture := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixture, "app.txt"), []byte("synced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shim.env = append(shim.env, "AWS_SHIM_SYNC_FIXTURE="+fixture)
	layout, err := generators.ResolveLayout(shim.proj, "deploy", "scripts/s3-sync.sh")
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	layout.Destination = outside
	layout.DestinationRel = outside
	gen, err := Build(Input{Layout: layout, Source: "s3://bucket/deploy", Mode: ModeSync})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generators.WriteScript(layout.ScriptPath, gen.Content); err != nil {
		t.Fatal(err)
	}
	out, code := shim.run(t, layout.ScriptPath)
	if code == 0 {
		t.Fatalf("outside destination accepted:\n%s", out)
	}
	if !strings.Contains(out, "프로젝트 폴더 밖") {
		t.Fatalf("outside guidance missing:\n%s", out)
	}
	if calls := shim.calls(t); len(calls) != 0 {
		t.Fatalf("aws was invoked for an outside destination: %v", calls)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("outside destination was written: %v", entries)
	}
}

func TestSyncScriptRefusesADestinationSymlink(t *testing.T) {
	shim := newAWSShim(t)
	fixture := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixture, "app.txt"), []byte("synced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shim.env = append(shim.env, "AWS_SHIM_SYNC_FIXTURE="+fixture)
	script := shim.script(t, "s3://bucket/deploy", ModeSync, "deploy", "scripts/s3-sync.sh")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(shim.proj, "deploy")); err != nil {
		t.Fatal(err)
	}
	out, code := shim.run(t, script)
	if code == 0 {
		t.Fatalf("destination symlink accepted:\n%s", out)
	}
	if calls := shim.calls(t); len(calls) != 0 {
		t.Fatalf("aws was invoked through a symlink: %v", calls)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("symlinked destination was written: %v", entries)
	}
}

func TestTarScriptDownloadsVerifiesExtractsAndKeepsStaleFiles(t *testing.T) {
	shim := newAWSShim(t)
	archive := filepath.Join(t.TempDir(), "artifact.tar.gz")
	writeTarGz(t, archive, []tarEntry{
		{name: "app.txt", body: "from tar\n"},
		{name: "sub/", dir: true},
		{name: "sub/b.txt", body: "b\n"},
		{name: "link", linkname: "app.txt", symlink: true},
	})
	shim.env = append(shim.env, "AWS_SHIM_CP_SOURCE="+archive)
	dest := filepath.Join(shim.proj, "deploy")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "stale.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	script := shim.script(t, "s3://bucket/path/app.tar.gz", ModeTar, "deploy", "scripts/s3-deploy.sh")
	out, code := shim.run(t, script)
	if code != 0 {
		t.Fatalf("tar script exit = %d\n%s", code, out)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "app.txt")); err != nil || string(data) != "from tar\n" {
		t.Fatalf("app.txt = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "sub", "b.txt")); err != nil || string(data) != "b\n" {
		t.Fatalf("sub/b.txt = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "stale.txt")); err != nil || string(data) != "keep\n" {
		t.Fatalf("stale file was removed: %q, %v", data, err)
	}
	info, err := os.Lstat(filepath.Join(dest, "link"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("internal symlink was not copied as a link: %v, %v", info, err)
	}
	if target, err := os.Readlink(filepath.Join(dest, "link")); err != nil || target != "app.txt" {
		t.Fatalf("internal symlink target = %q, %v", target, err)
	}
	if !strings.Contains(out, "임시 파일과 임시 폴더는 이 스크립트 종료 시 정리됩니다") {
		t.Fatalf("tar output does not state cleanup:\n%s", out)
	}
	shim.assertTempClean(t)
	calls := shim.calls(t)
	if len(calls) != 1 || !strings.Contains(calls[0], "s3 cp s3://bucket/path/app.tar.gz") {
		t.Fatalf("aws calls = %v", calls)
	}
}

func TestTarScriptRejectsATruncatedArchiveBeforeTouchingTheDestination(t *testing.T) {
	shim := newAWSShim(t)
	full := filepath.Join(t.TempDir(), "full.tar.gz")
	writeTarGz(t, full, []tarEntry{{name: "app.txt", body: "data\n"}})
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	truncated := filepath.Join(t.TempDir(), "truncated.tar.gz")
	if err := os.WriteFile(truncated, data[:len(data)-8], 0o644); err != nil {
		t.Fatal(err)
	}
	shim.env = append(shim.env, "AWS_SHIM_CP_SOURCE="+truncated)
	dest := filepath.Join(shim.proj, "deploy")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "sentinel.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := shim.script(t, "s3://bucket/path/app.tar.gz", ModeTar, "deploy", "scripts/s3-deploy.sh")
	out, code := shim.run(t, script)
	if code == 0 {
		t.Fatalf("truncated archive accepted:\n%s", out)
	}
	if !strings.Contains(out, "gzip 무결성 검사에 실패했습니다") {
		t.Fatalf("truncation guidance missing:\n%s", out)
	}
	assertOnlyFile(t, dest, "sentinel.txt")
	shim.assertTempClean(t)
}

func TestTarScriptRejectsHostileArchivesWithoutWritingOutside(t *testing.T) {
	cases := []struct {
		name    string
		entries []tarEntry
		want    string
	}{
		{
			name:    "relative traversal",
			entries: []tarEntry{{name: "../escape.txt", body: "escape\n"}},
			want:    "..",
		},
		{
			name:    "absolute path",
			entries: []tarEntry{{name: "/abs-escape.txt", body: "escape\n"}},
			want:    "절대 경로",
		},
		{
			name:    "escaping symlink",
			entries: []tarEntry{{name: "link", linkname: "../../outside.txt", symlink: true}},
			want:    "심볼릭 링크",
		},
		{
			name:    "nested escaping symlink",
			entries: []tarEntry{{name: "dir/", dir: true}, {name: "dir/link", linkname: "../../outside.txt", symlink: true}},
			want:    "심볼릭 링크",
		},
		{
			name:    "absolute symlink",
			entries: []tarEntry{{name: "link", linkname: "/tmp/dcraft-outside", symlink: true}},
			want:    "심볼릭 링크",
		},
		{
			name:    "escaping hardlink",
			entries: []tarEntry{{name: "link", linkname: "../outside.txt", hardlink: true}},
			want:    "하드 링크",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shim := newAWSShim(t)
			archive := filepath.Join(t.TempDir(), "hostile.tar.gz")
			writeTarGz(t, archive, tc.entries)
			shim.env = append(shim.env, "AWS_SHIM_CP_SOURCE="+archive)
			dest := filepath.Join(shim.proj, "deploy")
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dest, "sentinel.txt"), []byte("keep\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "outside.txt")
			if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			script := shim.script(t, "s3://bucket/path/app.tar.gz", ModeTar, "deploy", "scripts/s3-deploy.sh")
			out, code := shim.run(t, script)
			if code == 0 {
				t.Fatalf("hostile archive accepted:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("hostile guidance %q missing:\n%s", tc.want, out)
			}
			assertOnlyFile(t, dest, "sentinel.txt")
			if data, _ := os.ReadFile(outside); string(data) != "outside\n" {
				t.Fatalf("outside file changed: %q", data)
			}
			if _, err := os.Stat(filepath.Join(shim.proj, "escape.txt")); !os.IsNotExist(err) {
				t.Fatalf("archive wrote into the project root: %v", err)
			}
			shim.assertTempClean(t)
		})
	}
}

func TestTarScriptRefusesADestinationContainingASymlink(t *testing.T) {
	shim := newAWSShim(t)
	archive := filepath.Join(t.TempDir(), "artifact.tar.gz")
	writeTarGz(t, archive, []tarEntry{{name: "app.txt", body: "data\n"}})
	shim.env = append(shim.env, "AWS_SHIM_CP_SOURCE="+archive)
	outside := t.TempDir()
	dest := filepath.Join(shim.proj, "deploy")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dest, "escape")); err != nil {
		t.Fatal(err)
	}
	script := shim.script(t, "s3://bucket/path/app.tar.gz", ModeTar, "deploy", "scripts/s3-deploy.sh")
	out, code := shim.run(t, script)
	if code == 0 {
		t.Fatalf("destination symlink accepted:\n%s", out)
	}
	if !strings.Contains(out, "심볼릭 링크") {
		t.Fatalf("symlink guidance missing:\n%s", out)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("copy escaped through the destination symlink: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(dest, "app.txt")); !os.IsNotExist(err) {
		t.Fatalf("archive was copied into a symlinked destination: %v", err)
	}
	shim.assertTempClean(t)
}

func TestMissingAWSCliFailsBeforeAnyDestinationWrite(t *testing.T) {
	shim := newAWSShim(t)
	var env []string
	for _, kv := range shim.env {
		if strings.HasPrefix(kv, "PATH=") {
			continue
		}
		env = append(env, kv)
	}
	// The standard system tools stay available; the shim folder (and any
	// other aws installation) is removed from PATH.
	shim.env = append(env, "PATH=/bin:/usr/bin")
	script := shim.script(t, "s3://bucket/deploy", ModeSync, "deploy", "scripts/s3-sync.sh")
	out, code := shim.run(t, script)
	if code == 0 {
		t.Fatalf("script succeeded without aws:\n%s", out)
	}
	if !strings.Contains(out, "aws CLI를 찾지 못했습니다") {
		t.Fatalf("missing aws guidance missing:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(shim.proj, "deploy")); !os.IsNotExist(err) {
		t.Fatalf("script created a destination without aws: %v", err)
	}
}

type tarEntry struct {
	name     string
	body     string
	linkname string
	symlink  bool
	hardlink bool
	dir      bool
}

func writeTarGz(t *testing.T, path string, entries []tarEntry) {
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

func TestTarPreparationFailureLeavesANewDestinationAbsent(t *testing.T) {
	shim := newAWSShim(t)
	full := filepath.Join(t.TempDir(), "full.tar.gz")
	writeTarGz(t, full, []tarEntry{{name: "app.txt", body: "data\n"}})
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	truncated := filepath.Join(t.TempDir(), "truncated.tar.gz")
	if err := os.WriteFile(truncated, data[:len(data)-8], 0o644); err != nil {
		t.Fatal(err)
	}
	shim.env = append(shim.env, "AWS_SHIM_CP_SOURCE="+truncated)
	script := shim.script(t, "s3://bucket/path/app.tar.gz", ModeTar, "deploy", "scripts/s3-deploy.sh")
	out, code := shim.run(t, script)
	if code == 0 {
		t.Fatalf("truncated archive accepted:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(shim.proj, "deploy")); !os.IsNotExist(err) {
		t.Fatalf("failed preparation created the destination: %v", err)
	}
	shim.assertTempClean(t)
}
