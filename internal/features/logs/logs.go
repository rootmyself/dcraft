// Package logs implements the flat `logs` command: follow the stack or an
// explicit Compose service, optionally one container instance. It is read-only
// and uses the shared service/instance selector and live-output boundary.
package logs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/selector"
	"github.com/rootmyself/dcraft/internal/stack"
)

// Stable stage names published for both interfaces and machine consumers.
const (
	stageSelect = "select_instance"
	stageStream = "stream"
)

// Feature is the registered logs command.
type Feature struct {
	backend  stack.Backend
	streamer stack.Streamer
}

func New(backend stack.Backend, streamer stack.Streamer) *Feature {
	return &Feature{backend: backend, streamer: streamer}
}

// Report is the machine-readable data of a successful logs result. Index is
// zero-based; -1 means every instance of the selected services.
type Report struct {
	Follow     bool     `json:"follow"`
	Services   []string `json:"services"`
	Instance   string   `json:"instance,omitempty"`
	Index      int      `json:"index"`
	Tail       string   `json:"tail,omitempty"`
	Since      string   `json:"since,omitempty"`
	Timestamps bool     `json:"timestamps"`
}

// Payload is the feature result payload. JSON consumers read data.logs.
type Payload struct {
	Logs Report `json:"logs"`
}

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:             "logs",
		Title:            msgTitle,
		Summary:          msgSummary,
		Usage:            msgUsage,
		Section:          core.SectionInspect,
		ReadOnly:         true,
		SupportsServices: true,
		Menu: []core.MenuEntry{
			{Kind: core.MenuRun, Feature: "logs", Label: msgTitle, Summary: msgSummary, Order: 2},
		},
		Flags: []core.Flag{
			{Name: "no-follow", Kind: core.FlagBool, Usage: msgFlagNoFollow},
			{Name: "tail", Kind: core.FlagString, Usage: msgFlagTail},
			{Name: "since", Kind: core.FlagString, Usage: msgFlagSince},
			{Name: "timestamps", Kind: core.FlagBool, Usage: msgFlagTimestamps},
			{Name: "index", Shorthand: "i", Kind: core.FlagString, Usage: msgFlagIndex},
		},
	}
}

// LiveMode implements core.LiveFeature: stdout is user log data, not a result.
func (f *Feature) LiveMode() core.LiveMode { return core.LiveStream }

// RequiresServiceScope implements core.ServiceScope. A log line belongs to a
// service, so a whole-stack stream cannot be named on the screen or compared
// across tabs.
func (f *Feature) RequiresServiceScope() bool { return true }

// MaxServices implements core.ServiceScope. The interactive screen shows one
// tab per service; past four the tabs stop being readable. The CLI is not
// limited by this, because it writes one stream and no tabs.
func (f *Feature) MaxServices() int { return 4 }

// CanceledDiagnostic implements core.CanceledReporter. Stopping a follow
// detaches the stream; it never stops a service.
func (f *Feature) CanceledDiagnostic() core.Diagnostic {
	return core.Diagnostic{
		Category:    core.CategoryCanceled,
		Severity:    core.SeverityWarning,
		Message:     msgCanceled,
		NextActions: []string{msgCanceledActionOne, msgCanceledActionTwo},
	}
}

