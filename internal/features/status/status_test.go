package status_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/features/status"
	"github.com/rootmyself/dcraft/internal/stack"
)

type fakeBackend struct {
	snap   stack.Snapshot
	err    error
	called bool
	target core.Target
}

func (f *fakeBackend) Name() string { return "compose" }

func (f *fakeBackend) Inspect(ctx context.Context, target core.Target) (stack.Snapshot, error) {
	f.called = true
	f.target = target
	return f.snap, f.err
}

type testReporter struct{ stages []core.Stage }

func (r *testReporter) Begin(name, title string) {
	r.stages = append(r.stages, core.Stage{Name: name, Title: title, Status: core.StageStarted})
}

func (r *testReporter) End(name string, status core.StageStatus, detail string) {
	for i := len(r.stages) - 1; i >= 0; i-- {
		if r.stages[i].Name == name {
			r.stages[i].Status = status
			r.stages[i].Detail = detail
			return
		}
	}
}

func sampleSnapshot() stack.Snapshot {
	return stack.Snapshot{
		Backend:     "compose",
		ProjectName: "hello-world",
		ProjectDir:  "/work/hello-world",
		Services: []stack.Service{
			{
				Name:                  "web",
				Configured:            true,
				Image:                 "nginx:alpine",
				HealthcheckConfigured: false,
				State:                 stack.StateRunning,
				Health:                stack.HealthNone,
				AppVerified:           false,
				Instances: []stack.Instance{
					{Name: "hello-world-web-1", State: "running", Health: stack.HealthNone, Image: "nginx:alpine"},
				},
				Connections: []stack.Connection{
					{Source: stack.SourceRuntime, HostIP: "0.0.0.0", PublishedPort: 8080, TargetPort: 80, Protocol: "tcp"},
				},
			},
			{
				Name:                  "api",
				Configured:            true,
				Image:                 "api:local",
				Build:                 true,
				HealthcheckConfigured: true,
				State:                 stack.StateRunning,
				Health:                stack.HealthHealthy,
				AppVerified:           true,
				Instances: []stack.Instance{
					{Name: "hello-world-api-1", State: "running", Health: stack.HealthHealthy, Image: "api:local"},
				},
				Connections: []stack.Connection{},
			},
			{
				Name:        "db",
				Configured:  true,
				Image:       "postgres:16",
				State:       stack.StateNotCreated,
				Health:      stack.HealthNone,
				Instances:   []stack.Instance{},
				Connections: []stack.Connection{{Source: stack.SourceConfig, TargetPort: 5432, Protocol: "tcp", PublishedPort: 55432}},
			},
		},
		Summary: stack.Summary{Services: 3, Instances: 2, Running: 2, Healthy: 1, Unverified: 1, NotCreated: 1},
	}
}

func successResult(t *testing.T, snap stack.Snapshot) core.Result {
	t.Helper()
	return core.Result{
		SchemaVersion: core.SchemaVersion,
		Action:        "status",
		Outcome:       core.OutcomeSuccess,
		Target: core.Target{
			ProjectDir: "/work/hello-world",
			Files:      []string{"/work/hello-world/compose.yaml", "/work/hello-world/compose.override.yaml"},
			EnvFiles:   []string{},
			Profiles:   []string{},
		},
		Tools:       core.ToolInfo{DockerVersion: "29.10.0", ComposeVersion: "5.7.0", EndpointKind: "unix"},
		Diagnostics: []core.Diagnostic{},
		Data:        status.Payload{Stack: snap},
	}
}

func TestDescriptorPublishesStatusAsPrimaryReadOnlyFeature(t *testing.T) {
	f := status.New(&fakeBackend{})
	d := f.Descriptor()
	if d.Name != "status" {
		t.Fatalf("name = %q", d.Name)
	}
	if !d.Primary {
		t.Fatal("status must be the primary TUI screen")
	}
	if !d.ReadOnly {
		t.Fatal("status must be read-only")
	}
	if d.Approval != core.ApprovalNone {
		t.Fatalf("approval = %q", d.Approval)
	}
	if d.RecordsHistory {
		t.Fatal("status must not record history")
	}
	for _, text := range []string{d.Title, d.Summary, d.Usage} {
		if strings.TrimSpace(text) == "" {
			t.Fatalf("descriptor text missing: %+v", d)
		}
	}
}

func TestRunInspectsTargetThroughBackendAndReportsStage(t *testing.T) {
	backend := &fakeBackend{snap: sampleSnapshot()}
	f := status.New(backend)
	rep := &testReporter{}
	target := core.Target{ProjectDir: "/work/hello-world", Files: []string{"/work/hello-world/compose.yaml"}}
	payload, err := f.Run(context.Background(), core.Invocation{Target: target, Reporter: rep})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !backend.called || backend.target.ProjectDir != target.ProjectDir {
		t.Fatalf("backend called=%v target=%+v", backend.called, backend.target)
	}
	p, ok := payload.(status.Payload)
	if !ok {
		t.Fatalf("payload type = %T", payload)
	}
	if p.Stack.ProjectName != "hello-world" {
		t.Fatalf("payload = %+v", p)
	}
	if len(rep.stages) != 1 || rep.stages[0].Name != "inspect_stack" {
		t.Fatalf("stages = %+v", rep.stages)
	}
	if rep.stages[0].Status != core.StageSucceeded {
		t.Fatalf("stage status = %q", rep.stages[0].Status)
	}
	if rep.stages[0].Title == "" {
		t.Fatal("stage title missing")
	}
}

