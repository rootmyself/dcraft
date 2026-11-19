package lifecycle

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

// Present renders a successful lifecycle result for humans. Each lifecycle
// feature delegates its core.Presenter implementation here so the four
// commands share one honest description of what happened.
func Present(res core.Result, width int) string {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return msgNoPayload
	}
	report := payload.Lifecycle
	action := Action(report.Action)

	var b strings.Builder
	b.WriteString(doneLine(action))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s: %s (%s)\n", msgProjectLabel, report.ProjectName, nameSourceLabel(report.ProjectNameSource))
	if res.Target.ProjectDir != "" {
		fmt.Fprintf(&b, "%s: %s\n", msgFolderLabel, res.Target.ProjectDir)
	}
	if report.Scope == "services" && len(report.Services) > 0 {
		fmt.Fprintf(&b, "%s: %s (%s)\n", msgScopeLabel, strings.Join(report.Services, ", "), msgScopeServices)
	} else {
		fmt.Fprintf(&b, "%s: %s\n", msgScopeLabel, msgScopeStack)
	}
	fmt.Fprintf(&b, "%s: %s\n", msgEffectLabel, effectLine(action))
	fmt.Fprintf(&b, "%s: %s\n", msgStateLabel, stateLine(report.State))

	if len(report.Notices) > 0 {
		fmt.Fprintf(&b, "\n%s\n", msgNoticesLabel)
		for _, notice := range report.Notices {
			fmt.Fprintf(&b, "  - %s\n", notice)
		}
	}

	fmt.Fprintf(&b, "\n%s\n", msgNextLabel)
	for _, line := range nextActions(action, report.State == nil) {
		fmt.Fprintf(&b, "  - %s\n", line)
	}
	_ = width
	return strings.TrimRight(b.String(), "\n")
}

func payloadFrom(data any) (Payload, bool) {
	switch v := data.(type) {
	case Payload:
		return v, true
	case *Payload:
		if v != nil {
			return *v, true
		}
	}
	return Payload{}, false
}

func doneLine(action Action) string {
	switch action {
	case ActionUp:
		return msgDoneUp
	case ActionStop:
		return msgDoneStop
	case ActionDown:
		return msgDoneDown
	case ActionRestart:
		return msgDoneRestart
	default:
		return msgDoneUp
	}
}

func effectLine(action Action) string {
	switch action {
	case ActionStop:
		return msgEffectStop
	case ActionDown:
		return msgEffectDown
	case ActionRestart:
		return msgEffectRestart
	default:
		return msgEffectUp
	}
}

func nameSourceLabel(source string) string {
	switch source {
	case "allocated":
		return msgNameAuto
	case "explicit":
		return msgNameExplicit
	case "configured":
		return msgNameConfigured
	case "existing":
		return msgNameExisting
	default:
		return msgNameUnknown
	}
}

func stateLine(snap *stack.Snapshot) string {
	if snap == nil {
		return msgStateMissing
	}
	return summarizeState(*snap)
}

func nextActions(action Action, stateUnknown bool) []string {
	var out []string
	switch action {
	case ActionUp:
		out = []string{msgNextStatus, msgNextStop, msgNextDown}
	case ActionStop:
		out = []string{msgNextUp, msgNextStatus}
	case ActionDown:
		out = []string{msgNextUp, msgNextStatus, msgNextVolumesKept}
	case ActionRestart:
		out = []string{msgNextStatus, msgNextRestartToApply}
	}
	if stateUnknown {
		out = append(out, msgNextObserveFailed)
	}
	return out
}
