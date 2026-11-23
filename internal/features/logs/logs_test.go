package logs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

type fakeBackend struct {
	snap stack.Snapshot
	err  error
}

func (f *fakeBackend) Name() string { return "fake" }
func (f *fakeBackend) Inspect(ctx context.Context, target core.Target) (stack.Snapshot, error) {
	return f.snap, f.err
}

func scaledSnapshot() stack.Snapshot {
	return stack.Snapshot{Services: []stack.Service{
		{Name: "db", Configured: true, State: stack.StateRunning, Instances: []stack.Instance{{Name: "p-db-1", State: "running"}}},
		{Name: "web", Configured: true, State: stack.StateRunning, Instances: []stack.Instance{
			{Name: "p-web-1", State: "running"}, {Name: "p-web-2", State: "running"},
		}},
	}}
}

type recordedCall struct {
	req     stack.LogsRequest
	streams core.IO
}

type fakeStreamer struct {
	mu     sync.Mutex
	calls  []recordedCall
	write  string
	err    error
	block  bool
	cancel context.CancelFunc
}

func (f *fakeStreamer) Logs(ctx context.Context, target core.Target, req stack.LogsRequest, streams core.IO) error {
	f.mu.Lock()
	f.calls = append(f.calls, recordedCall{req: req, streams: streams})
	f.mu.Unlock()
	if f.write != "" && streams.Stdout != nil {
		_, _ = streams.Stdout.Write([]byte(f.write))
	}
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return f.err
}

func (f *fakeStreamer) Exec(ctx context.Context, target core.Target, req stack.ExecRequest, streams core.IO) (stack.ExecResult, error) {
	return stack.ExecResult{}, errors.New("not used")
}

func (f *fakeStreamer) last(t *testing.T) recordedCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("streamer was not called")
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeStreamer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeReporter struct {
	mu     sync.Mutex
	stages []core.Stage
}

func (r *fakeReporter) Begin(name, title string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stages = append(r.stages, core.Stage{Name: name, Title: title, Status: core.StageStarted})
}

