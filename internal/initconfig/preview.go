package initconfig

import (
	"fmt"
	"path/filepath"
	"strings"
)

func previewText(p *prepared) string {
	var b strings.Builder
	b.WriteString(msgPreviewTitle)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "%s: %s\n", msgPreviewMode, p.input.modeLabel())
	fmt.Fprintf(&b, "%s: %s\n", msgPreviewService, p.input.Service)
	if p.input.Mode == ModeDockerfile {
		fmt.Fprintf(&b, "빌드 컨텍스트: %s\nDockerfile: %s\n", p.input.Context, p.input.Dockerfile)
	} else {
		fmt.Fprintf(&b, "이미지: %s\n", p.input.Image)
	}
	pathLine := p.dest.Path
	if p.overwrite {
		pathLine += " (기존 파일을 덮어씀)"
	}
	fmt.Fprintf(&b, "%s: %s\n", msgPreviewPath, pathLine)
	fmt.Fprintf(&b, "%s: %s\n", msgPreviewName, p.validation.ProjectName)
	if p.dest.Outside {
		fmt.Fprintf(&b, "경고: %s\n", msgNoticeOutside)
	}
	b.WriteString("\n")
	b.WriteString(msgPreviewContent)
	b.WriteString(":\n")
	b.WriteString(p.spec.Content)
	if !strings.HasSuffix(p.spec.Content, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(msgPreviewEnd)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "%s: %s 통과 (서비스: %s)\n", msgPreviewCheck, p.validation.Command, strings.Join(p.validation.Services, ", "))
	fmt.Fprintf(&b, "%s: %s\n", msgPreviewCreates, p.dest.Path)
	resourceLines := resourceTexts(p.resources)
	if len(resourceLines) > 0 {
		b.WriteString(msgPreviewResources)
		b.WriteString(":\n")
		for _, line := range resourceLines {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	}
	if p.overwrite {
		fmt.Fprintf(&b, "%s: %s (file_overwrite) — 자동화에서는 --approve file_overwrite를 명시.\n", msgPreviewApproval, msgApprovalOverwrite)
	}
	if len(p.notices) > 0 {
		b.WriteString("\n")
		b.WriteString(msgPreviewRef)
		b.WriteString(":\n")
		for _, notice := range p.notices {
			fmt.Fprintf(&b, "- %s\n", notice)
		}
	}
	fmt.Fprintf(&b, "\n%s: %s\n", msgPreviewNext, p.nextCommand)
	if p.nextCommand == msgNextCommand {
		b.WriteString(msgPreviewInDir)
		b.WriteString("\n")
	}
	b.WriteString(msgNoRun)
	b.WriteString("\n")
	return b.String()
}

func resourceTexts(resources []Resource) []string {
	out := make([]string, 0, len(resources))
	for _, resource := range resources {
		if resource.Kind == "file" {
			continue
		}
		out = append(out, fmt.Sprintf("%s %s — %s", resourceLabel(resource.Kind), resource.Name, resource.Detail))
	}
	return out
}

func resourceLabel(kind string) string {
	switch kind {
	case "named_volume":
		return "이름 있는 볼륨"
	case "anonymous_volume":
		return "익명 볼륨"
	case "bind":
		return "호스트 경로"
	case "network":
		return "네트워크"
	case "file":
		return "파일"
	default:
		return kind
	}
}

// nextCommandFor names the command that uses the saved file. A standard file
// in the project root is discovered by the next `dcraft up`; any other path
// must be selected explicitly from its own directory, because Compose resolves
// relative paths and the default project name from the first file's directory.
func nextCommandFor(dest Destination, projectDir, projectName string) string {
	base := filepath.Base(dest.Path)
	command := ""
	if filepath.Clean(dest.Dir) == filepath.Clean(projectDir) {
		for _, name := range standardComposeFiles {
			if base == name {
				command = msgNextCommand
				break
			}
		}
	}
	if command == "" {
		command = fmt.Sprintf("cd %s && dcraft up -f %s", shellQuote(dest.Dir), shellQuote(base))
	}
	if name := strings.TrimSpace(projectName); name != "" {
		command += " -p " + shellQuote(name)
	}
	return command
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
