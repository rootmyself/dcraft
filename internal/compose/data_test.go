package compose

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
)

const dataConfigJSON = `{
  "name": "proj",
  "services": {
    "app": {"volumes": [
      {"type": "volume", "source": "data", "target": "/data", "volume": {}},
      {"type": "volume", "source": "custom", "target": "/custom", "volume": {}},
      {"type": "volume", "source": "ext", "target": "/ext", "volume": {}},
      {"type": "volume", "target": "/anon", "read_only": true, "volume": {}},
      {"type": "bind", "source": "/host/bind", "target": "/bind", "bind": {}}
    ]},
    "worker": {"volumes": [
      {"type": "volume", "source": "data", "target": "/work", "read_only": true, "volume": {}}
    ]}
  },
  "volumes": {
    "custom": {"name": "custom-engine", "external": false},
    "data": {"name": "proj_data", "external": false},
    "ext": {"name": "ext-engine", "external": true}
  }
}`

func dataTarget(dir string) core.Target {
	return core.Target{ProjectDir: dir, Files: []string{dir + "/compose.yaml"}}
}

func dataRunner(t *testing.T, config string) *execxtest.Runner {
	t.Helper()
	return &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		line := cmd.Name + " " + strings.Join(cmd.Args, " ")
		switch {
		case strings.Contains(line, "config --format json"):
			return execx.Result{Stdout: []byte(config)}, nil
		default:
			return execx.Result{Stderr: []byte("unexpected: " + line), ExitCode: 1}, nil
		}
	}}
}

func TestSourcesResolveVolumeKeysToEngineNames(t *testing.T) {
	runner := dataRunner(t, dataConfigJSON)
	c := New(runner, nil)
	sources, err := c.Sources(context.Background(), dataTarget("/project"))
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	byKey := map[string]data.DataSource{}
	var anonymous int
	var bind *data.DataSource
	for _, src := range sources {
		switch {
		case src.Anonymous:
			if !src.ReadOnly {
				t.Fatalf("the read-only anonymous mount was not noticed: %+v", src)
			}
			anonymous++
		case src.Kind == data.KindBind:
			s := src
			bind = &s
		default:
			byKey[src.Key] = src
		}
	}
	dataSrc := byKey["data"]
	if dataSrc.Name != "proj_data" || dataSrc.Kind != data.KindVolume {
		t.Fatalf("data source = %+v", dataSrc)
	}
	if strings.Join(dataSrc.Services, ",") != "app,worker" {
		t.Fatalf("data services = %v", dataSrc.Services)
	}
	if strings.Join(dataSrc.MountTargets, ",") != "/data,/work" {
		t.Fatalf("data mount targets = %v", dataSrc.MountTargets)
	}
	if dataSrc.ReadOnly {
		t.Fatalf("a volume with a writable mount must not be reported read-only: %+v", dataSrc)
	}
	if got := byKey["custom"]; got.Name != "custom-engine" || got.External {
		t.Fatalf("custom source = %+v", got)
	}
	if got := byKey["ext"]; got.Name != "ext-engine" || !got.External {
		t.Fatalf("external source = %+v", got)
	}
	if anonymous != 1 {
		t.Fatalf("anonymous sources = %d, want 1", anonymous)
	}
	if bind == nil || bind.Name != "/host/bind" || bind.Kind != data.KindBind {
		t.Fatalf("bind source = %+v", bind)
	}

	calls := strings.Join(runner.CallLines(), "\n")
	if !strings.Contains(calls, "compose -f /project/compose.yaml config --format json") {
		t.Fatalf("config call = %s", calls)
	}
}

func TestServiceRuntimeParsesNDJSON(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stdout: []byte(strings.Join([]string{
			`{"Service":"app","State":"running","ID":"aaa","ExitCode":0}`,
			`{"Service":"worker","State":"exited","ID":"bbb","ExitCode":137}`,
		}, "\n"))}, nil
	}}
	c := New(runner, nil)
	running, err := c.ServiceRuntime(context.Background(), dataTarget("/project"))
	if err != nil {
		t.Fatalf("ServiceRuntime: %v", err)
	}
	if len(running) != 2 || running[0].Service != "app" || running[0].State != "running" {
		t.Fatalf("runtime = %+v", running)
	}
	if running[1].ExitCode != 137 {
		t.Fatalf("exit code = %d", running[1].ExitCode)
	}
	if line := runner.CallLines()[0]; !strings.Contains(line, "compose -f /project/compose.yaml ps --all --format json") {
		t.Fatalf("call = %q", line)
	}
}

