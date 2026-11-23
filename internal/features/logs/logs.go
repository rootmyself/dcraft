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
	streamErr := f.streamer.Logs(ctx, in.Target, req, streams)
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
