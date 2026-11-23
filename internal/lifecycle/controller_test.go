package lifecycle_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/lifecycle"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

type reporter struct {
	mu     sync.Mutex
	stages []core.Stage
}

func (r *reporter) Begin(name, title string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stages = append(r.stages, core.Stage{Name: name, Title: title, Status: core.StageStarted})
}
func (r *reporter) End(name string, status core.StageStatus, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.stages) - 1; i >= 0; i-- {
		if r.stages[i].Name == name && r.stages[i].Status == core.StageStarted {
			r.stages[i].Status = status
			r.stages[i].Detail = detail
			return
		}
	}
}
func (r *reporter) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.stages))
	for _, st := range r.stages {
		out = append(out, st.Name)
	}
	return out
}
func (r *reporter) status(name string) core.StageStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, st := range r.stages {
		if st.Name == name {
			return st.Status
		}
	}
	return ""
}

type fakeLifecycle struct {
	mu       sync.Mutex
	calls    []string
	services [][]string
	targets  []core.Target
	started  chan struct{}
	block    chan struct{}
	result   stack.OpResult
	err      error
	onCall   func()
}

func (f *fakeLifecycle) record(ctx context.Context, action string, target core.Target, services []string) (stack.OpResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, action)
	f.services = append(f.services, append([]string{}, services...))
	f.targets = append(f.targets, target)
	started, block, hook := f.started, f.block, f.onCall
	err := f.err
	res := f.result
	// A test-provided start signal and block apply to the first call only.
	f.started, f.block = nil, nil
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if started != nil {
		close(started)
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
	}
	return res, err
}

func (f *fakeLifecycle) Up(ctx context.Context, t core.Target, services []string) (stack.OpResult, error) {
	return f.record(ctx, "up", t, services)
}
func (f *fakeLifecycle) Stop(ctx context.Context, t core.Target, services []string) (stack.OpResult, error) {
	return f.record(ctx, "stop", t, services)
}
func (f *fakeLifecycle) Down(ctx context.Context, t core.Target) (stack.OpResult, error) {
	return f.record(ctx, "down", t, nil)
}
func (f *fakeLifecycle) Restart(ctx context.Context, t core.Target, services []string) (stack.OpResult, error) {
	return f.record(ctx, "restart", t, services)
}
func (f *fakeLifecycle) callNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

type fakeIdentity struct {
	config    stack.ConfigInfo
	derived   string
	projects  []stack.ProjectInfo
	err       error
	configErr error
}

func (f *fakeIdentity) ConfigInfo(ctx context.Context, t core.Target) (stack.ConfigInfo, error) {
	if f.configErr != nil {
		return stack.ConfigInfo{}, f.configErr
	}
	return f.config, nil
}
func (f *fakeIdentity) DerivedProjectName(ctx context.Context, t core.Target) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.derived, nil
}
func (f *fakeIdentity) Projects(ctx context.Context) ([]stack.ProjectInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.projects, nil
}

type fakeBackend struct {
	snap stack.Snapshot
	err  error
}

func (f *fakeBackend) Name() string { return "fake" }
func (f *fakeBackend) Inspect(ctx context.Context, t core.Target) (stack.Snapshot, error) {
	if f.err != nil {
		return stack.Snapshot{}, f.err
	}
	return f.snap, nil
}

type harness struct {
	controller *lifecycle.Controller
	life       *fakeLifecycle
	identity   *fakeIdentity
	backend    *fakeBackend
	store      *state.Store
	stateDir   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := state.New(dir)
	h := &harness{
		life:     &fakeLifecycle{},
		identity: &fakeIdentity{config: stack.ConfigInfo{EffectiveName: "", Services: []string{"api", "web"}}, derived: "hello-world"},
		backend:  &fakeBackend{snap: runningSnapshot()},
		store:    store,
		stateDir: dir,
	}
	h.controller = lifecycle.New(h.life, h.identity, h.backend, store, core.SystemClock{})
	return h
}

