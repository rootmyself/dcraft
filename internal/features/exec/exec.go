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
	Stdout   string   `json:"stdout,omitempty"`
	Stderr   string   `json:"stderr,omitempty"`
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
	return Payload{Exec: report}, nil
}

// Present implements core.Presenter for successful results.
func (f *Feature) Present(res core.Result, width int) string {
	payload, ok := res.Data.(Payload)
	if !ok {
		return msgNoPayload
	}
	report := payload.Exec
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", msgFinished)
	fmt.Fprintf(&b, "%s: %s · %s #%d\n", msgServiceLabel, report.Service, report.Instance, report.Index)
	fmt.Fprintf(&b, "%s: %s\n", msgCommandLabel, strings.Join(report.Command, " "))
	fmt.Fprintf(&b, "%s: %d · %s", msgExitLabel, report.ExitCode, msgServicesRunning)
	return strings.TrimRight(b.String(), "\n")
}

// Inputs implements core.InputProvider: the TUI lists running instances and
// asks for a command line. A CLI run never calls this; it requires -s and the
// argv after `--`.
func (f *Feature) Inputs(ctx context.Context, in core.Invocation) ([]core.InputField, error) {
	if len(in.Services) > 0 && len(in.Args) > 0 {
		return nil, nil
	}
	if f.backend == nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoBackend,
			NextActions: []string{msgNoBackendAction},
		})
	}
	snap, err := f.backend.Inspect(ctx, in.Target)
	if err != nil {
		return nil, err
	}
	var options []core.InputOption
	for _, svc := range selector.Services(snap) {
		for _, inst := range svc.Instances {
			if !inst.Running {
				continue
			}
			options = append(options, core.InputOption{
				Value: fmt.Sprintf("%s|%d", svc.Name, inst.Index),
				Label: fmt.Sprintf("%s · %s (실행 중)", svc.Name, inst.Name),
			})
		}
	}
	if len(options) == 0 {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInstanceStopped,
			Severity:    core.SeverityError,
			Message:     msgNothingRunning,
			NextActions: []string{msgNothingRunningActionOne, msgNothingRunningActionTwo},
		})
	}
	return []core.InputField{
		{
			Key:      fieldInstance,
			Label:    msgFieldInstance,
			Help:     msgFieldInstanceHelp,
			Kind:     core.InputChoice,
			Options:  options,
			Required: true,
		},
		{
			Key:      fieldCommand,
			Label:    msgFieldCommand,
			Help:     msgFieldCommandHelp,
			Kind:     core.InputText,
			Required: true,
		},
	}, nil
}

// WithInputs implements core.InputProvider.
func (f *Feature) WithInputs(req core.Request, values map[string]string) (core.Request, error) {
	target := strings.TrimSpace(values[fieldInstance])
	service, index, ok := strings.Cut(target, "|")
	service = strings.TrimSpace(service)
	if !ok || service == "" {
		return req, core.Fail(core.Diagnostic{
			Category:    core.CategorySelectionRequired,
			Severity:    core.SeverityError,
			Message:     msgPickInstance,
			NextActions: []string{msgPickInstanceAction},
		})
	}
	if _, err := strconv.Atoi(index); err != nil {
		return req, core.Fail(core.Diagnostic{
			Category:    core.CategorySelectionRequired,
			Severity:    core.SeverityError,
			Message:     msgPickInstance,
			NextActions: []string{msgPickInstanceAction},
		})
	}
	argv, err := splitCommand(values[fieldCommand])
	if err != nil {
		return req, core.FailCause(err, core.Diagnostic{
			Category:    core.CategorySelectionRequired,
			Severity:    core.SeverityError,
			Message:     msgInvalidCommand,
			NextActions: []string{msgInvalidCommandAction},
		})
	}
	req.Services = []string{service}
	if req.Options == nil {
		req.Options = map[string]string{}
	}
	req.Options["index"] = index
	req.Args = argv
	return req, nil
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
