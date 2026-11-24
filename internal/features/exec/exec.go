// Package exec implements the flat `exec` command: run the user's exact argv
// inside one running container instance. It never assumes a shell, never picks
// the first instance of a scaled service, and always returns the terminal to
// the management screen after an attached command.
package exec

import (
	"bytes"
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
	stageAttach = "attach"
)

// Feature is the registered exec command.
type Feature struct {
	backend  stack.Backend
	streamer stack.Streamer
}

func New(backend stack.Backend, streamer stack.Streamer) *Feature {
	return &Feature{backend: backend, streamer: streamer}
}

// Report is the machine-readable data of a successful exec result. Stdout and
// Stderr are populated only in JSON mode, where the command is captured
// instead of attached to the terminal.
type Report struct {
	Service  string   `json:"service"`
	Instance string   `json:"instance"`
	Index    int      `json:"index"`
	Command  []string `json:"command"`
	TTY      bool     `json:"tty"`
	ExitCode int      `json:"exit_code"`
	// Substituted reports that bash was missing and /bin/sh ran instead.
	Substituted bool   `json:"substituted,omitempty"`
	Stdout      string `json:"stdout,omitempty"`
	Stderr      string `json:"stderr,omitempty"`
}

// Payload is the feature result payload. JSON consumers read data.exec.
type Payload struct {
	Exec Report `json:"exec"`
}

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:             "exec",
		Title:            msgTitle,
		Summary:          msgSummary,
		Usage:            msgUsage,
		Section:          core.SectionInspect,
		ReadOnly:         true,
		SupportsServices: true,
		AcceptsArgs:      true,
		Flags: []core.Flag{
			{Name: "index", Shorthand: "i", Kind: core.FlagString, Usage: msgFlagIndex},
			{Name: "no-tty", Shorthand: "T", Kind: core.FlagBool, Usage: msgFlagNoTTY},
		},
	}
}

// LiveMode implements core.LiveFeature: the command's output and input are
// user data, not a result document.
func (f *Feature) LiveMode() core.LiveMode { return core.LiveAttach }

// CanceledDiagnostic implements core.CanceledReporter. Leaving an attached
// command restores the terminal and never stops the service.
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
	command, err := commandArgv(in.Args)
	if err != nil {
		return nil, err
	}
	index, err := parseIndex(in.Option("index"))
	if err != nil {
		return nil, err
	}

	selection, err := core.RunStageValue(in.Reporter, stageSelect, msgStageSelect, func() (selector.Selection, error) {
		snap, inspectErr := f.backend.Inspect(ctx, in.Target)
		if inspectErr != nil {
			return selector.Selection{}, inspectErr
		}
		return selector.OneRunning(snap, in.Services, index)
	})
	if err != nil {
		return nil, err
	}

	streams := in.IO.WithDefaults()
	attach := streams.TTY && !in.BoolOption("no-tty")
	var capturedOut, capturedErr bytes.Buffer
	if in.JSON {
		// A machine-readable result cannot stream user output. Capture it into
		// the payload and never attach an interactive terminal.
		attach = false
		streams.Stdout = &capturedOut
		streams.Stderr = &capturedErr
	}
	streams.TTY = attach

	req := stack.ExecRequest{
		Service: selection.Service.Name,
		Index:   composeIndex(selection),
		Command: command,
		TTY:     attach,
	}
	report := Report{
		Service:  req.Service,
		Instance: selection.Chosen.Name,
		Index:    selection.Chosen.Index,
		Command:  append([]string{}, command...),
		TTY:      attach,
	}

	in.Reporter.Begin(stageAttach, msgStageAttach)
	result, streamErr := f.streamer.Exec(ctx, in.Target, req, streams)
	// An image without bash still gets a shell: one retry with /bin/sh, and the
	// substitution is stated. Any other failure is reported unchanged.
	if substituted, ok := substitutionRetry(req.Command, result.Output); ok && streamErr == nil && result.ExitCode != 0 {
		req.Command = substituted
		report.Command = append([]string{}, substituted...)
		in.Reporter.Begin(stageAttach, msgStageAttach)
		result, streamErr = f.streamer.Exec(ctx, in.Target, req, streams)
		report.Substituted = true
	}
	switch {
	case errors.Is(streamErr, context.Canceled), errors.Is(streamErr, context.DeadlineExceeded), ctx.Err() != nil:
		in.Reporter.End(stageAttach, core.StageCanceled, msgTerminalRestored)
		return nil, ctx.Err()
	case streamErr != nil:
		in.Reporter.End(stageAttach, core.StageFailed, "")
		return nil, execFailure(streamErr)
	case result.ExitCode != 0:
		in.Reporter.End(stageAttach, core.StageFailed, "")
		return nil, remoteFailure(report, result)
	}
	in.Reporter.End(stageAttach, core.StageSucceeded, "")
	if in.JSON {
		report.Stdout = capturedOut.String()
		report.Stderr = capturedErr.String()
	}
	// The store keeps exactly what ran, and only after it ran: a command that
	// failed to start is not offered again. A command that looks like it
	// carries a credential is never stored, even though it still ran.
	if !looksLikeCredential(strings.Join(report.Command, " ")) {
		rememberCommand(in.Target.ProjectDir, report.Command)
	}
	return Payload{Exec: report}, nil
}

