package rollback

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

// Present implements core.Presenter for successful rollbacks. It states the
// recorded images that were applied, what the runtime actually reports, and
// what was not restored.
func Present(res core.Result, width int) string {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return msgNoController
	}
	report := payload.Rollback
	var b strings.Builder
	title := msgSuccessHeader
	if !report.Verified {
		title = msgSuccessUnverified
	}
	fmt.Fprintf(&b, "%s\n", title)
	fmt.Fprintf(&b, "%s: %s\n", msgSuccessProject, report.ProjectName)
	fmt.Fprintf(&b, "%s: %s\n", msgSuccessFolder, res.Target.ProjectDir)
	if len(report.Files) > 0 {
		fmt.Fprintf(&b, "%s: %s\n", msgSuccessFiles, strings.Join(report.Files, ", "))
	}
	if len(report.EnvFiles) > 0 {
		fmt.Fprintf(&b, "%s: %s\n", msgSuccessEnvFiles, strings.Join(report.EnvFiles, ", "))
	}
	if len(report.Profiles) > 0 {
		fmt.Fprintf(&b, "%s: %s\n", msgSuccessProfiles, strings.Join(report.Profiles, ", "))
	}
	if report.Environment != "" {
		fmt.Fprintf(&b, "%s: %s\n", msgSuccessEnv, report.Environment)
	}
	fmt.Fprintf(&b, "%s: %s\n", msgSuccessServices, strings.Join(report.Services, ", "))
	b.WriteString("\n" + msgPlanImages + "\n")
	for _, pin := range report.Images {
		fmt.Fprintf(&b, "  - %s: %s (태그: %s)\n", pin.Service, pin.ImageID, pin.RetentionTag)
	}
	b.WriteString("\n" + msgSuccessReadiness + "\n")
	if len(report.Readiness) == 0 {
		fmt.Fprintf(&b, "  %s\n", msgSuccessNone)
	}
	for _, svc := range report.Readiness {
		fmt.Fprintf(&b, "  - %s: %s (%s)\n", svc.Service, readinessLabel(svc), readinessDetailText(svc))
	}
	b.WriteString("\n" + msgSuccessRecord + "\n")
	fmt.Fprintf(&b, "  %s\n", msgSuccessRecordKept)
	if len(report.Notices) > 0 {
		b.WriteString("\n주의\n")
		for _, notice := range report.Notices {
			fmt.Fprintf(&b, "  - %s\n", notice)
		}
	}
	b.WriteString("\n" + msgSuccessNext + "\n")
	fmt.Fprintf(&b, "  - %s\n", msgSuccessNextStatus)
	fmt.Fprintf(&b, "  - %s\n", msgSuccessNextUpdate)
	fmt.Fprintf(&b, "  - %s\n", msgSuccessNextData)
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

func readinessLabel(svc stack.ServiceReadiness) string {
	switch svc.Status {
	case stack.ReadinessHealthy:
		return "실행 중 · healthcheck 통과"
	case stack.ReadinessRunningUnverified:
		return "실행 중 · 앱 확인 안 됨 (healthcheck 없음)"
	case stack.ReadinessUnhealthy:
		return "실행 중 · unhealthy"
	case stack.ReadinessRestarting:
		return "재시작 반복"
	case stack.ReadinessExited:
		return "종료됨"
	case stack.ReadinessNotStarted:
		return "시작되지 않음"
	case stack.ReadinessNotCreated:
		return "컨테이너 없음"
	case stack.ReadinessTimeout:
		return "확인 시간 초과"
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