func TestRunPropagatesBackendFailureAndMarksStage(t *testing.T) {
	backend := &fakeBackend{err: core.Fail(core.Diagnostic{
		Category: core.CategoryNoConfiguration, Severity: core.SeverityError, Message: "no config",
	})}
	rep := &testReporter{}
	_, err := status.New(backend).Run(context.Background(), core.Invocation{Reporter: rep})
	if err == nil {
		t.Fatal("Run succeeded, want error")
	}
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v", err)
	}
	if ce.Diagnostics[0].Category != core.CategoryNoConfiguration {
		t.Fatalf("diagnostics = %+v", ce.Diagnostics)
	}
	if len(rep.stages) != 1 || rep.stages[0].Status != core.StageFailed {
		t.Fatalf("stages = %+v", rep.stages)
	}
}

func TestPresentShowsStackServicesInstancesConnectionsAndVerification(t *testing.T) {
	f := status.New(&fakeBackend{})
	res := successResult(t, sampleSnapshot())
	out := f.Present(res, 0)

	for _, want := range []string{
		"hello-world",
		"/work/hello-world",
		"compose.override.yaml",
		"29.10.0",
		"5.7.0",
		"web",
		"실행 중",
		"healthcheck 없음",
		"앱 확인 안 됨",
		"hello-world-web-1",
		"0.0.0.0:8080",
		"80/tcp",
		"api",
		"healthy",
		"앱 확인됨",
		"db",
		"컨테이너 없음",
		"55432",
		"dcraft status",
		"dcraft",
		"관리 작업",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{"dcraft backup", "dcraft restore", "dcraft rollback"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("output advertises unimplemented command %q:\n%s", forbidden, out)
		}
	}
}

func TestPresentDistinguishesUnverifiedFromHealthy(t *testing.T) {
	f := status.New(&fakeBackend{})
	res := successResult(t, sampleSnapshot())
	out := f.Present(res, 0)
	if !strings.Contains(out, "앱 확인 안 됨") {
		t.Fatalf("missing unverified wording:\n%s", out)
	}
	if !strings.Contains(out, "앱 확인됨") {
		t.Fatalf("missing verified wording:\n%s", out)
	}
}

func TestPresentShowsEmptyStack(t *testing.T) {
	f := status.New(&fakeBackend{})
	snap := stack.Snapshot{Backend: "compose", ProjectName: "empty", ProjectDir: "/work/empty", Services: []stack.Service{}}
	res := successResult(t, snap)
	out := f.Present(res, 0)
	if !strings.Contains(out, "서비스가 없습니다") {
		t.Fatalf("empty stack not explained:\n%s", out)
	}
}

func TestPresentMarksUnconfiguredRunningServices(t *testing.T) {
	f := status.New(&fakeBackend{})
	snap := sampleSnapshot()
	snap.Services = append(snap.Services, stack.Service{
		Name:       "oldtool",
		Configured: false,
		State:      stack.StateRunning,
		Health:     stack.HealthNone,
		Instances:  []stack.Instance{{Name: "hello-world-oldtool-1", State: "running"}},
	})
	res := successResult(t, snap)
	out := f.Present(res, 0)
	if !strings.Contains(out, "현재 Compose 설정에 없음") {
		t.Fatalf("unconfigured service not explained:\n%s", out)
	}
}

func TestPresentShowsAutomaticDotEnv(t *testing.T) {
	f := status.New(&fakeBackend{})
	res := successResult(t, sampleSnapshot())
	res.Target.DefaultEnvFile = "/work/hello-world/.env"
	out := f.Present(res, 0)
	if !strings.Contains(out, "/work/hello-world/.env (자동 적용)") {
		t.Fatalf("automatic .env not shown:\n%s", out)
	}
}

func TestPresentHandlesMissingPayloadGracefully(t *testing.T) {
	f := status.New(&fakeBackend{})
	res := successResult(t, sampleSnapshot())
	res.Data = nil
	out := f.Present(res, 0)
	if out == "" {
		t.Fatal("presenter returned empty output for missing payload")
	}
	if !strings.Contains(out, "상태 정보를 표시할 수 없습니다") {
		t.Fatalf("missing payload not explained: %q", out)
	}
}

func TestPresentWrapsAtRequestedWidth(t *testing.T) {
	f := status.New(&fakeBackend{})
	res := successResult(t, sampleSnapshot())
	out := f.Present(res, 40)
	for _, line := range strings.Split(out, "\n") {
		// Guidance may wrap; allow up to the requested width plus margin.
		if len([]rune(line)) > 80 {
			t.Fatalf("line longer than expected at width 40: %q", line)
		}
	}
}
