package compose

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
)

func restoreHelperRunner(t *testing.T, startOutput string) *execxtest.Runner {
	t.Helper()
	return &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		line := cmd.Name + " " + strings.Join(cmd.Args, " ")
		switch {
		case strings.HasPrefix(line, "docker create "):
			return execx.Result{Stdout: []byte("cid")}, nil
		case strings.HasPrefix(line, "docker start -a "):
			return execx.Result{Stdout: []byte(startOutput)}, nil
		case strings.HasPrefix(line, "docker rm -f "):
			return execx.Result{}, nil
		case strings.HasPrefix(line, "docker inspect --format"):
			return execx.Result{Stderr: []byte("no such object"), ExitCode: 1}, nil
		default:
			return execx.Result{Stderr: []byte("unexpected: " + line), ExitCode: 9}, nil
		}
	}}
}

func TestEmptyVolumeUsesFindDeleteAndCleansTheHelper(t *testing.T) {
	runner := restoreHelperRunner(t, "")
	c := New(runner, nil)
	res, err := c.EmptyVolume(context.Background(), "proj_data")
	if err != nil {
		t.Fatalf("EmptyVolume: %v\n%s", err, res.Output)
	}
	calls := strings.Join(runner.CallLines(), "\n")
	for _, want := range []string{
		"create --name dcraft-restore-",
		"-v proj_data:/data",
		"find /data -mindepth 1 -delete",
		"start -a dcraft-restore-",
		"rm -f dcraft-restore-",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("missing %q:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "/data/*") {
		t.Fatalf("unsafe empty command was used:\n%s", calls)
	}
}

func TestExtractVolumeMountsTheArtifactReadOnlyAndExtractsAsRoot(t *testing.T) {
	runner := restoreHelperRunner(t, "")
	c := New(runner, nil)
	if _, err := c.ExtractVolume(context.Background(), "proj_data", "/backups/artifact"); err != nil {
		t.Fatalf("ExtractVolume: %v", err)
	}
	calls := strings.Join(runner.CallLines(), "\n")
	for _, want := range []string{
		"-v /backups/artifact:/backup:ro",
		"-v proj_data:/data ",
		"--entrypoint tar",
		"-C /data -xpzf /backup/data.tar.gz",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("missing %q:\n%s", want, calls)
		}
	}
}

func TestVerifyVolumeMountsTheScriptReadOnly(t *testing.T) {
	runner := restoreHelperRunner(t, "")
	c := New(runner, nil)
	if _, err := c.VerifyVolume(context.Background(), "proj_data", "/var/tmp/check/check.sh"); err != nil {
		t.Fatalf("VerifyVolume: %v", err)
	}
	calls := strings.Join(runner.CallLines(), "\n")
	for _, want := range []string{
		"-v proj_data:/data:ro",
		"-v /var/tmp/check:/verify:ro",
		"--entrypoint sh",
		"/verify/check.sh",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("missing %q:\n%s", want, calls)
		}
	}
}

func TestVolumeUsageParsesBlocksAndEntries(t *testing.T) {
	runner := restoreHelperRunner(t, "12 7")
	c := New(runner, nil)
	usage, err := c.VolumeUsage(context.Background(), "proj_data")
	if err != nil {
		t.Fatalf("VolumeUsage: %v", err)
	}
	if usage.Bytes != 12*1024 || usage.Entries != 7 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestVolumeExistsDistinguishesMissingFromPermissionFailure(t *testing.T) {
	missing := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stderr: []byte("Error: No such volume: gone"), ExitCode: 1}, nil
	}}
	exists, err := New(missing, nil).VolumeExists(context.Background(), "gone")
	if err != nil || exists {
		t.Fatalf("missing volume = %v, %v", exists, err)
	}

	denied := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stderr: []byte("permission denied"), ExitCode: 1}, nil
	}}
	_, err = New(denied, nil).VolumeExists(context.Background(), "proj_data")
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategoryRestoreFailed {
		t.Fatalf("permission failure = %v", err)
	}
}
