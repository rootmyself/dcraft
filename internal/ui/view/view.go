package view

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
)

// Diagnostics renders Korean diagnostics for humans. Errors are problems;
// warnings are guidance that still allows success. Detail is raw tool output.
func Diagnostics(diags []core.Diagnostic) string {
	var b strings.Builder
	for _, d := range diags {
		label := "문제"
		if d.Severity == core.SeverityWarning {
			label = "주의"
		} else if d.Severity == core.SeverityInfo {
			label = "안내"
		}
		core.WriteSection(&b, fmt.Sprintf("%s: %s", label, d.Message))
		if strings.TrimSpace(d.Detail) != "" {
			for _, line := range strings.Split(strings.TrimSpace(d.Detail), "\n") {
				fmt.Fprintf(&b, "  %s\n", line)
			}
		}
		if len(d.NextActions) > 0 {
			core.WriteSection(&b, "다음 행동:")
			for i, action := range d.NextActions {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, action)
			}
		}
	}
	return core.NormalizeSections(b.String())
}

// Warnings returns only non-blocking diagnostics.
func Warnings(diags []core.Diagnostic) []core.Diagnostic {
	var out []core.Diagnostic
	for _, d := range diags {
		if d.Severity != core.SeverityError {
			out = append(out, d)
		}
	}
	return out
}

// Failure renders a failed or canceled result: outcome, target, diagnostics,
// completed and failed stages. It never claims partial work succeeded.
func Failure(res core.Result) string {
	var b strings.Builder
	b.WriteString(FailureText(res))
	if stages := StageList(res.Stages); stages != "" {
		b.WriteString("\n\n단계:\n")
		b.WriteString(stages)
	}
	return strings.TrimRight(b.String(), "\n")
}

// FailureText is Failure without the stage list. The CLI appends StageList; the
// TUI appends its own stage timeline, so both render the identical Korean
// outcome, target, operation ID and diagnostics while the stage rows use each
// interface's own glyphs and can show the stages that never ran.
func FailureText(res core.Result) string {
	var b strings.Builder
	title := "작업 실패"
	switch res.Outcome {
	case core.OutcomeCanceled:
		title = "작업 중단"
	}
	action := res.Action
	if action == "" {
		action = "(알 수 없음)"
	}
	fmt.Fprintf(&b, "%s: %s\n", title, action)
	if res.Target.ProjectDir != "" {
		fmt.Fprintf(&b, "대상: %s\n", res.Target.ProjectDir)
	}
	if res.SchemaVersion > 0 && res.OperationID != "" {
		fmt.Fprintf(&b, "작업 ID: %s\n", res.OperationID)
	}
	if len(res.Diagnostics) > 0 {
		b.WriteString("\n")
		b.WriteString(Diagnostics(res.Diagnostics))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// SuccessFallback is used when a feature does not implement core.Presenter.
func SuccessFallback(res core.Result) string {
	action := res.Action
	if action == "" {
		action = "작업"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "작업 완료: %s\n", action)
	if res.Target.ProjectDir != "" {
		fmt.Fprintf(&b, "대상: %s\n", res.Target.ProjectDir)
	}
	return strings.TrimRight(b.String(), "\n")
}

// StageList renders ordered stages with Korean status words.
func StageList(stages []core.Stage) string {
	var b strings.Builder
	for _, st := range stages {
		title := st.Title
		if title == "" {
			title = st.Name
		}
		fmt.Fprintf(&b, "  %s %s (%s)\n", stageSymbol(st.Status), title, stageStatusLabel(st.Status))
	}
	return strings.TrimRight(b.String(), "\n")
}

// StageStatusLabel is the Korean status word for one stage. It is exported so
// the TUI can render a stage with its own glyph set without duplicating the
// wording; view.StageList keeps using it.
func StageStatusLabel(status core.StageStatus) string { return stageStatusLabel(status) }

func stageSymbol(status core.StageStatus) string {
	switch status {
	case core.StageSucceeded:
		return "✓"
	case core.StageFailed:
		return "✕"
	case core.StageCanceled:
		return "■"
	case core.StageStarted:
		return "·"
	default:
		return "-"
	}
}

func stageStatusLabel(status core.StageStatus) string {
	switch status {
	case core.StageSucceeded:
		return "완료"
	case core.StageFailed:
		return "실패"
	case core.StageCanceled:
		return "중단"
	case core.StageStarted:
		return "진행 중"
	default:
		return "건너뜀"
	}
}
