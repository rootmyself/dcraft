package restore

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
)

// Present renders a successful restore for humans. Failure and cancellation
// screens come from the shared diagnostics renderer.
func Present(res core.Result, width int) string {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return "복원 결과를 표시할 수 없습니다."
	}
	report := payload.Restore
	var b strings.Builder
	b.WriteString(msgDoneHeader)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "프로젝트: %s\n", report.ProjectName)
	fmt.Fprintf(&b, "백업: %s\n", report.ArtifactPath)
	if report.TargetKind == data.KindBind {
		fmt.Fprintf(&b, msgPreviewBindTarget+"\n", report.HostPath)
	} else {
		fmt.Fprintf(&b, "대상: %s → %s\n", report.VolumeKey, report.VolumeName)
	}
	fmt.Fprintf(&b, "백업 내용: %d개 항목, %s\n", report.BackupContent.Entries, humanBytes(report.BackupContent.Bytes))
	fmt.Fprintf(&b, "교체 후 데이터 상태: %s\n", report.DataState)
	fmt.Fprintf(&b, "안전 백업: %s\n", report.SafetyBackup)
	fmt.Fprintf(&b, msgPreviewServices+"\n", joinOrNone(report.AffectedServices))
	fmt.Fprintf(&b, msgPreviewRunning+"\n", joinOrNone(report.RunningBefore))
	if len(report.Stopped) > 0 {
		fmt.Fprintf(&b, "중지했다가 다시 시작한 서비스: %s\n", strings.Join(report.Stopped, ", "))
	} else {
		fmt.Fprintf(&b, "중지한 서비스: 없음\n")
	}
	fmt.Fprintf(&b, "복구한 서비스: %s\n", joinOrNone(report.Restarted))
	if len(report.Warnings) > 0 {
		b.WriteString("\n경고:\n")
		for _, warning := range report.Warnings {
			fmt.Fprintf(&b, "  - %s\n", warning)
		}
	}
	b.WriteString("\n안내:\n")
	for _, notice := range report.Notices {
		fmt.Fprintf(&b, "  - %s\n", notice)
	}
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
