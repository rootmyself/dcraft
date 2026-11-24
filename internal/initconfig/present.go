package initconfig

import (
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
)

// Present renders the saved-file summary. The exact content was already shown
// in the CLI plan or the TUI wizard preview, so this screen repeats the effects
// and the next command rather than a second full file dump.
func Present(res core.Result, width int) string {
	return strings.Join(textLines(presentLines(res)), "\n") + "\n"
}

// Document implements core.ViewPresenter: the saved path, the validated command
// and the next command are declared kinds. The CLI keeps the same text.
func Document(res core.Result) core.Document {
	return core.Document{Blocks: []core.Block{{Kind: core.BlockValueLines, Lines: documentLines(res)}}}
}

// documentLines is the saved-file screen in the interactive surface's terms.
// The saved file is a standard Compose input in the project folder, so the
// reader can start it from the menu. A file written elsewhere or under another
// name needs the explicit command, because the menu would not find it; there the
// command stays.
func documentLines(res core.Result) []core.ValueLine {
	lines := presentLines(res)
	report, ok := payloadReport(res.Data)
	if ok && report.NextCommand != msgNextCommand {
		return lines
	}
	for i, line := range lines {
		if len(line) == 0 || line[0].Text != msgPreviewNext+": " {
			continue
		}
		lines[i] = core.ValueLine{{Text: msgDocNext}}
		rest := lines[i+1:]
		if len(rest) > 0 && rest[0][0].Text == msgPreviewInDir {
			rest = rest[1:]
		}
		return append(lines[:i+1], rest...)
	}
	return lines
}

func presentLines(res core.Result) []core.ValueLine {
	report, ok := payloadReport(res.Data)
	if !ok {
		return []core.ValueLine{
			{{Text: msgSaved}},
			{{Text: msgPreviewNext + ": "}, {Text: msgNextCommand, Kind: core.ValueCommand}},
		}
	}
	lines := []core.ValueLine{
		{{Text: msgSaved}},
		{{Text: msgSavedPath + ": "}, {Text: report.Path, Kind: core.ValuePath}},
		{{Text: msgSavedService + ": "}, {Text: report.Service, Kind: core.ValueService}},
	}
	if report.Mode == string(ModeDockerfile) {
		lines[len(lines)-1] = append(lines[len(lines)-1], core.TextRun{Text: " (Dockerfile 빌드)"})
	} else {
		lines[len(lines)-1] = append(lines[len(lines)-1], core.TextRun{Text: " (이미지)"})
	}
	if report.ProjectName != "" {
		lines = append(lines, core.ValueLine{
			{Text: "프로젝트 이름(Compose 파생): "},
			{Text: report.ProjectName, Kind: core.ValueProject},
		})
	}
	if report.Validation.Command != "" {
		lines = append(lines, core.ValueLine{
			{Text: msgSavedCheck + ": "},
			{Text: report.Validation.Command, Kind: core.ValueCommand},
			{Text: " 통과"},
		})
	}
	if report.Overwritten {
		lines = append(lines, core.ValueLine{{Text: msgSavedChanged + ": " + msgSavedReplace}})
	} else {
		lines = append(lines, core.ValueLine{{Text: msgSavedChanged + ": " + msgSavedNew}})
	}
	for _, notice := range report.Notices {
		lines = append(lines, core.ValueLine{{Text: "- " + notice}})
	}
	lines = append(lines, nil, core.ValueLine{
		{Text: msgPreviewNext + ": "},
		{Text: report.NextCommand, Kind: core.ValueCommand},
	})
	if report.NextCommand == msgNextCommand {
		lines = append(lines, core.ValueLine{{Text: msgPreviewInDir}})
	}
	lines = append(lines, core.ValueLine{{Text: msgNoRun}}, core.ValueLine{{Text: msgSavedNoOther}})
	return lines
}

func textLines(lines []core.ValueLine) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = line.Text()
	}
	return out
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
		NextActions: []string{msgCanceledAct, "dcraft status로 기존 Compose 설정 확인."},
	}
}
