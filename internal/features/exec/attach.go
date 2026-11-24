package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/selector"
	"github.com/rootmyself/dcraft/internal/stack"
)

// The attach screen always offers the two shells first, then this project's
// recent commands, then one row to type something new. The pinned shells are
// not deletable: they are the way to get a shell when nothing else works.
const (
	// shellBash is the default command. It is what a reader usually wants.
	shellBash = "/bin/bash"
	// shellSh is the fallback when the image has no bash.
	shellSh = "/bin/sh"
	// commandNew is the row that reveals the text field. It carries no command
	// of its own.
	commandNew = ""
)

// commandChoices is the ordered option list: pinned shells, recent commands,
// then the row that asks for a typed command.
func commandChoices(history History) []core.InputOption {
	options := []core.InputOption{
		{Value: shellBash, Label: shellBash + msgShellDefault},
		{Value: shellSh, Label: shellSh + msgShellFallback},
	}
	for _, command := range history.Commands {
		options = append(options, core.InputOption{Value: command, Label: msgRecentPrefix + command})
	}
	options = append(options, core.InputOption{Value: commandNew, Label: msgNewCommand})
	return options
}

// commandHistory loads this project's store for the attach screen. A store that
// cannot be read is stated on screen; the screen still works.
// commandHistory reads this project's store for the screen. A read-only screen
// creates nothing: the file appears only when a command is actually recorded, so
// opening the form leaves the project untouched. The screen still states the
// ignore rule on the run that creates it, because the recorded result carries
// the history it wrote.
func commandHistory(projectDir string) (History, string) {
	if strings.TrimSpace(projectDir) == "" {
		return History{}, ""
	}
	history, err := LoadHistory(projectDir, false)
	if err != nil {
		return History{ProjectDir: projectDir}, err.Error()
	}
	return history, ""
}

// Inputs offers the command selection too. A read-only inspection still needs a
// running instance first, so the instance list comes from the same snapshot.
func (f *Feature) Inputs(ctx context.Context, in core.Invocation) ([]core.InputField, error) {
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
	history, historyErr := commandHistory(in.Target.ProjectDir)
	commandHelp := msgFieldCommandHelp
	if historyErr != "" {
		commandHelp += "\n" + msgHistoryUnreadable
	}
	fields := []core.InputField{
		{
			Key:      fieldInstance,
			Label:    msgFieldInstance,
			Help:     msgFieldInstanceHelp,
			Kind:     core.InputChoice,
			Options:  options,
			Required: true,
		},
		{
			Key:     fieldCommandChoice,
			Label:   msgFieldCommandChoice,
			Help:    msgFieldCommandChoiceHelp,
			Kind:    core.InputChoice,
			Options: commandChoices(history),
		},
	}
	// The typed command stays visible so a command that is not in the store can
	// still be run. The new-command row is what uses it; selecting a stored
	// command ignores it.
	fields = append(fields, core.InputField{
		Key:   fieldCommand,
		Label: msgFieldCommand,
		Help:  commandHelp,
		Kind:  core.InputText,
	})
	return fields, nil
}

// looksLikeCredential reports whether a typed command appears to carry a value
// that should not be stored. It is a warning, never a block: dcraft cannot know
// what an argument means.
func looksLikeCredential(command string) bool {
	lower := strings.ToLower(command)
	for _, needle := range []string{
		"password=", "passwd=", "token=", "secret=", "api_key=", "apikey=",
		"authorization:", "bearer ", "-p\"", "--password", "AWS_SECRET_ACCESS_KEY",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// WithInputs resolves the chosen row into the argv the run uses. A stored
// command is split with the same rules as a typed one, so both paths behave
// identically.
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
	raw := strings.TrimSpace(values[fieldCommandChoice])
	if raw == commandNew {
		raw = strings.TrimSpace(values[fieldCommand])
	}
	if raw == "" {
		return req, core.Fail(core.Diagnostic{
			Category:    core.CategorySelectionRequired,
			Severity:    core.SeverityError,
			Message:     msgCommandRequired,
			NextActions: []string{msgCommandRequiredActionOne, msgCommandRequiredActionTwo},
		})
	}
	argv, err := splitCommand(raw)
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

// rememberCommand stores what actually ran. It runs after a successful attach,
// so a command that failed to start is not offered again. It is also the only
// writer: opening the screen never creates the file.
func rememberCommand(projectDir string, command []string) RememberOutcome {
	if strings.TrimSpace(projectDir) == "" || len(command) == 0 {
		return RememberOutcome{}
	}
	history, err := trackingHistory(projectDir)
	if err != "" {
		return RememberOutcome{Err: fmt.Errorf("%s", err)}
	}
	out := RememberOutcome{Created: history.created}
	out.Err = history.Remember(command)
	return out
}

// forgetCommand removes one remembered command. The answer names the command so
// the reader sees what left the store.
func forgetCommand(projectDir, command string) (any, error) {
	if strings.TrimSpace(projectDir) == "" {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryTargetInvalid,
			Severity:    core.SeverityError,
			Message:     msgForgetNoProject,
			NextActions: []string{msgForgetNoProjectAction},
		})
	}
	history, err := LoadHistory(projectDir, false)
	if err != nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgHistoryUnreadable,
			NextActions: []string{msgForgetAction},
		})
	}
	if saveErr := history.Delete(command); saveErr != nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgHistoryUnreadable,
			NextActions: []string{msgForgetAction},
		})
	}
	return Payload{Exec: Report{Command: []string{command}, Notices: []string{fmt.Sprintf(msgForgotCommand, command)}}}, nil
}