func (r *fakeReporter) End(name string, status core.StageStatus, detail string) {
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

func (r *fakeReporter) find(name string) (core.Stage, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, st := range r.stages {
		if st.Name == name {
			return st, true
		}
	}
	return core.Stage{}, false
}

func invocation(services []string, options map[string]string, streams core.IO, reporter core.Reporter) core.Invocation {
	if options == nil {
		options = map[string]string{}
	}
	return core.Invocation{
		Target:   core.Target{ProjectDir: "/p", Files: []string{"/p/compose.yaml"}},
		Services: services,
		Options:  options,
		IO:       streams,
		Reporter: reporter,
	}
}

func TestLogsDescriptorIsReadOnlyLiveStreamingWithServiceSelection(t *testing.T) {
	f := New(&fakeBackend{}, &fakeStreamer{})
	d := f.Descriptor()
	if d.Name != "logs" || !d.ReadOnly || !d.SupportsServices {
		t.Fatalf("descriptor = %+v", d)
	}
	if d.Title == "" || d.Summary == "" || d.Usage == "" {
		t.Fatalf("missing Korean text: %+v", d)
	}
	if mode := f.LiveMode(); mode != core.LiveStream {
		t.Fatalf("live mode = %q", mode)
	}
	want := map[string]bool{"no-follow": false, "tail": false, "since": false, "timestamps": false, "index": false}
	for _, fl := range d.Flags {
		if _, ok := want[fl.Name]; !ok {
			t.Fatalf("unexpected flag %q", fl.Name)
		}
		want[fl.Name] = true
		if fl.Name == "index" && fl.Shorthand != "i" {
			t.Fatalf("index shorthand = %q", fl.Shorthand)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("missing flag %q", name)
		}
	}
}

func TestLogsDefaultsToFollowingTheWholeStackWithoutColorWhenNotATTY(t *testing.T) {
	streamer := &fakeStreamer{write: "p-web-1 | hello\n"}
	out := &strings.Builder{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	payload, err := f.Run(context.Background(), invocation(nil, nil, core.IO{Stdout: out, TTY: false}, &fakeReporter{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	call := streamer.last(t)
	if !call.req.Follow || call.req.Color || call.req.Index != -1 {
		t.Fatalf("request = %+v", call.req)
	}
	if strings.Join(call.req.Services, ",") != "db,web" {
		t.Fatalf("services = %v", call.req.Services)
	}
	if call.req.Tail != "" || call.req.Since != "" || call.req.Timestamps {
		t.Fatalf("unexpected defaults: %+v", call.req)
	}
	report := payload.(Payload).Logs
	if !report.Follow || strings.Join(report.Services, ",") != "db,web" || report.Index != -1 {
		t.Fatalf("payload = %+v", report)
	}
	if !strings.Contains(out.String(), "p-web-1 | hello") {
		t.Fatalf("live output = %q", out.String())
	}
}

func TestLogsMapsFlagsAndInstanceSelection(t *testing.T) {
	streamer := &fakeStreamer{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	options := map[string]string{
		"no-follow":  "true",
		"tail":       "100",
		"since":      "10m",
		"timestamps": "true",
		"index":      "1",
	}
	payload, err := f.Run(context.Background(), invocation([]string{"web"}, options, core.IO{Stdout: &strings.Builder{}, TTY: true}, &fakeReporter{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	call := streamer.last(t)
	if call.req.Follow || !call.req.Color || call.req.Tail != "100" || call.req.Since != "10m" || !call.req.Timestamps {
		t.Fatalf("request = %+v", call.req)
	}
	if call.req.Index != 1 || len(call.req.Services) != 1 || call.req.Services[0] != "web" {
		t.Fatalf("target = %+v", call.req)
	}
	report := payload.(Payload).Logs
	if report.Instance != "p-web-2" || report.Index != 1 {
		t.Fatalf("payload = %+v", report)
	}
}

func TestLogsRequiresOneServiceWhenAnInstanceIndexIsGiven(t *testing.T) {
	streamer := &fakeStreamer{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	_, err := f.Run(context.Background(), invocation(nil, map[string]string{"index": "0"}, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{}))
	if err == nil {
		t.Fatal("expected a selection failure")
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategorySelectionRequired {
		t.Fatalf("error = %v", err)
	}
	if streamer.callCount() != 0 {
		t.Fatal("logs started a stream despite an unusable selection")
	}
}

func TestLogsRejectsAnUnknownServiceWithoutStreaming(t *testing.T) {
	streamer := &fakeStreamer{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	_, err := f.Run(context.Background(), invocation([]string{"cache"}, nil, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{}))
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategoryServiceUnknown {
		t.Fatalf("error = %v", err)
	}
	if streamer.callCount() != 0 {
		t.Fatal("logs streamed an unknown service")
	}
}

func TestLogsReportsBackendFailureInTheSelectionStage(t *testing.T) {
	reporter := &fakeReporter{}
	f := New(&fakeBackend{err: core.Fail(core.Diagnostic{
		Category: core.CategoryNoConfiguration, Severity: core.SeverityError, Message: "Compose 설정 찾기 실패.",
	})}, &fakeStreamer{})
	_, err := f.Run(context.Background(), invocation(nil, nil, core.IO{Stdout: &strings.Builder{}}, reporter))
	if err == nil {
		t.Fatal("expected a backend failure")
	}
	st, ok := reporter.find(stageSelect)
	if !ok || st.Status != core.StageFailed {
		t.Fatalf("selection stage = %+v", st)
	}
}

func TestLogsCancellationDetachesWithoutStoppingServices(t *testing.T) {
	streamer := &fakeStreamer{block: true}
	ctx, cancel := context.WithCancel(context.Background())
	reporter := &fakeReporter{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	done := make(chan error, 1)
	go func() {
		_, err := f.Run(ctx, invocation(nil, nil, core.IO{Stdout: &strings.Builder{}}, reporter))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("logs follow did not stop after cancellation")
	}
	st, ok := reporter.find(stageStream)
	if !ok || st.Status != core.StageCanceled {
		t.Fatalf("stream stage = %+v", st)
	}
	if !strings.Contains(st.Detail, "분리") || !strings.Contains(st.Detail, "계속 실행") {
		t.Fatalf("cancel detail = %q", st.Detail)
	}
	d := f.CanceledDiagnostic()
	if !strings.Contains(d.Message, "분리") || !strings.Contains(d.Message, "계속 실행") {
		t.Fatalf("canceled diagnostic = %+v", d)
	}
	if len(d.NextActions) == 0 {
		t.Fatal("no next actions")
	}
}

func TestLogsStreamFailureIsAnOperationFailureWithKoreanGuidance(t *testing.T) {
	streamer := &fakeStreamer{err: &stack.CommandError{Action: "logs", ExitCode: 1, Output: "no such container"}}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	_, err := f.Run(context.Background(), invocation([]string{"web"}, nil, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{}))
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v", err)
	}
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryLogsFailed || !strings.Contains(d.Message, "로그") {
		t.Fatalf("diagnostic = %+v", d)
	}
	if !strings.Contains(d.Detail, "no such container") || len(d.NextActions) == 0 {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestLogsPresenterStatesThatServicesKeepRunning(t *testing.T) {
	f := New(&fakeBackend{}, &fakeStreamer{})
	payload := Payload{Logs: Report{Follow: true, Services: []string{"web"}, Instance: "p-web-1", Index: 0}}
	text := f.Present(core.Result{Outcome: core.OutcomeSuccess, Data: payload}, 0)
	for _, want := range []string{"web", "p-web-1", "계속 실행"} {
		if !strings.Contains(text, want) {
			t.Fatalf("presenter missing %q:\n%s", want, text)
		}
	}
}
