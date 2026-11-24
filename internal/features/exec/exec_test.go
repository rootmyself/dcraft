package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		{Name: "job", Configured: true, State: stack.StateExited, Instances: []stack.Instance{{Name: "p-job-1", State: "exited"}}},
	}}
}

type recordedExec struct {
	req     stack.ExecRequest
	streams core.IO
}

type fakeStreamer struct {
	mu     sync.Mutex
	calls  []recordedExec
	out    string
	errOut string
	result stack.ExecResult
	err    error
	block  bool
	// sequence answers the calls in order before falling back to result/err.
	// It is how a test states "the probe says X, then the attach says Y".
	sequence []fakeAnswer
}

// fakeAnswer is one scripted answer for fakeStreamer. An empty answer with a
// nil error is a success with exit 0.
type fakeAnswer struct {
	result stack.ExecResult
	err    error
}

func (f *fakeStreamer) Logs(ctx context.Context, target core.Target, req stack.LogsRequest, streams core.IO) error {
	return errors.New("not used")
}

func (f *fakeStreamer) Exec(ctx context.Context, target core.Target, req stack.ExecRequest, streams core.IO) (stack.ExecResult, error) {
	f.mu.Lock()
	index := len(f.calls)
	f.calls = append(f.calls, recordedExec{req: req, streams: streams})
	var answer fakeAnswer
	scripted := index < len(f.sequence)
	if scripted {
		answer = f.sequence[index]
	}
	f.mu.Unlock()
	if f.out != "" && streams.Stdout != nil {
		_, _ = streams.Stdout.Write([]byte(f.out))
	}
	if f.errOut != "" && streams.Stderr != nil {
		_, _ = streams.Stderr.Write([]byte(f.errOut))
	}
	if f.block {
		<-ctx.Done()
		return stack.ExecResult{}, ctx.Err()
	}
	if scripted {
		return answer.result, answer.err
	}
	return f.result, f.err
}

func (f *fakeStreamer) last(t *testing.T) recordedExec {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("streamer was not called")
	}
	return f.calls[len(f.calls)-1]
}

// recorded returns the requests the streamer received, in call order.
func (f *fakeStreamer) recorded() []recordedExec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedExec{}, f.calls...)
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

func invocation(services, args []string, options map[string]string, streams core.IO, reporter core.Reporter) core.Invocation {
	if options == nil {
		options = map[string]string{}
	}
	return core.Invocation{
		Target:   core.Target{ProjectDir: "/p", Files: []string{"/p/compose.yaml"}},
		Services: services,
		Args:     args,
		Options:  options,
		IO:       streams,
		Reporter: reporter,
	}
}

func diagnosticOf(t *testing.T, err error) core.Diagnostic {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var ce *core.Error
	if !errors.As(err, &ce) || len(ce.Diagnostics) == 0 {
		t.Fatalf("error = %v", err)
	}
	return ce.Diagnostics[0]
}

func TestExecDescriptorRequiresAServiceAndCommandAndAttaches(t *testing.T) {
	f := New(&fakeBackend{}, &fakeStreamer{})
	d := f.Descriptor()
	if d.Name != "exec" || !d.ReadOnly || !d.SupportsServices || !d.AcceptsArgs {
		t.Fatalf("descriptor = %+v", d)
	}
	if f.LiveMode() != core.LiveAttach {
		t.Fatalf("live mode = %q", f.LiveMode())
	}
	flags := map[string]string{}
	for _, fl := range d.Flags {
		flags[fl.Name] = fl.Shorthand
	}
	if flags["index"] != "i" || flags["no-tty"] != "T" {
		t.Fatalf("flags = %+v", flags)
	}
}