func runningSnapshot() stack.Snapshot {
	return stack.Snapshot{
		Backend: "fake", ProjectName: "hello-world", ProjectDir: "/work/hello-world",
		Services: []stack.Service{{
			Name: "web", Configured: true, State: stack.StateRunning, Health: stack.HealthNone,
			Instances:   []stack.Instance{{Name: "hello-world-web-1", State: "running"}},
			Connections: []stack.Connection{},
		}},
		Summary: stack.Summary{Services: 1, Instances: 1, Running: 1, Unverified: 1},
	}
}

func invocation(services ...string) core.Invocation {
	return core.Invocation{
		Target: core.Target{
			ProjectDir: "/work/hello-world",
			Files:      []string{"/work/hello-world/compose.yaml"},
			EnvFiles:   []string{},
			Profiles:   []string{},
		},
		Services: services,
		Reporter: &reporter{},
		Clock:    core.SystemClock{},
	}
}

func (h *harness) run(t *testing.T, action lifecycle.Action, in core.Invocation) (lifecycle.Payload, error) {
	t.Helper()
	return h.controller.Run(context.Background(), in, action)
}

func category(err error) core.Category {
	var ce *core.Error
	if errors.As(err, &ce) && len(ce.Diagnostics) > 0 {
		return ce.Diagnostics[0].Category
	}
	return ""
}

