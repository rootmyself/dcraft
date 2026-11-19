package update

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

// Present implements core.Presenter for successful updates. It states what ran,
// what was verified, what was only observed running, and what was recorded; it
// never claims an app is healthy without a healthcheck or a rollback that did
// not happen.
func Present(res core.Result, width int) string {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return msgNoUpdater
	}
	var b strings.Builder
	writeTarget(&b, res, payload.Update)
	b.WriteString("\n")
	// The plan is shown once, before the run (CLI preview / TUI plan screen).
	// Repeating it in the success output would print it twice per operation.
	b.WriteString(renderSuccess(payload, width))
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

func writeTarget(b *strings.Builder, res core.Result, report Report) {
	fmt.Fprintf(b, "%s: %s\n", msgSuccessProject, report.ProjectName)
	fmt.Fprintf(b, "%s: %s\n", msgSuccessFolder, res.Target.ProjectDir)
	writeList(b, msgSuccessFiles, res.Target.Files, msgSuccessNone)
	writeList(b, msgSuccessEnvFiles, res.Target.EnvFiles, msgSuccessNone)
	writeList(b, msgSuccessProfiles, res.Target.Profiles, msgSuccessNone)
	if res.Target.Environment != "" {
		fmt.Fprintf(b, "%s: %s\n", msgSuccessEnv, res.Target.Environment)
	}
	if report.Scope == "services" {
		fmt.Fprintf(b, "%s: %s\n", msgSuccessServices, strings.Join(report.Services, ", "))
	} else {
		fmt.Fprintf(b, "%s: %s\n", msgSuccessServices, msgSuccessWhole)
	}
}

func writeList(b *strings.Builder, label string, items []string, empty string) {
	if len(items) == 0 {
		fmt.Fprintf(b, "%s: %s\n", label, empty)
		return
	}
	fmt.Fprintf(b, "%s: %s\n", label, strings.Join(items, ", "))
}

func renderSuccess(payload Payload, width int) string {
	report := payload.Update
	var b strings.Builder
	title := msgSuccessHeader
	if !report.Verified {
		title = msgSuccessUnverified
	}
	fmt.Fprintf(&b, "%s\n", title)
	b.WriteString(msgSuccessWork + "\n")
	fmt.Fprintf(&b, "  - %s: %s\n", msgSuccessBuild, listOrNone(report.Built))
	fmt.Fprintf(&b, "  - %s: %s\n", msgSuccessPull, listOrNone(report.Pulled))
	apply := msgPlanDefaultRecreate
	if report.Options.ForceRecreate {
		apply = msgPlanForceRecreate
	}
	fmt.Fprintf(&b, "  - %s:%s\n", msgSuccessApply, apply)
	b.WriteString("\n" + msgSuccessReadiness + "\n")
	if len(report.Readiness) == 0 {
		fmt.Fprintf(&b, "  %s\n", msgSuccessNone)
	}
	for _, svc := range report.Readiness {
		fmt.Fprintf(&b, "  - %s: %s (%s)\n", svc.Service, readinessLabel(svc), readinessDetailText(svc))
	}
	b.WriteString("\n" + msgSuccessRecord + "\n")
	switch report.RecordStatus {
	case RecordRecorded:
		for _, line := range goodVersionSummary(report.GoodVersion) {
			b.WriteString(line + "\n")
		}
		if report.GoodVersion != nil {
			fmt.Fprintf(&b, "  %s\n", msgGoodRetentionNote)
		}
	case RecordFailed:
		fmt.Fprintf(&b, "  %s\n", msgGoodRecordFailed)
		if strings.TrimSpace(report.RecordDetail) != "" {
			fmt.Fprintf(&b, "  %s\n", report.RecordDetail)
		}
	default:
		fmt.Fprintf(&b, "  %s\n", msgSuccessNone)
	}
	if len(report.Notices) > 0 {
		b.WriteString("\n주의\n")
		for _, notice := range report.Notices {
			fmt.Fprintf(&b, "  - %s\n", notice)
		}
	}
	b.WriteString("\n" + msgSuccessNext + "\n")
	fmt.Fprintf(&b, "  - %s\n", msgSuccessNextStatus)
	fmt.Fprintf(&b, "  - %s\n", msgSuccessNextUpdate)
	fmt.Fprintf(&b, "  - %s\n", msgSuccessNextRoll)
	return strings.TrimRight(b.String(), "\n")
}

func readinessLabel(svc stack.ServiceReadiness) string {
	switch svc.Status {
	case stack.ReadinessHealthy:
		return "실행 중 · " + msgHealthVerified
	case stack.ReadinessRunningUnverified:
		return "실행 중 · " + msgHealthUnverified
	case stack.ReadinessUnhealthy:
		return msgHealthUnhealthy
	case stack.ReadinessRestarting:
		return msgStateRestarting
	case stack.ReadinessExited:
		return msgStateExited
	case stack.ReadinessNotStarted:
		return msgStateNotStarted
	case stack.ReadinessNotCreated:
		return msgStateMissing
	case stack.ReadinessTimeout:
		return msgStateTimeout
	default:
		return string(svc.Status)
	}
}

func readinessDetailText(svc stack.ServiceReadiness) string {
	parts := []string{}
	if svc.HealthcheckConfigured {
		parts = append(parts, "healthcheck 설정됨")
	} else {
		parts = append(parts, "healthcheck 없음")
	}
	if svc.Detail != "" {
		parts = append(parts, svc.Detail)
	}
	return strings.Join(parts, " · ")
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return msgSuccessNone
	}
	return strings.Join(items, ", ")
}
