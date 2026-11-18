package compose

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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

type helperLifecycle struct {
	mu    sync.Mutex
	calls []string
	// inspectResults is consumed by successive docker inspect calls.
	inspectResults []execx.Result
	rmResults      []execx.Result
	startErr       error
	startResult    execx.Result
	createResult   execx.Result
	createErr      error
	cancelOnStart  context.CancelFunc
	alwaysExists   bool
}

func (h *helperLifecycle) handler(cmd execx.Command) (execx.Result, error) {
	line := cmd.Name + " " + strings.Join(cmd.Args, " ")
	h.mu.Lock()
	h.calls = append(h.calls, line)
	h.mu.Unlock()
	switch {
	case strings.HasPrefix(line, "docker create "):
		return h.createResult, h.createErr
	case strings.HasPrefix(line, "docker start -a "):
		if h.cancelOnStart != nil {
			h.cancelOnStart()
		}
		return h.startResult, h.startErr
	case strings.HasPrefix(line, "docker rm -f "):
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.rmResults) == 0 {
			return execx.Result{}, nil
		}
		res := h.rmResults[0]
		h.rmResults = h.rmResults[1:]
		return res, nil
	case strings.HasPrefix(line, "docker inspect "):
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.alwaysExists {
			return execx.Result{Stdout: []byte("cid")}, nil
		}
		if len(h.inspectResults) == 0 {
			return execx.Result{Stderr: []byte("No such object"), ExitCode: 1}, nil
		}
		res := h.inspectResults[0]
		h.inspectResults = h.inspectResults[1:]
		return res, nil
	default:
		return execx.Result{Stderr: []byte("unexpected: " + line), ExitCode: 1}, nil
	}
}

func (h *helperLifecycle) lines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.calls...)
}

func newHelperCompose(t *testing.T, h *helperLifecycle) *Compose {
	t.Helper()
	c := New(&execxtest.Runner{Handler: h.handler}, nil)
	c.cleanupTimeout = 2 * time.Second
	c.cleanupPoll = 5 * time.Millisecond
	c.cleanupGrace = 20 * time.Millisecond
	return c
}