func TestUpSuccessReportsNameScopeAndObservedState(t *testing.T) {
	h := newHarness(t)
	in := invocation()
	payload, err := h.run(t, lifecycle.ActionUp, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rep := in.Reporter.(*reporter)
	want := []string{"resolve_project", "coordinate", "allocate_name", "up", "observe"}
	if got := rep.names(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	for _, name := range want {
		if rep.status(name) != core.StageSucceeded {
			t.Fatalf("stage %s = %q", name, rep.status(name))
		}
	}
	report := payload.Lifecycle
	if report.ProjectName != "hello-world" || report.ProjectNameSource != "allocated" {
		t.Fatalf("report = %+v", report)
	}
	if report.Scope != "stack" || len(report.Services) != 0 {
		t.Fatalf("scope = %+v", report)
	}
	if !report.ConfigApplied {
		t.Fatal("up must state that the current config was applied")
	}
	if report.VolumesRemoved {
		t.Fatal("up claimed to remove volumes")
	}
	if report.State == nil || report.State.Summary.Running != 1 {
		t.Fatalf("state = %+v", report.State)
	}
	if len(report.Notices) != 0 {
		t.Fatalf("notices = %v", report.Notices)
	}
	if calls := h.life.callNames(); strings.Join(calls, ",") != "up" {
		t.Fatalf("lifecycle calls = %v", calls)
	}
	if got := h.life.targets[0].ProjectName; got != "hello-world" {
		t.Fatalf("compose was called with project name %q", got)
	}
}

func TestServiceSelectionReachesTheControllerAndCompose(t *testing.T) {
	h := newHarness(t)
	payload, err := h.run(t, lifecycle.ActionStop, invocation("web", "web", " api "))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := payload.Lifecycle
	if report.Scope != "services" || strings.Join(report.Services, ",") != "web,api" {
		t.Fatalf("report = %+v", report)
	}
	if got := strings.Join(h.life.services[0], ","); got != "web,api" {
		t.Fatalf("compose services = %q", got)
	}
	if report.ConfigApplied {
		t.Fatal("stop claimed to apply configuration")
	}
}

func TestUnknownServiceFailsBeforeAnyChange(t *testing.T) {
	h := newHarness(t)
	_, err := h.run(t, lifecycle.ActionUp, invocation("worker"))
	if category(err) != core.CategoryServiceUnknown {
		t.Fatalf("err = %v", err)
	}
	if calls := h.life.callNames(); len(calls) != 0 {
		t.Fatalf("docker was changed: %v", calls)
	}
	if fileExists(t, h.stateDir+"/names.json") {
		t.Fatal("invalid service selection allocated a name")
	}
	var ce *core.Error
	errors.As(err, &ce)
	if !strings.Contains(ce.Diagnostics[0].Detail, "api") || !strings.Contains(ce.Diagnostics[0].Detail, "web") {
		t.Fatalf("detail does not list available services: %+v", ce.Diagnostics[0])
	}
}

func TestDeclaredNameConflictStopsBeforeAnyChange(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(t, lifecycle.ActionUp, invocation()); err != nil {
		t.Fatal(err)
	}
	// A second target asks for the name the first target owns.
	in := invocation()
	in.Target.ProjectDir = "/work/other"
	in.Target.Files = []string{"/work/other/compose.yaml"}
	h.identity.config.EffectiveName = "hello-world"
	h.identity.derived = "other"
	_, err := h.run(t, lifecycle.ActionUp, in)
	if category(err) != core.CategoryProjectNameConflict {
		t.Fatalf("err = %v", err)
	}
	if calls := h.life.callNames(); strings.Join(calls, ",") != "up" {
		t.Fatalf("conflicting target changed docker: %v", calls)
	}
	var ce *core.Error
	errors.As(err, &ce)
	for _, want := range []string{"hello-world", "project-name"} {
		if !strings.Contains(ce.Diagnostics[0].Message+strings.Join(ce.Diagnostics[0].NextActions, " "), want) {
			t.Fatalf("conflict guidance missing %q: %+v", want, ce.Diagnostics[0])
		}
	}
}

func TestBusyStackFailsWithoutWaitingAndNamesTheHolder(t *testing.T) {
	h := newHarness(t)
	lock, err := h.store.AcquireStackLock(context.Background(),
		core.Target{ProjectDir: "/work/hello-world"}, state.LockHolder{Action: "down", PID: 4242, StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	_, err = h.run(t, lifecycle.ActionUp, invocation())
	if category(err) != core.CategoryStackBusy {
		t.Fatalf("err = %v", err)
	}
	if calls := h.life.callNames(); len(calls) != 0 {
		t.Fatalf("busy stack was changed: %v", calls)
	}
	var ce *core.Error
	errors.As(err, &ce)
	if !strings.Contains(ce.Diagnostics[0].Detail, "down") && !strings.Contains(ce.Diagnostics[0].Message, "down") {
		t.Fatalf("busy diagnostic does not name the holder: %+v", ce.Diagnostics[0])
	}
	if strings.Contains(strings.Join(ce.Diagnostics[0].NextActions, " "), "기다리기") &&
		!strings.Contains(strings.Join(ce.Diagnostics[0].NextActions, " "), "끝난 뒤") {
		t.Fatalf("next actions suggest unbounded waiting: %v", ce.Diagnostics[0].NextActions)
	}
}

func TestBusyRenameLeavesTheNameRegistryByteIdentical(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(t, lifecycle.ActionUp, invocation()); err != nil {
		t.Fatal(err)
	}
	namesPath := filepath.Join(h.stateDir, "names.json")
	before, err := os.ReadFile(namesPath)
	if err != nil {
		t.Fatal(err)
	}

	lock, err := h.store.AcquireStackLock(context.Background(),
		core.Target{ProjectDir: "/work/hello-world"}, state.LockHolder{Action: "down", PID: 4242, StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	rename := invocation()
	rename.Target.ProjectName = "renamed-stack"
	h.identity.config.EffectiveName = "renamed-stack"
	_, err = h.run(t, lifecycle.ActionUp, rename)
	_ = lock.Release()
	if category(err) != core.CategoryStackBusy {
		t.Fatalf("err = %v, want stack_busy", err)
	}

	after, err := os.ReadFile(namesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("blocked rename changed names.json:\nbefore=%s\nafter=%s", before, after)
	}
	// The next operation still adopts the original name, never the blocked one.
	h.identity.config.EffectiveName = ""
	payload, err := h.run(t, lifecycle.ActionUp, invocation())
	if err != nil {
		t.Fatal(err)
	}
	if payload.Lifecycle.ProjectName != "hello-world" {
		t.Fatalf("next operation used %q, want hello-world", payload.Lifecycle.ProjectName)
	}
}

func TestSameStackOperationsAreSerializedAndOtherStacksAreNot(t *testing.T) {
	h := newHarness(t)
	started := make(chan struct{})
	h.life.started = started
	block := make(chan struct{})
	h.life.block = block
	done := make(chan struct{})
	go func() {
		_, _ = h.run(t, lifecycle.ActionUp, invocation())
		close(done)
	}()
	<-started

	_, err := h.run(t, lifecycle.ActionDown, invocation())
	if category(err) != core.CategoryStackBusy {
		t.Fatalf("same-stack conflict err = %v", err)
	}

	other := invocation()
	other.Target.ProjectDir = "/work/other"
	other.Target.Files = []string{"/work/other/compose.yaml"}
	h.identity.config.EffectiveName = "other"
	h.identity.derived = "other"
	payload, err := h.run(t, lifecycle.ActionUp, other)
	if err != nil {
		t.Fatalf("other stack was blocked: %v", err)
	}
	if payload.Lifecycle.ProjectName != "other" {
		t.Fatalf("other project name = %q", payload.Lifecycle.ProjectName)
	}
	close(block)
	<-done
}

func TestActionFailureReportsFailedStageCurrentStateAndRecovery(t *testing.T) {
	h := newHarness(t)
	h.life.result = stack.OpResult{Output: "Error response from daemon: port is already allocated"}
	h.life.err = errors.New("docker compose up exited with status 1")
	_, err := h.run(t, lifecycle.ActionUp, invocation())
	if category(err) != core.CategoryLifecycleFailed {
		t.Fatalf("err = %v", err)
	}
	var ce *core.Error
	errors.As(err, &ce)
	diag := ce.Diagnostics[0]
	joined := diag.Message + " " + diag.Detail + " " + strings.Join(diag.NextActions, " ")
	for _, want := range []string{"시작", "현재 상태", "실행 중", "dcraft status", "port is already allocated"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("failure diagnostic missing %q: %+v", want, diag)
		}
	}
	if len(diag.NextActions) < 2 {
		t.Fatalf("next actions = %v", diag.NextActions)
	}
}

func TestActionFailureWithoutObservableStateStillReportsTheStage(t *testing.T) {
	h := newHarness(t)
	h.life.err = errors.New("boom")
	h.backend.err = errors.New("cannot inspect")
	in := invocation()
	_, err := h.run(t, lifecycle.ActionDown, in)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategoryLifecycleFailed {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(ce.Diagnostics[0].Message, "확인 실패") {
		t.Fatalf("missing observation caveat: %+v", ce.Diagnostics[0])
	}
	rep := in.Reporter.(*reporter)
	if rep.status("down") != core.StageFailed {
		t.Fatalf("down stage = %q", rep.status("down"))
	}
}

func TestCancellationIsPropagatedToTheEngine(t *testing.T) {
	h := newHarness(t)
	started := make(chan struct{})
	h.life.started = started
	h.life.block = make(chan struct{})
	h.life.err = context.Canceled
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := h.controller.Run(ctx, invocation(), lifecycle.ActionUp)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestObservationFailureDoesNotTurnACompletedChangeIntoFailure(t *testing.T) {
	h := newHarness(t)
	h.backend.err = errors.New("inspect unavailable")
	in := invocation()
	payload, err := h.run(t, lifecycle.ActionStop, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if payload.Lifecycle.State != nil {
		t.Fatalf("state = %+v, want unknown", payload.Lifecycle.State)
	}
	if len(payload.Lifecycle.Notices) != 1 || !strings.Contains(payload.Lifecycle.Notices[0], "확인 실패") {
		t.Fatalf("notices = %v", payload.Lifecycle.Notices)
	}
	if rep := in.Reporter.(*reporter); rep.status("observe") != core.StageFailed {
		t.Fatalf("observe stage = %q, want failed", rep.status("observe"))
	}
}

func TestDownAndRestartDoNotClaimConfigApplication(t *testing.T) {
	for _, action := range []lifecycle.Action{lifecycle.ActionDown, lifecycle.ActionRestart} {
		h := newHarness(t)
		payload, err := h.run(t, action, invocation())
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if payload.Lifecycle.ConfigApplied {
			t.Fatalf("%s claimed to apply configuration", action)
		}
		if payload.Lifecycle.VolumesRemoved {
			t.Fatalf("%s claimed to remove volumes", action)
		}
	}
}

func TestRenamingARecordedTargetProducesANotice(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(t, lifecycle.ActionUp, invocation()); err != nil {
		t.Fatal(err)
	}
	h.identity.config.EffectiveName = "chosen-name"
	in := invocation()
	in.Target.ProjectName = "chosen-name"
	payload, err := h.run(t, lifecycle.ActionUp, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if payload.Lifecycle.ProjectName != "chosen-name" {
		t.Fatalf("name = %q", payload.Lifecycle.ProjectName)
	}
	if len(payload.Lifecycle.Notices) == 0 || !strings.Contains(strings.Join(payload.Lifecycle.Notices, " "), "hello-world") {
		t.Fatalf("rename notice missing: %v", payload.Lifecycle.Notices)
	}
}

func TestConfigFailureStopsBeforeAllocation(t *testing.T) {
	h := newHarness(t)
	h.identity.configErr = core.Fail(core.Diagnostic{
		Category: core.CategoryInvalidConfiguration, Severity: core.SeverityError,
		Message: "Compose 설정을 읽을 수 없음.",
	})
	_, err := h.run(t, lifecycle.ActionUp, invocation())
	if category(err) != core.CategoryInvalidConfiguration {
		t.Fatalf("err = %v", err)
	}
	if calls := h.life.callNames(); len(calls) != 0 {
		t.Fatalf("invalid config changed docker: %v", calls)
	}
	if fileExists(t, h.stateDir+"/names.json") {
		t.Fatal("invalid config allocated a name")
	}
}

func TestPayloadIsMachineReadable(t *testing.T) {
	h := newHarness(t)
	payload, err := h.run(t, lifecycle.ActionUp, invocation())
	if err != nil {
		t.Fatal(err)
	}
	wrapped := map[string]any{"lifecycle": payload.Lifecycle}
	data, err := json.Marshal(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Lifecycle struct {
			Action            string   `json:"action"`
			ProjectName       string   `json:"project_name"`
			ProjectNameSource string   `json:"project_name_source"`
			Scope             string   `json:"scope"`
			Services          []string `json:"services"`
			VolumesRemoved    bool     `json:"volumes_removed"`
			ConfigApplied     bool     `json:"config_applied"`
			Notices           []string `json:"notices"`
		} `json:"lifecycle"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("payload JSON: %v\n%s", err, data)
	}
	if decoded.Lifecycle.Action != "up" || decoded.Lifecycle.Services == nil || decoded.Lifecycle.Notices == nil {
		t.Fatalf("payload = %+v\n%s", decoded.Lifecycle, data)
	}
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

func TestConfiguredNameIsHonoredAndConflictsAreReported(t *testing.T) {
	h := newHarness(t)
	h.identity.config.EffectiveName = "declared-name"
	payload, err := h.run(t, lifecycle.ActionUp, invocation())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if payload.Lifecycle.ProjectName != "declared-name" || payload.Lifecycle.ProjectNameSource != "configured" {
		t.Fatalf("report = %+v", payload.Lifecycle)
	}
	if got := h.life.targets[0].ProjectName; got != "declared-name" {
		t.Fatalf("compose project name = %q", got)
	}
	second := invocation()
	second.Target.ProjectDir = "/work/second"
	second.Target.Files = []string{"/work/second/compose.yaml"}
	_, err = h.run(t, lifecycle.ActionUp, second)
	if category(err) != core.CategoryProjectNameConflict {
		t.Fatalf("err = %v", err)
	}
}