func TestExecRequiresATargetServiceAndCommand(t *testing.T) {
	streamer := &fakeStreamer{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	cases := []struct {
		name     string
		services []string
		args     []string
	}{
		{name: "no service", services: nil, args: []string{"date"}},
		{name: "two services", services: []string{"web", "db"}, args: []string{"date"}},
		{name: "no command", services: []string{"db"}, args: nil},
		{name: "empty command", services: []string{"db"}, args: []string{"  "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.Run(context.Background(), invocation(tc.services, tc.args, nil, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{}))
			d := diagnosticOf(t, err)
			if d.Category != core.CategorySelectionRequired {
				t.Fatalf("category = %q, message = %q", d.Category, d.Message)
			}
			if len(d.NextActions) == 0 {
				t.Fatal("no Korean next actions")
			}
		})
	}
	if streamer.callCount() != 0 {
		t.Fatal("exec attached despite missing input")
	}
}

func TestExecNeverPicksTheFirstInstanceOfAScaledService(t *testing.T) {
	streamer := &fakeStreamer{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	_, err := f.Run(context.Background(), invocation([]string{"web"}, []string{"date"}, nil, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{}))
	d := diagnosticOf(t, err)
	if d.Category != core.CategorySelectionRequired {
		t.Fatalf("category = %q", d.Category)
	}
	for _, want := range []string{"#0", "p-web-1", "#1", "p-web-2"} {
		if !strings.Contains(d.Detail, want) {
			t.Fatalf("detail missing %q: %q", want, d.Detail)
		}
	}
	if streamer.callCount() != 0 {
		t.Fatal("exec picked an instance without asking")
	}
}

func TestExecRejectsAStoppedServiceWithExitOne(t *testing.T) {
	streamer := &fakeStreamer{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	_, err := f.Run(context.Background(), invocation([]string{"job"}, []string{"date"}, nil, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{}))
	d := diagnosticOf(t, err)
	if d.Category != core.CategoryInstanceStopped {
		t.Fatalf("category = %q", d.Category)
	}
	if !strings.Contains(d.Message, "실행 중이 아님") || len(d.NextActions) == 0 {
		t.Fatalf("diagnostic = %+v", d)
	}
	if streamer.callCount() != 0 {
		t.Fatal("exec attached to a stopped service")
	}
}

func TestExecRejectsAnOutOfRangeInstance(t *testing.T) {
	streamer := &fakeStreamer{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	_, err := f.Run(context.Background(), invocation([]string{"db"}, []string{"date"}, map[string]string{"index": "3"}, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{}))
	d := diagnosticOf(t, err)
	if d.Category != core.CategoryInstanceUnknown {
		t.Fatalf("category = %q", d.Category)
	}
	if streamer.callCount() != 0 {
		t.Fatal("exec attached to an unknown instance")
	}
}

func TestExecPassesTheExactArgvWithoutAShell(t *testing.T) {
	streamer := &fakeStreamer{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	command := []string{"printf", "%s", "hi there", "&&", "echo", "$HOME"}
	payload, err := f.Run(context.Background(), invocation([]string{"db"}, command, nil, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	call := streamer.last(t)
	if strings.Join(call.req.Command, "|") != strings.Join(command, "|") {
		t.Fatalf("command = %#v", call.req.Command)
	}
	if call.req.Service != "db" || call.req.Index != -1 {
		t.Fatalf("single-instance target = %+v", call.req)
	}
	report := payload.(Payload).Exec
	if report.Service != "db" || report.Instance != "p-db-1" || report.ExitCode != 0 {
		t.Fatalf("payload = %+v", report)
	}
}

func TestExecTTYOnlyWhenTheCallerIsATTYAndNoTTYIsNotSet(t *testing.T) {
	cases := []struct {
		name    string
		io      core.IO
		options map[string]string
		want    bool
	}{
		{name: "tty", io: core.IO{TTY: true}, want: true},
		{name: "no tty flag", io: core.IO{TTY: true}, options: map[string]string{"no-tty": "true"}, want: false},
		{name: "caller not a tty", io: core.IO{TTY: false}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			streamer := &fakeStreamer{}
			f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
			streams := tc.io
			streams.Stdout = &strings.Builder{}
			if _, err := f.Run(context.Background(), invocation([]string{"db"}, []string{"date"}, tc.options, streams, &fakeReporter{})); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := streamer.last(t).req.TTY; got != tc.want {
				t.Fatalf("TTY = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExecPropagatesANonZeroRemoteExitAsAFailure(t *testing.T) {
	streamer := &fakeStreamer{result: stack.ExecResult{ExitCode: 7, Output: "boom"}}
	reporter := &fakeReporter{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	_, err := f.Run(context.Background(), invocation([]string{"db"}, []string{"false"}, nil, core.IO{Stdout: &strings.Builder{}}, reporter))
	d := diagnosticOf(t, err)
	if d.Category != core.CategoryExecFailed {
		t.Fatalf("category = %q", d.Category)
	}
	if st, ok := reporter.find(stageAttach); !ok || st.Status != core.StageFailed {
		t.Fatalf("attach stage = %+v", st)
	}
	if d.ExitCode == nil || *d.ExitCode != 7 {
		t.Fatalf("remote exit code = %v", d.ExitCode)
	}
	if !strings.Contains(d.Message, "7") || !strings.Contains(d.Detail, "boom") {
		t.Fatalf("diagnostic = %+v", d)
	}
	res := core.Result{Outcome: core.OutcomeFailure, Diagnostics: []core.Diagnostic{d}}
	if res.ExitCode() != 1 {
		t.Fatalf("dcraft exit = %d, want 1", res.ExitCode())
	}
}

func TestExecExplainsAMissingCommandWithoutAssumingAShell(t *testing.T) {
	streamer := &fakeStreamer{result: stack.ExecResult{
		ExitCode: 127,
		Output:   `OCI runtime exec failed: exec: "nosuch": executable file not found in $PATH`,
	}}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	_, err := f.Run(context.Background(), invocation([]string{"db"}, []string{"nosuch"}, nil, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{}))
	d := diagnosticOf(t, err)
	if d.Category != core.CategoryExecFailed {
		t.Fatalf("category = %q", d.Category)
	}
	if !strings.Contains(d.Message, "nosuch") || !strings.Contains(d.Message, "없음") {
		t.Fatalf("message = %q", d.Message)
	}
	joined := strings.Join(d.NextActions, " ")
	if !strings.Contains(joined, "이미지") || !strings.Contains(joined, "셸") {
		t.Fatalf("next actions = %+v", d.NextActions)
	}
}

func TestExecExplainsAServiceThatStoppedAfterSelection(t *testing.T) {
	streamer := &fakeStreamer{result: stack.ExecResult{ExitCode: 1, Output: `service "db" is not running`}}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	_, err := f.Run(context.Background(), invocation([]string{"db"}, []string{"date"}, nil, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{}))
	d := diagnosticOf(t, err)
	if d.Category != core.CategoryExecFailed || !strings.Contains(d.Message, "실행 중이 아님") {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestExecJSONCapturesOutputAndNeverAttachesATTY(t *testing.T) {
	streamer := &fakeStreamer{out: "captured-out\n", errOut: "captured-err\n"}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	outer := &strings.Builder{}
	in := invocation([]string{"db"}, []string{"date"}, nil, core.IO{Stdout: outer, TTY: true}, &fakeReporter{})
	in.JSON = true
	payload, err := f.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	call := streamer.last(t)
	if call.req.TTY || call.streams.TTY {
		t.Fatal("JSON mode attached an interactive terminal")
	}
	report := payload.(Payload).Exec
	if report.Stdout != "captured-out\n" || report.Stderr != "captured-err\n" {
		t.Fatalf("payload = %+v", report)
	}
	if outer.String() != "" {
		t.Fatalf("raw command output leaked outside the JSON result: %q", outer.String())
	}
}

func TestExecCancellationRestoresTheTerminalAndDetaches(t *testing.T) {
	streamer := &fakeStreamer{block: true}
	ctx, cancel := context.WithCancel(context.Background())
	reporter := &fakeReporter{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	done := make(chan error, 1)
	go func() {
		_, err := f.Run(ctx, invocation([]string{"db"}, []string{"sleep", "30"}, nil, core.IO{Stdout: &strings.Builder{}}, reporter))
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
		t.Fatal("exec did not stop after cancellation")
	}
	st, ok := reporter.find(stageAttach)
	if !ok || st.Status != core.StageCanceled {
		t.Fatalf("attach stage = %+v", st)
	}
	if !strings.Contains(st.Detail, "복원") || !strings.Contains(st.Detail, "계속 실행") {
		t.Fatalf("detail = %q", st.Detail)
	}
	d := f.CanceledDiagnostic()
	if !strings.Contains(d.Message, "복원") || !strings.Contains(d.Message, "계속 실행") {
		t.Fatalf("canceled diagnostic = %+v", d)
	}
}

func TestExecInputsOfferTheDefaultShellAndParseTheCommandWithoutAShell(t *testing.T) {
	f := New(&fakeBackend{snap: scaledSnapshot()}, &fakeStreamer{})
	fields, err := f.Inputs(context.Background(), invocation(nil, nil, nil, core.IO{}, &fakeReporter{}))
	if err != nil {
		t.Fatalf("Inputs: %v", err)
	}
	if len(fields) != 3 || fields[0].Key != "instance" || fields[1].Key != "command-choice" || fields[2].Key != "command" {
		t.Fatalf("fields = %+v", fields)
	}
	var values []string
	for _, opt := range fields[0].Options {
		values = append(values, opt.Value)
	}
	if strings.Join(values, ",") != "db|0,web|0,web|1" {
		t.Fatalf("instance options = %v", values)
	}
	// The first two rows are the pinned shells: bash is the default.
	var choices []string
	for _, opt := range fields[1].Options {
		choices = append(choices, opt.Value)
	}
	if strings.Join(choices, ",") != "/bin/bash,/bin/sh," {
		t.Fatalf("command choices = %v", choices)
	}

	// A stored choice runs without asking for a typed command.
	req, err := f.WithInputs(core.Request{Feature: "exec"}, map[string]string{
		"instance": "web|1", "command-choice": "/bin/bash", "command": "",
	})
	if err != nil {
		t.Fatalf("WithInputs: %v", err)
	}
	if strings.Join(req.Services, ",") != "web" || req.Options["index"] != "1" {
		t.Fatalf("request target = %+v", req)
	}
	if strings.Join(req.Args, "|") != "/bin/bash" {
		t.Fatalf("command = %#v", req.Args)
	}

	// The new-command row reads the typed field with the same quoting rules.
	req, err = f.WithInputs(core.Request{Feature: "exec"}, map[string]string{
		"instance": "web|1", "command-choice": "",
		"command": `printf '%s' "a b"`,
	})
	if err != nil {
		t.Fatalf("WithInputs: %v", err)
	}
	if strings.Join(req.Args, "|") != `printf|%s|a b` {
		t.Fatalf("command = %#v", req.Args)
	}
}

func TestExecInputsFailWhenNothingIsRunning(t *testing.T) {
	snap := stack.Snapshot{Services: []stack.Service{{
		Name: "job", Configured: true, Instances: []stack.Instance{{Name: "p-job-1", State: "exited"}},
	}}}
	f := New(&fakeBackend{snap: snap}, &fakeStreamer{})
	_, err := f.Inputs(context.Background(), invocation(nil, nil, nil, core.IO{}, &fakeReporter{}))
	d := diagnosticOf(t, err)
	if d.Category != core.CategoryInstanceStopped {
		t.Fatalf("category = %q", d.Category)
	}
}

// The document declares the service and the command so the TUI can paint them;
// the CLI text is the same line body.
func TestExecDocumentTagsServiceAndCommand(t *testing.T) {
	f := New(&fakeBackend{}, &fakeStreamer{})
	payload := Payload{Exec: Report{
		Service: "web", Instance: "p-web-2", Index: 1,
		Command: []string{"printf", "hi"}, ExitCode: 0,
	}}
	res := core.Result{Outcome: core.OutcomeSuccess, Data: payload}
	doc := f.Document(res)
	if len(doc.Blocks) != 1 || doc.Blocks[0].Kind != core.BlockValueLines {
		t.Fatalf("document = %+v", doc)
	}
	kinds := map[core.ValueKind]int{}
	var text []string
	for _, line := range doc.Blocks[0].Lines {
		text = append(text, line.Text())
		for _, run := range line {
			kinds[run.Kind]++
		}
	}
	if kinds[core.ValueService] != 1 || kinds[core.ValueCommand] != 1 {
		t.Fatalf("kinds = %v, want one service and one command", kinds)
	}
	if got, want := strings.Join(text, "\n"), f.Present(res, 0); got != want {
		t.Fatalf("document text = %q, want presenter text %q", got, want)
	}
}

func TestExecPresenterShowsTheResolvedTarget(t *testing.T) {
	f := New(&fakeBackend{}, &fakeStreamer{})
	payload := Payload{Exec: Report{
		Service: "web", Instance: "p-web-2", Index: 1,
		Command: []string{"printf", "hi"}, TTY: true, ExitCode: 0,
	}}
	text := f.Present(core.Result{Outcome: core.OutcomeSuccess, Data: payload}, 0)
	for _, want := range []string{"web", "p-web-2", "printf hi", "종료 코드"} {
		if !strings.Contains(text, want) {
			t.Fatalf("presenter missing %q:\n%s", want, text)
		}
	}
}

// The recent-command store lives inside the project, keeps five newest
// de-duplicated entries, and can remove one.
func TestHistoryKeepsFiveNewestDeDuplicatedCommands(t *testing.T) {
	project := t.TempDir()
	history, err := LoadHistory(project, true)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !history.created {
		t.Fatal("first creation was not reported, so the ignore guidance never prints")
	}
	if guidance := history.IgnoreGuidance(); !strings.Contains(guidance, ".dcraft/") {
		t.Fatalf("ignore guidance = %q", guidance)
	}
	for i := 0; i < 7; i++ {
		if err := history.Remember([]string{"echo", fmt.Sprintf("%d", i)}); err != nil {
			t.Fatalf("remember %d: %v", i, err)
		}
	}
	if len(history.Commands) != 5 {
		t.Fatalf("commands = %v, want five", history.Commands)
	}
	if history.Commands[0] != "echo 6" || history.Commands[4] != "echo 2" {
		t.Fatalf("commands = %v, want newest first", history.Commands)
	}
	// Re-running a stored command moves it to the front instead of adding it.
	if err := history.Remember([]string{"echo", "2"}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if len(history.Commands) != 5 || history.Commands[0] != "echo 2" {
		t.Fatalf("commands = %v", history.Commands)
	}
	if err := history.Delete("echo 2"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, command := range history.Commands {
		if command == "echo 2" {
			t.Fatalf("delete kept the command: %v", history.Commands)
		}
	}
	// The store is a file inside the project, and it survives a reload.
	path := HistoryPath(project)
	if !strings.HasPrefix(path, project) {
		t.Fatalf("history path = %q, want it inside %q", path, project)
	}
	reloaded, err := LoadHistory(project, false)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if strings.Join(reloaded.Commands, ",") != strings.Join(history.Commands, ",") {
		t.Fatalf("reload = %v, want %v", reloaded.Commands, history.Commands)
	}
}

// A store that cannot be read is reported, not silently treated as empty: the
// reader's commands would otherwise disappear without a word.
func TestHistoryReportsACorruptStore(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(HistoryPath(project)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(HistoryPath(project), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHistory(project, false); err == nil {
		t.Fatal("corrupt store read as empty")
	}
}

// The attach screen offers the two shells, then this project's recent commands,
// then the new-command row.
func TestCommandChoicesOrderPinnedHistoryAndNewRow(t *testing.T) {
	history := History{Commands: []string{"/bin/bash", "date", "psql -U postgres"}}
	var values []string
	for _, option := range commandChoices(history) {
		values = append(values, option.Value)
	}
	want := "/bin/bash,/bin/sh,/bin/bash,date,psql -U postgres,"
	if strings.Join(values, ",") != want {
		t.Fatalf("choices = %v, want %v", values, want)
	}
}

// A successful attach records what ran, so the next screen offers it.
func TestExecRecordsTheCommandThatRan(t *testing.T) {
	project := t.TempDir()
	f := New(&fakeBackend{snap: scaledSnapshot()}, &fakeStreamer{})
	in := invocation([]string{"web"}, []string{"date"}, map[string]string{"index": "1"}, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{})
	in.Target.ProjectDir = project
	if _, err := f.Run(context.Background(), in); err != nil {
		t.Fatalf("run: %v", err)
	}
	history, err := LoadHistory(project, false)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(history.Commands) != 1 || history.Commands[0] != "date" {
		t.Fatalf("history = %v", history.Commands)
	}
}

// An image without bash still gets a shell: the one retry runs /bin/sh and the
// result says the command was substituted.
func TestExecRetriesWithShWhenBashIsMissing(t *testing.T) {
	project := t.TempDir()
	streamer := &fakeStreamer{
		result: stack.ExecResult{ExitCode: 127, Output: "exec: \"/bin/bash\": executable file not found in $PATH"},
	}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	in := invocation([]string{"web"}, []string{"/bin/bash"}, map[string]string{"index": "1"}, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{})
	in.Target.ProjectDir = project
	// The fake streamer answers every call the same way, so the retry is
	// observed through the recorded requests and the substituted flag.
	_, err := f.Run(context.Background(), in)
	if err == nil {
		t.Fatal("the retry result was reported as success")
	}
	calls := streamer.recorded()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want the one retry", len(calls))
	}
	if strings.Join(calls[1].req.Command, " ") != "/bin/sh" {
		t.Fatalf("retry command = %v", calls[1].req.Command)
	}
}

// The retry stays the safety net behind the probe: when bash disappears between
// the probe and the attach, the run still ends in sh rather than failing.
func TestExecRetriesWhenBashDisappearsAfterTheProbe(t *testing.T) {
	streamer := &fakeStreamer{sequence: []fakeAnswer{
		{result: stack.ExecResult{ExitCode: 0}}, // probe: bash exists
		{result: stack.ExecResult{ExitCode: 127, Output: `OCI runtime exec failed: exec: "/bin/bash": stat /bin/bash: no such file or directory`}},
		{}, // the retry runs sh
	}}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	payload, err := f.Run(context.Background(), ttyInvocation([]string{"/bin/bash"}, &fakeReporter{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := streamer.recorded()
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want the probe, the attach, and the retry", len(calls))
	}
	if got := strings.Join(calls[2].req.Command, " "); got != "/bin/sh" {
		t.Fatalf("retry command = %q", got)
	}
	report := payload.(Payload).Exec
	if !report.Substituted || strings.Join(report.Command, " ") != "/bin/sh" {
		t.Fatalf("payload = %+v, want the sh run reported", report)
	}
}

// The retry happens only for a missing command, never for a real failure.
func TestSubstitutionRetryIsNarrow(t *testing.T) {
	if _, ok := substitutionRetry([]string{"psql"}, "psql: error"); ok {
		t.Fatal("retried a non-shell command")
	}
	if _, ok := substitutionRetry([]string{"/bin/bash"}, "some other failure"); ok {
		t.Fatal("retried a bash failure that was not a missing command")
	}
	if got, ok := substitutionRetry([]string{"/bin/bash", "-c", "echo hi"}, "no such file or directory"); !ok {
		t.Fatal("missing-command bash was not retried")
	} else if strings.Join(got, " ") != "/bin/sh -c echo hi" {
		t.Fatalf("retry = %v", got)
	}
}

// ttyInvocation is an interactive attach: the caller relays a terminal and -T
// is absent, which is what turns the pre-attach probe on.
func ttyInvocation(args []string, reporter core.Reporter) core.Invocation {
	in := invocation([]string{"db"}, args, nil, core.IO{Stdout: &strings.Builder{}, TTY: true}, reporter)
	in.Target.ProjectDir = "/p"
	return in
}

// An interactive attach with bash asks the target before running the shell, so
// an image without bash lands in sh without printing the OCI error first.
func TestExecTTYBashProbeAbsentRunsShFromTheStart(t *testing.T) {
	streamer := &fakeStreamer{sequence: []fakeAnswer{
		{result: stack.ExecResult{ExitCode: 127}}, // probe: the runtime cannot start bash
		{}, // attach: sh starts
	}}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	payload, err := f.Run(context.Background(), ttyInvocation([]string{"/bin/bash"}, &fakeReporter{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := streamer.recorded()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want the probe and the attach", len(calls))
	}
	// The probe asks the exact argv0 the reader chose, so a bash on PATH is
	// not mistaken for a missing one and a bare name is not assumed present.
	if got := strings.Join(calls[0].req.Command, " "); got != "/bin/bash --version" {
		t.Fatalf("probe command = %q", got)
	}
	if calls[0].req.TTY {
		t.Fatal("the probe allocated a terminal")
	}
	if calls[0].streams.Stdout != io.Discard || calls[0].streams.Stderr != io.Discard {
		t.Fatal("the probe's output could reach the terminal")
	}
	if got := strings.Join(calls[1].req.Command, " "); got != "/bin/sh" {
		t.Fatalf("attach command = %q, want /bin/sh", got)
	}
	if !calls[1].req.TTY {
		t.Fatal("the attach did not allocate a terminal")
	}
	report := payload.(Payload).Exec
	if !report.Substituted {
		t.Fatal("the substitution was not reported")
	}
	if strings.Join(report.Command, " ") != "/bin/sh" {
		t.Fatalf("reported command = %v, want the argv that ran", report.Command)
	}
	if !containsSubstitutionNotice(report.Notices) {
		t.Fatalf("notices = %v, want the substitution notice", report.Notices)
	}
}

// A bare `bash` is probed by its own name, so the answer belongs to the argv the
// reader will actually run. An image whose bash lives outside the pinned paths
// (or that resolves through PATH) must not be downgraded to sh.
func TestExecTTYProbeAsksTheRequestedArgv0(t *testing.T) {
	streamer := &fakeStreamer{sequence: []fakeAnswer{
		{result: stack.ExecResult{ExitCode: 0}}, // probe: bare bash starts
		{},                                      // attach
	}}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	payload, err := f.Run(context.Background(), ttyInvocation([]string{"bash", "-l"}, &fakeReporter{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := streamer.recorded()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want the probe and the attach", len(calls))
	}
	if got := strings.Join(calls[0].req.Command, " "); got != "bash --version" {
		t.Fatalf("probe command = %q, want the bare name the reader typed", got)
	}
	if got := strings.Join(calls[1].req.Command, " "); got != "bash -l" {
		t.Fatalf("attach command = %q", got)
	}
	if payload.(Payload).Exec.Substituted {
		t.Fatal("a bash that starts was downgraded to sh")
	}
}

// A probe that finds bash changes nothing: the requested argv runs and no
// substitution is reported.
func TestExecTTYBashProbePresentRunsTheRequestedArgv(t *testing.T) {
	streamer := &fakeStreamer{sequence: []fakeAnswer{
		{result: stack.ExecResult{ExitCode: 0}}, // probe: bash exists
		{},                                      // attach
	}}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	payload, err := f.Run(context.Background(), ttyInvocation([]string{"/bin/bash", "-l"}, &fakeReporter{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := streamer.recorded()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want the probe and the attach", len(calls))
	}
	if got := strings.Join(calls[1].req.Command, " "); got != "/bin/bash -l" {
		t.Fatalf("attach command = %q, want the requested argv", got)
	}
	report := payload.(Payload).Exec
	if report.Substituted {
		t.Fatal("a present bash was reported as substituted")
	}
	if strings.Join(report.Command, " ") != "/bin/bash -l" {
		t.Fatalf("reported command = %v", report.Command)
	}
	if containsSubstitutionNotice(report.Notices) {
		t.Fatalf("notices = %v, want no substitution notice", report.Notices)
	}
}

// An undecidable probe never fails the run and never changes the argv: the
// existing retry stays the safety net.
func TestExecTTYBashProbeInconclusiveAttachesUnchanged(t *testing.T) {
	cases := []struct {
		name  string
		first fakeAnswer
	}{
		{name: "stream error", first: fakeAnswer{err: errors.New("docker daemon busy")}},
		{name: "exit two", first: fakeAnswer{result: stack.ExecResult{ExitCode: 2}}},
		// bash started and then refused the probe command: the binary exists,
		// so this says nothing about whether the attach can run.
		{name: "bash rejected the probe", first: fakeAnswer{result: stack.ExecResult{ExitCode: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			streamer := &fakeStreamer{sequence: []fakeAnswer{tc.first, {}}}
			f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
			payload, err := f.Run(context.Background(), ttyInvocation([]string{"/bin/bash"}, &fakeReporter{}))
			if err != nil {
				t.Fatalf("an inconclusive probe failed the run: %v", err)
			}
			calls := streamer.recorded()
			if len(calls) != 2 {
				t.Fatalf("calls = %d, want the probe and the attach", len(calls))
			}
			if got := strings.Join(calls[1].req.Command, " "); got != "/bin/bash" {
				t.Fatalf("attach command = %q, want the requested argv", got)
			}
			report := payload.(Payload).Exec
			if report.Substituted {
				t.Fatal("an inconclusive probe reported a substitution")
			}
			if containsSubstitutionNotice(report.Notices) {
				t.Fatalf("notices = %v, want no substitution notice", report.Notices)
			}
		})
	}
}

// The probe belongs to the interactive attach only: a non-interactive or JSON
// run never spends an extra docker exec.
func TestExecNonInteractiveAndJSONRunsNeverProbe(t *testing.T) {
	cases := []struct {
		name    string
		streams core.IO
		options map[string]string
		json    bool
	}{
		{name: "no tty flag", streams: core.IO{TTY: true}, options: map[string]string{"no-tty": "true"}},
		{name: "caller not a tty", streams: core.IO{}},
		{name: "json", streams: core.IO{TTY: true}, json: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			streamer := &fakeStreamer{}
			f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
			streams := tc.streams
			streams.Stdout = &strings.Builder{}
			in := invocation([]string{"db"}, []string{"/bin/bash"}, tc.options, streams, &fakeReporter{})
			in.JSON = tc.json
			if _, err := f.Run(context.Background(), in); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := streamer.callCount(); got != 1 {
				t.Fatalf("calls = %d, want exactly the attach", got)
			}
		})
	}
}

// Only bash is guessed for. Any other command runs as typed on the first and
// only call.
func TestExecTTYNonBashCommandsNeverProbe(t *testing.T) {
	for _, command := range [][]string{{"date"}, {"/bin/sh"}, {"sh"}} {
		streamer := &fakeStreamer{}
		f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
		if _, err := f.Run(context.Background(), ttyInvocation(command, &fakeReporter{})); err != nil {
			t.Fatalf("run %v: %v", command, err)
		}
		calls := streamer.recorded()
		if len(calls) != 1 {
			t.Fatalf("%v: calls = %d, want no probe", command, len(calls))
		}
		if got := strings.Join(calls[0].req.Command, " "); got != strings.Join(command, " ") {
			t.Fatalf("%v: attach command = %q", command, got)
		}
	}
}

// A canceled probe ends the run: the reader already left, so nothing attaches
// behind their back.
func TestExecCanceledBashProbeCancelsTheRun(t *testing.T) {
	streamer := &fakeStreamer{sequence: []fakeAnswer{{err: context.Canceled}}}
	reporter := &fakeReporter{}
	f := New(&fakeBackend{snap: scaledSnapshot()}, streamer)
	_, err := f.Run(context.Background(), ttyInvocation([]string{"/bin/bash"}, reporter))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want a canceled run", err)
	}
	if got := streamer.callCount(); got != 1 {
		t.Fatalf("calls = %d, want the canceled probe only", got)
	}
	st, ok := reporter.find(stageAttach)
	if !ok || st.Status != core.StageCanceled {
		t.Fatalf("attach stage = %+v", st)
	}
}

// containsSubstitutionNotice reports whether the run stated the sh fallback.
func containsSubstitutionNotice(notices []string) bool {
	for _, notice := range notices {
		if strings.Contains(notice, "/bin/sh") && strings.Contains(notice, "대체") {
			return true
		}
	}
	return false
}

// A command that looks like it carries a credential still runs but is never
// written to the project store.
func TestExecDoesNotStoreCommandsThatLookLikeCredentials(t *testing.T) {
	project := t.TempDir()
	f := New(&fakeBackend{snap: scaledSnapshot()}, &fakeStreamer{})
	in := invocation([]string{"web"}, []string{"psql", "postgres://user:password=secret@db/app"},
		map[string]string{"index": "1"}, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{})
	in.Target.ProjectDir = project
	if _, err := f.Run(context.Background(), in); err != nil {
		t.Fatalf("run: %v", err)
	}
	history, err := LoadHistory(project, false)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(history.Commands) != 0 {
		t.Fatalf("a credential-looking command was stored: %v", history.Commands)
	}
}

// Opening the screen leaves the project untouched; the file appears on the run
// that records a command, and that run states the ignore rule.
func TestExecScreenCreatesNothingAndTheFirstRunStatesTheIgnoreRule(t *testing.T) {
	project := t.TempDir()
	f := New(&fakeBackend{snap: scaledSnapshot()}, &fakeStreamer{})
	in := invocation(nil, nil, nil, core.IO{}, &fakeReporter{})
	in.Target.ProjectDir = project
	if _, err := f.Inputs(context.Background(), in); err != nil {
		t.Fatalf("Inputs: %v", err)
	}
	if _, statErr := os.Stat(HistoryPath(project)); !os.IsNotExist(statErr) {
		t.Fatal("opening the form created the history file")
	}

	in = invocation([]string{"web"}, []string{"date"}, map[string]string{"index": "1"}, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{})
	in.Target.ProjectDir = project
	data, err := f.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	notices := data.(Payload).Exec.Notices
	if len(notices) != 1 || !strings.Contains(notices[0], ".dcraft/") {
		t.Fatalf("notices = %v, want the ignore rule once", notices)
	}
}

// `dcraft exec --forget <명령>` removes one remembered command and attaches
// nothing. Its help text points here, so the promise is real.
func TestExecForgetsOneRememberedCommand(t *testing.T) {
	project := t.TempDir()
	history, err := LoadHistory(project, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"date", "psql -U postgres"} {
		if err := history.Remember(strings.Fields(line)); err != nil {
			t.Fatal(err)
		}
	}
	f := New(&fakeBackend{snap: scaledSnapshot()}, &fakeStreamer{})
	in := invocation(nil, nil, map[string]string{"forget": "date"}, core.IO{}, &fakeReporter{})
	in.Target.ProjectDir = project
	data, err := f.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if notices := data.(Payload).Exec.Notices; len(notices) != 1 || !strings.Contains(notices[0], "date") {
		t.Fatalf("notices = %v", notices)
	}
	after, err := LoadHistory(project, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Commands) != 1 || after.Commands[0] != "psql -U postgres" {
		t.Fatalf("history = %v", after.Commands)
	}
}

// The credential warning is conditional: it names the command the reader typed,
// not every command.
func TestExecWarnsOnlyForCommandsThatLookLikeCredentials(t *testing.T) {
	project := t.TempDir()
	f := New(&fakeBackend{snap: scaledSnapshot()}, &fakeStreamer{})
	run := func(args []string) []string {
		in := invocation([]string{"web"}, args, map[string]string{"index": "1"}, core.IO{Stdout: &strings.Builder{}}, &fakeReporter{})
		in.Target.ProjectDir = project
		data, err := f.Run(context.Background(), in)
		if err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
		return data.(Payload).Exec.Notices
	}
	for _, notice := range run([]string{"date"}) {
		if strings.Contains(notice, "비밀값") {
			t.Fatalf("a plain command warned about credentials: %v", notice)
		}
	}
	notices := run([]string{"psql", "postgres://u:password=secret@db/app"})
	if len(notices) != 1 || !strings.Contains(notices[0], "비밀값") {
		t.Fatalf("notices = %v, want the credential warning", notices)
	}
}