// Present implements core.Presenter for successful results.
func (f *Feature) Present(res core.Result, width int) string {
	return core.ValueLinesText(presentLines(res))
}

// Document implements core.ViewPresenter. The service and the command are
// declared kinds, so the TUI can paint them while the CLI text stays byte for
// byte the same.
func (f *Feature) Document(res core.Result) core.Document {
	return core.Document{Blocks: []core.Block{{Kind: core.BlockValueLines, Lines: presentLines(res)}}}
}

func presentLines(res core.Result) []core.ValueLine {
	payload, ok := res.Data.(Payload)
	if !ok {
		return []core.ValueLine{{{Text: msgNoPayload}}}
	}
	report := payload.Exec
	finished := msgFinished
	if report.Substituted {
		finished += " " + msgSubstituted
	}
	return []core.ValueLine{
		{{Text: finished}},
		{
			{Text: msgServiceLabel + ": "},
			{Text: report.Service, Kind: core.ValueService},
			{Text: fmt.Sprintf(" · %s #%d", report.Instance, report.Index)},
		},
		{
			{Text: msgCommandLabel + ": "},
			{Text: strings.Join(report.Command, " "), Kind: core.ValueCommand},
		},
		{{Text: fmt.Sprintf("%s: %d · %s", msgExitLabel, report.ExitCode, msgServicesRunning)}},
	}
}

// composeIndex returns the Compose --index value for the selected service. A
// single-instance service needs no index; a scaled service uses the chosen
// instance so a stopped first container cannot be mistaken for the target.
func composeIndex(selection selector.Selection) int {
	if len(selection.Instances) < 2 || selection.Chosen == nil {
		return -1
	}
	return selection.Chosen.Index
}

func commandArgv(args []string) ([]string, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategorySelectionRequired,
			Severity:    core.SeverityError,
			Message:     msgCommandRequired,
			NextActions: []string{msgCommandRequiredActionOne, msgCommandRequiredActionTwo},
		})
	}
	return append([]string{}, args...), nil
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

// execFailure is a boundary/start failure before the command ran.
func execFailure(err error) error {
	cause := err
	var dockerErr *core.Error
	if errors.As(err, &dockerErr) {
		return err
	}
	return core.FailCause(cause, core.Diagnostic{
		Category:    core.CategoryExecFailed,
		Severity:    core.SeverityError,
		Message:     msgNotStarted,
		Detail:      detailOf(err),
		NextActions: []string{msgNotStartedActionOne, msgNotStartedActionTwo},
	})
}

// remoteFailure explains a completed command with a non-zero status. dcraft's
// own exit stays 1 (operation failure); the remote code is carried in the
// diagnostic's machine-readable exit_code field so a tool exit 2 is never
// confused with dcraft's usage exit 2.
func remoteFailure(report Report, result stack.ExecResult) error {
	code := result.ExitCode
	message := fmt.Sprintf(msgRemoteFailed, code)
	if looksLikeMissingCommand(result.Output) {
		message = fmt.Sprintf(msgCommandMissing, report.Command[0])
	}
	if looksLikeNotRunning(result.Output) {
		message = fmt.Sprintf(msgServiceStopped, report.Service)
	}
	diag := core.Diagnostic{
		Category:    core.CategoryExecFailed,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      truncate(result.Output, 800),
		NextActions: []string{msgRemoteFailedActionOne, msgRemoteFailedActionTwo},
		ExitCode:    &code,
	}
	if looksLikeMissingCommand(result.Output) {
		diag.NextActions = []string{msgCommandMissingActionOne, msgCommandMissingActionTwo}
	}
	if looksLikeNotRunning(result.Output) {
		diag.NextActions = []string{msgServiceStoppedActionOne, msgServiceStoppedActionTwo}
	}
	return core.Fail(diag)
}

func looksLikeMissingCommand(output string) bool {
	lower := strings.ToLower(output)
	for _, needle := range []string{
		"executable file not found",
		"not found in $path",
		"no such file or directory",
		`exec: "`,
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func looksLikeNotRunning(output string) bool {
	return strings.Contains(strings.ToLower(output), "is not running")
}

func detailOf(err error) string {
	var cmdErr *stack.CommandError
	if errors.As(err, &cmdErr) {
		return cmdErr.Output
	}
	return err.Error()
}

func truncate(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return text[:max] + "…"
}