// Run implements core.Feature.
func (f *Feature) Run(ctx context.Context, in core.Invocation) (any, error) {
	if f.backend == nil || f.streamer == nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoBackend,
			NextActions: []string{msgNoBackendAction},
		})
	}
	index, err := parseIndex(in.Option("index"))
	if err != nil {
		return nil, err
	}
	if len(in.Services) == 0 {
		// Logs name services. A whole-stack stream cannot tell a reader which
		// service a line came from, and the interactive screen would draw one
		// tab per service anyway, so the selection is required.
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategorySelectionRequired,
			Severity:    core.SeverityError,
			Message:     msgServiceRequired,
			NextActions: []string{msgServiceRequiredActionOne, msgServiceRequiredActionTwo},
		})
	}
	streams := in.IO.WithDefaults()

	targets, err := core.RunStageValue(in.Reporter, stageSelect, msgStageSelect, func() (selector.LogTargetsResult, error) {
		snap, inspectErr := f.backend.Inspect(ctx, in.Target)
		if inspectErr != nil {
			return selector.LogTargetsResult{}, inspectErr
		}
		return selector.LogTargets(snap, in.Services, index)
	})
	if err != nil {
		return nil, err
	}

	req := stack.LogsRequest{
		Follow:     !in.BoolOption("no-follow"),
		Tail:       strings.TrimSpace(in.Option("tail")),
		Since:      strings.TrimSpace(in.Option("since")),
		Timestamps: in.BoolOption("timestamps"),
		Color:      streams.TTY && !in.JSON,
		Index:      index,
	}
	for _, svc := range targets.Services {
		req.Services = append(req.Services, svc.Name)
	}
	report := Report{
		Follow:     req.Follow,
		Services:   append([]string{}, req.Services...),
		Index:      index,
		Tail:       req.Tail,
		Since:      req.Since,
		Timestamps: req.Timestamps,
	}
	if report.Services == nil {
		report.Services = []string{}
	}
	if targets.Picked != nil && targets.Picked.Chosen != nil {
		report.Instance = targets.Picked.Chosen.Name
	}

	in.Reporter.Begin(stageStream, msgStageStream)
	streamErr := f.stream(ctx, in, req, streams)
	switch {
	case streamErr == nil:
		in.Reporter.End(stageStream, core.StageSucceeded, "")
	case errors.Is(streamErr, context.Canceled), errors.Is(streamErr, context.DeadlineExceeded), ctx.Err() != nil:
		in.Reporter.End(stageStream, core.StageCanceled, msgStreamDetached)
		return nil, ctx.Err()
	default:
		in.Reporter.End(stageStream, core.StageFailed, "")
		return nil, logsFailure(streamErr)
	}
	return Payload{Logs: report}, nil
}

// stream runs the log read. With a fanout the screen watches several services
// at once, so each service gets its own child process and its own stream; the
// first failure is reported but never stops the other services. Without a
// fanout (the CLI) one child covers the whole selection.
func (f *Feature) stream(ctx context.Context, in core.Invocation, req stack.LogsRequest, streams core.IO) error {
	if streams.Fanout == nil || len(req.Services) < 2 {
		return f.streamer.Logs(ctx, in.Target, req, streams)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(req.Services))
	for _, service := range req.Services {
		service := service
		one := req
		one.Services = []string{service}
		out := streams.Fanout.Stream(service)
		out.Width, out.Height = streams.Width, streams.Height
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := f.streamer.Logs(ctx, in.Target, one, out)
			streams.Fanout.StreamDone(service, err)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		// One service's failure is that tab's failure: the others already ran
		// to their own end. The result keeps the first failure, so the screen
		// can report it without hiding the streams that succeeded.
		return err
	}
	return nil
}

// Present implements core.Presenter for successful results.
func (f *Feature) Present(res core.Result, width int) string {
	payload, ok := res.Data.(Payload)
	if !ok {
		return msgNoPayload
	}
	report := payload.Logs
	var b strings.Builder
	b.WriteString(msgFinished)
	target := strings.Join(report.Services, ", ")
	if target == "" {
		target = msgWholeStack
	}
	fmt.Fprintf(&b, "\n%s: %s", msgTargetLabel, target)
	if report.Instance != "" {
		fmt.Fprintf(&b, " · %s #%d", report.Instance, report.Index)
	}
	if !report.Follow {
		fmt.Fprintf(&b, "\n%s", msgNoFollowDone)
	}
	return strings.TrimRight(b.String(), "\n")
}

func parseIndex(raw string) (int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return -1, nil
	}
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 {
		return -1, core.Fail(core.Diagnostic{
			Category:    core.CategorySelectionRequired,
			Severity:    core.SeverityError,
			Message:     fmt.Sprintf(msgInvalidIndex, raw),
			NextActions: []string{msgInvalidIndexActionOne, msgInvalidIndexActionTwo},
		})
	}
	return index, nil
}

func logsFailure(err error) error {
	cause := err
	var cmdErr *stack.CommandError
	if errors.As(err, &cmdErr) {
		cause = nil
	}
	return core.FailCause(cause, core.Diagnostic{
		Category:    core.CategoryLogsFailed,
		Severity:    core.SeverityError,
		Message:     msgFailure,
		Detail:      detailOf(err),
		NextActions: []string{msgFailureActionOne, msgFailureActionTwo},
	})
}

func detailOf(err error) string {
	var cmdErr *stack.CommandError
	if errors.As(err, &cmdErr) {
		return cmdErr.Output
	}
	return err.Error()
}
