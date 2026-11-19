package initconfig

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
)

// Present renders the saved-file summary. The exact content was already shown
// in the CLI plan or the TUI wizard preview, so this screen repeats the effects
// and the next command rather than a second full file dump.
func Present(res core.Result, width int) string {
	report, ok := payloadReport(res.Data)
	if !ok {
		return fmt.Sprintf("%s\n%s: %s\n", msgSaved, msgPreviewNext, msgNextCommand)
	}
	var b strings.Builder
	b.WriteString(msgSaved)
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s: %s\n", msgSavedPath, report.Path)
	fmt.Fprintf(&b, "%s: %s", msgSavedService, report.Service)
	if report.Mode == string(ModeDockerfile) {
		b.WriteString(" (Dockerfile 빌드)")
	} else {
		b.WriteString(" (이미지)")
	}
	b.WriteString("\n")
	if report.ProjectName != "" {
		fmt.Fprintf(&b, "프로젝트 이름(Compose 파생): %s\n", report.ProjectName)
	}
	if report.Validation.Command != "" {
		fmt.Fprintf(&b, "%s: %s 통과\n", msgSavedCheck, report.Validation.Command)
	}
	if report.Overwritten {
		fmt.Fprintf(&b, "%s: %s\n", msgSavedChanged, msgSavedReplace)
	} else {
		fmt.Fprintf(&b, "%s: %s\n", msgSavedChanged, msgSavedNew)
	}
	for _, notice := range report.Notices {
		fmt.Fprintf(&b, "- %s\n", notice)
	}
	fmt.Fprintf(&b, "\n%s: %s\n", msgPreviewNext, report.NextCommand)
	if report.NextCommand == msgNextCommand {
		b.WriteString(msgPreviewInDir)
		b.WriteString("\n")
	}
	b.WriteString(msgNoRun)
	b.WriteString("\n")
	b.WriteString(msgSavedNoOther)
	b.WriteString("\n")
	return b.String()
}

func payloadReport(data any) (Report, bool) {
	switch value := data.(type) {
	case Payload:
		return value.Init, true
	case *Payload:
		if value != nil {
			return value.Init, true
		}
	case Report:
		return value, true
	case *Report:
		if value != nil {
			return *value, true
		}
	}
	return Report{}, false
}

// CanceledDiagnostic states the save window honestly even though the atomic
// saver leaves either the previous file or the complete new content.
func CanceledDiagnostic() core.Diagnostic {
	return core.Diagnostic{
		Category:    core.CategoryCanceled,
		Severity:    core.SeverityWarning,
		Message:     msgCanceled,
		NextActions: []string{msgCanceledAct, "dcraft status로 기존 Compose 설정을 확인하세요."},
	}
}