func TestArchiveVolumeCreatesStartsAndRemovesTheHelper(t *testing.T) {
	h := &helperLifecycle{}
	c := newHelperCompose(t, h)
	if _, err := c.ArchiveVolume(context.Background(), "proj_data", "/backups/work"); err != nil {
		t.Fatalf("ArchiveVolume: %v", err)
	}
	lines := h.lines()
	if len(lines) < 3 {
		t.Fatalf("calls = %v", lines)
	}
	create := lines[0]
	for _, want := range []string{
		"docker create --name dcraft-backup-",
		"--label dcraft.backup=1",
		"-v proj_data:/data:ro",
		"-v /backups/work:/backup",
		"--entrypoint tar alpine:3.20 -C /data -czf /backup/data.tar.gz .",
	} {
		if !strings.Contains(create, want) {
			t.Fatalf("create call missing %q: %s", want, create)
		}
	}
	if strings.Contains(create, "/data:rw") {
		t.Fatalf("source volume is not read-only: %s", create)
	}
	if !strings.HasPrefix(lines[1], "docker start -a dcraft-backup-") {
		t.Fatalf("start call = %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "docker rm -f dcraft-backup-") {
		t.Fatalf("cleanup call = %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "docker inspect --format ") {
		t.Fatalf("cleanup confirmation = %q", lines[3])
	}
}

func TestArchiveVolumeRemovesTheHelperOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &helperLifecycle{cancelOnStart: cancel, startErr: context.Canceled}
	c := newHelperCompose(t, h)
	_, err := c.ArchiveVolume(ctx, "proj_data", "/backups/work")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	lines := h.lines()
	var removed bool
	for _, line := range lines {
		if strings.HasPrefix(line, "docker rm -f dcraft-backup-") {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("helper was not removed on cancellation: %v", lines)
	}
}

func TestArchiveVolumeRetriesCleanupUntilTheContainerIsGone(t *testing.T) {
	h := &helperLifecycle{
		// The first inspect still finds the container; rm then succeeds and
		// the next inspect reports it gone.
		inspectResults: []execx.Result{{Stdout: []byte("cid")}, {Stderr: []byte("No such object"), ExitCode: 1}},
	}
	c := newHelperCompose(t, h)
	if _, err := c.ArchiveVolume(context.Background(), "proj_data", "/backups/work"); err != nil {
		t.Fatalf("ArchiveVolume: %v", err)
	}
	var removes int
	for _, line := range h.lines() {
		if strings.HasPrefix(line, "docker rm -f dcraft-backup-") {
			removes++
		}
	}
	if removes < 2 {
		t.Fatalf("cleanup did not retry: %v", h.lines())
	}
}

func TestArchiveVolumeReportsACleanupFailure(t *testing.T) {
	h := &helperLifecycle{alwaysExists: true}
	c := newHelperCompose(t, h)
	c.cleanupTimeout = 50 * time.Millisecond
	_, err := c.ArchiveVolume(context.Background(), "proj_data", "/backups/work")
	if err == nil || !strings.Contains(err.Error(), "remove backup helper") {
		t.Fatalf("error = %v", err)
	}
}

func TestArchiveVolumeReportsTheHelperFailureOutput(t *testing.T) {
	h := &helperLifecycle{startResult: execx.Result{Stderr: []byte("tar: can't open './secret': Permission denied"), ExitCode: 1}}
	c := newHelperCompose(t, h)
	out, err := c.ArchiveVolume(context.Background(), "proj_data", "/backups/work")
	if err == nil {
		t.Fatal("archive failure was swallowed")
	}
	if !strings.Contains(out.Output, "Permission denied") {
		t.Fatalf("output = %q", out.Output)
	}
}

func TestArchiveVolumeRemovesTheHelperWhenTheContextIsAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := &helperLifecycle{createErr: context.Canceled}
	c := newHelperCompose(t, h)
	if _, err := c.ArchiveVolume(ctx, "proj_data", "/backups/work"); err == nil {
		t.Fatal("canceled archive did not fail")
	}
	var removed bool
	for _, line := range h.lines() {
		if strings.HasPrefix(line, "docker rm -f dcraft-backup-") {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("helper was not removed after cancellation: %v", h.lines())
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

func TestArchiveVolumeRemovesAContainerCreatedAfterACanceledCreate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := &helperLifecycle{
		createErr: context.Canceled,
		// First inspection sees nothing; the daemon completes the create
		// late, so the next inspection finds it. Cleanup must still remove it.
		inspectResults: []execx.Result{
			{Stderr: []byte("No such object"), ExitCode: 1},
			{Stdout: []byte("late-cid")},
			{Stderr: []byte("No such object"), ExitCode: 1},
		},
	}
	c := newHelperCompose(t, h)
	c.cleanupGrace = 500 * time.Millisecond
	if _, err := c.ArchiveVolume(ctx, "proj_data", "/backups/work"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if names := backupHelperNamesFromCalls(h.lines()); len(names) == 0 {
		t.Fatalf("late-created helper was not removed: %v", h.lines())
	}
	var removes int
	for _, line := range h.lines() {
		if strings.HasPrefix(line, "docker rm -f dcraft-backup-") {
			removes++
		}
	}
	if removes < 2 {
		t.Fatalf("cleanup did not wait for a late create: %v", h.lines())
	}
}

func backupHelperNamesFromCalls(lines []string) []string {
	var names []string
	for _, line := range lines {
		if strings.HasPrefix(line, "docker rm -f ") {
			names = append(names, strings.TrimPrefix(line, "docker rm -f "))
		}
	}
	return names
}

func TestBindConsumersMergeTheHostAndDesktopVisibleSpellings(t *testing.T) {
	var calls []string
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		line := cmd.Name + " " + strings.Join(cmd.Args, " ")
		calls = append(calls, line)
		switch {
		case strings.Contains(line, "volume=/host_mnt/host/data"):
			return execx.Result{Stdout: []byte("desk-app-1\tdesk\tapp\trunning\n")}, nil
		case strings.Contains(line, "volume=/host/data"):
			return execx.Result{Stdout: []byte("native-app-1\tnative\tapp\texited\n")}, nil
		default:
			return execx.Result{}, nil
		}
	}}
	c := New(runner, nil)
	consumers, err := c.BindConsumers(context.Background(), dataTarget("/project"), "/host/data")
	if err != nil {
		t.Fatalf("BindConsumers: %v", err)
	}
	if len(consumers) != 2 {
		t.Fatalf("consumers = %+v", consumers)
	}
	if consumers[0].Project != "desk" || consumers[1].State != "exited" {
		t.Fatalf("consumers = %+v", consumers)
	}
	log := strings.Join(calls, "\n")
	if !strings.Contains(log, "ps -a --filter volume=/host/data") || !strings.Contains(log, "ps -a --filter volume=/host_mnt/host/data") {
		t.Fatalf("both spellings were not queried:\n%s", log)
	}
	// The same container returned by both spellings is reported once.
	calls = nil
	runner = &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stdout: []byte("same-app-1\tproj\tapp\trunning\n")}, nil
	}}
	c = New(runner, nil)
	consumers, err = c.BindConsumers(context.Background(), dataTarget("/project"), "/host/data")
	if err != nil {
		t.Fatal(err)
	}
	if len(consumers) != 1 {
		t.Fatalf("duplicate consumers = %+v", consumers)
	}
}