// bashProbeResult is the pre-attach check's answer. Only an explicit "absent"
// changes the command; everything else keeps the argv the reader asked for.
type bashProbeResult int

const (
	// probeInconclusive means the probe could not decide, so the requested
	// argv runs unchanged and substitutionRetry stays the safety net.
	probeInconclusive bashProbeResult = iota
	// probeBashPresent means the requested bash starts.
	probeBashPresent
	// probeBashAbsent means the requested bash cannot start.
	probeBashAbsent
)

// bashProbeCommand asks the instance the only question that predicts the
// attach: does this exact argv0 start? It uses the argv0 the reader chose
// instead of a fixed list of paths, so a bash on PATH (a distribution that
// keeps it in /usr/local/bin) is not mistaken for an image without bash, and a
// bare `bash` that PATH cannot resolve is not mistaken for one that can.
//
// `--version` is the harmless argument: bash prints and exits 0 without reading
// a startup file, so the probe cannot change container state. dcraft still
// never wraps the reader's argv in `sh -c`.
func bashProbeCommand(command []string) []string {
	return []string{command[0], "--version"}
}

// probeBash asks the instance whether the requested bash starts before an
// interactive attach runs, so an image without it never prints the OCI runtime
// error to the reader's terminal before landing in sh. It goes through the same
// streaming boundary as the attach and discards both outputs, so the probe can
// never write to the terminal and cannot consume the reader's input.
//
// A failed or undecidable probe is not a failure: the attach runs the requested
// argv unchanged. Only a cancellation ends the run, because the reader already
// asked to leave.
func (f *Feature) probeBash(ctx context.Context, target core.Target, req stack.ExecRequest) (bashProbeResult, error) {
	probe := stack.ExecRequest{
		Service: req.Service,
		Index:   req.Index,
		Command: bashProbeCommand(req.Command),
		TTY:     false,
	}
	result, streamErr := f.streamer.Exec(ctx, target, probe, core.IO{Stdout: io.Discard, Stderr: io.Discard})
	if streamErr != nil {
		// A canceled probe is the reader leaving, not an undecidable answer:
		// reporting it as inconclusive would attach behind their back.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return probeInconclusive, ctxErr
		}
		if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
			return probeInconclusive, streamErr
		}
		return probeInconclusive, nil
	}
	switch result.ExitCode {
	case 0:
		return probeBashPresent, nil
	case 126, 127:
		// The runtime's own "cannot start it" statuses: not executable, or not
		// found. Any other status means bash ran and reported something, which
		// says nothing about whether it exists.
		return probeBashAbsent, nil
	}
	return probeInconclusive, nil
}

// isBashCommand reports whether the effective argv starts with bash: the pinned
// path or the bare name. The rest of the argv never changes the decision.
func isBashCommand(command []string) bool {
	return len(command) > 0 && (command[0] == shellBash || command[0] == "bash")
}

// substitutionRetry is the one retry the attach makes on its own: an image
// without bash still gets a shell. Everything else is reported unchanged.
func substitutionRetry(command []string, output string) ([]string, bool) {
	if len(command) == 0 || (command[0] != shellBash && command[0] != "bash") {
		return nil, false
	}
	if !looksLikeMissingCommand(output) {
		return nil, false
	}
	return append([]string{shellSh}, command[1:]...), true
}
