package backup

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
)

// Present renders a successful backup for humans. It shows the user-chosen
// location, size, completion/integrity state, affected services and the
// sensitive-data notice. It never prints file contents or environment values.
func Present(res core.Result, width int) string {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return "백업 결과 표시 불가."
	}
	report := payload.Backup
	var b strings.Builder
	b.WriteString(msgDoneHeader)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "프로젝트: %s\n", report.ProjectName)
	fmt.Fprintf(&b, "저장 폴더: %s\n", report.OutputDir)
	fmt.Fprintf(&b, "백업 대상: %s\n", joinOrNone(sourceKeys(report.Sources)))
	fmt.Fprintf(&b, msgPlanServices+"\n", joinOrNone(report.AffectedServices))
	fmt.Fprintf(&b, msgPlanRunning+"\n", joinOrNone(report.RunningBefore))
	if len(report.Stopped) > 0 {
		fmt.Fprintf(&b, "중지했다가 다시 시작한 서비스: %s\n", strings.Join(report.Stopped, ", "))
	} else {
		b.WriteString("중지한 서비스: 없음 (이미 중지된 서비스는 시작하지 않았음)\n")
	}
	fmt.Fprintf(&b, "상태 복구: %s\n", joinOrNone(report.Restarted))
	core.WriteSection(&b, "산출물:")
	for _, art := range report.Artifacts {
		fmt.Fprintf(&b, "  - %s\n", art.Path)
		fmt.Fprintf(&b, "    완료: %t · 형식: dcraft 백업 v1 · %s: %s · %s: %d\n",
			art.Complete, msgSizeLabel, humanBytes(art.ArtifactSizeBytes), msgFilesLabel, art.Entries)
		fmt.Fprintf(&b, "    데이터 %.1f%% · SHA-256: %s\n",
			percent(art.SizeBytes, art.ArtifactSizeBytes), art.SHA256)
	}
	core.WriteSection(&b, "안내:")
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

func sourceKeys(sources []Source) []string {
	out := make([]string, 0, len(sources))
	for _, src := range sources {
		if src.Kind == data.KindBind {
			label := "폴더 " + src.HostPath
			if label != "" {
				out = append(out, label)
			}
			continue
		}
		key := src.VolumeKey
		if key == "" {
			key = src.VolumeName
		}
		if key != "" {
			out = append(out, key)
		}
	}
	return out
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func percent(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}