func TestVolumeConsumersQueryTheActualNameAndIncludeStoppedContainers(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stdout: []byte("other-app-1\tother\tapp\trunning\nproj-app-1\tproj\tapp\texited\n")}, nil
	}}
	c := New(runner, nil)
	consumers, err := c.VolumeConsumers(context.Background(), dataTarget("/project"), "proj_data")
	if err != nil {
		t.Fatalf("VolumeConsumers: %v", err)
	}
	if len(consumers) != 2 || consumers[0].Project != "other" || consumers[1].State != "exited" {
		t.Fatalf("consumers = %+v", consumers)
	}
	line := runner.CallLines()[0]
	if !strings.HasPrefix(line, "docker ps -a --filter volume=proj_data --format ") {
		t.Fatalf("call = %q", line)
	}
	if strings.Contains(line, "data ") && !strings.Contains(line, "volume=proj_data") {
		t.Fatalf("query used the compose key instead of the engine name: %q", line)
	}
}

func TestStopAndStartUseComposeWithOnlyTheNamedServices(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{}, nil
	}}
	c := New(runner, nil)
	target := dataTarget("/project")
	if _, err := c.StopServices(context.Background(), target, []string{"app", "worker"}); err != nil {
		t.Fatalf("StopServices: %v", err)
	}
	if _, err := c.StartServices(context.Background(), target, []string{"app"}); err != nil {
		t.Fatalf("StartServices: %v", err)
	}
	lines := runner.CallLines()
	if strings.Join(lines, "\n") != strings.Join([]string{
		"docker compose -f /project/compose.yaml stop app worker",
		"docker compose -f /project/compose.yaml start app",
	}, "\n") {
		t.Fatalf("calls = %v", lines)
	}
}

func TestArchiveVolumeUsesARootHelperOnAReadOnlySource(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{}, nil
	}}
	c := New(runner, nil)
	if _, err := c.ArchiveVolume(context.Background(), "proj_data", "/backups/work"); err != nil {
		t.Fatalf("ArchiveVolume: %v", err)
	}
	line := runner.CallLines()[0]
	for _, want := range []string{
		"docker run --rm --name dcraft-backup-",
		"-v proj_data:/data:ro",
		"-v /backups/work:/backup",
		"--entrypoint tar alpine:3.20 -C /data -czf /backup/data.tar.gz .",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("archive call missing %q: %s", want, line)
		}
	}
	if strings.Contains(line, "/data:rw") {
		t.Fatalf("source volume is not read-only: %s", line)
	}
}

func TestArchiveVolumeReportsTheHelperFailureOutput(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stderr: []byte("tar: can't open './secret': Permission denied"), ExitCode: 1}, nil
	}}
	c := New(runner, nil)
	out, err := c.ArchiveVolume(context.Background(), "proj_data", "/backups/work")
	if err == nil {
		t.Fatal("archive failure was swallowed")
	}
	if !strings.Contains(out.Output, "Permission denied") {
		t.Fatalf("output = %q", out.Output)
	}
}

func TestArchiveVolumeKillsTheHelperWhenInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{}, ctx.Err()
	}}
	c := New(runner, nil)
	if _, err := c.ArchiveVolume(ctx, "proj_data", "/backups/work"); err == nil {
		t.Fatal("canceled archive did not fail")
	}
	var killed bool
	for _, line := range runner.CallLines() {
		if strings.HasPrefix(line, "docker kill dcraft-backup-") {
			killed = true
		}
	}
	if !killed {
		t.Fatalf("helper was not killed after interruption: %v", runner.CallLines())
	}
}

func TestExternalVolumeFlagAcceptsBooleanAndObjectForms(t *testing.T) {
	for _, raw := range []string{`true`, `{"name":"shared"}`} {
		var doc dataConfigDoc
		payload := `{"services":{},"volumes":{"ext":{"name":"ext","external":` + raw + `}}}`
		if err := json.Unmarshal([]byte(payload), &doc); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		if !doc.Volumes["ext"].IsExternal() {
			t.Fatalf("external %s was not detected", raw)
		}
	}
}
