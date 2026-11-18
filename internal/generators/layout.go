// Package generators holds the scaffolding shared by host-script generators
// (`generate-git-hook`, and later the S3 generator): project-scoped path
// validation for the synced destination and the generated script file, plus
// the safe executable-file writer. Generators own their script text and
// previews; this package owns no Git or AWS behavior.
package generators

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options controls the one decision a generator may relax: whether paths
// outside the project folder were explicitly confirmed.
type Options struct {
	// AllowOutside is set only by an explicit user input. It never permits
	// the project folder itself as the destination.
	AllowOutside bool
}

// Layout is the resolved, validated location pair for one generated script.
// All paths are absolute and cleaned. Relative fields are project-relative
// when the path lives inside the project; otherwise they keep the absolute
// spelling.
type Layout struct {
	ProjectDir     string
	Destination    string
	ScriptPath     string
	DestinationRel string
	ScriptRel      string
	// ScriptToProject is the path from the script's directory to the project
	// folder, used to derive the project root when the script runs.
	ScriptToProject string
	// ScriptInsideProject reports whether the output is project-relative (and
	// therefore can derive the project root from its own location).
	ScriptInsideProject bool
}

// InputError is a rejected path or input value. The generator turns it into
// the input diagnostic (exit 2); it is never a partial write.
type InputError struct {
	Field   string
	Message string
	Detail  string
}

func (e *InputError) Error() string {
	if e.Detail != "" {
		return e.Field + ": " + e.Message + " (" + e.Detail + ")"
	}
	return e.Field + ": " + e.Message
}

func inputError(field, message, detail string) *InputError {
	return &InputError{Field: field, Message: message, Detail: detail}
}

// ResolveLayout validates that the destination is project data and the output
// is a script file, not the project root and not the synced tree itself.
//
// Rules (both paths):
//   - empty, control characters and a `.git` component are rejected;
//   - relative paths are resolved against the project folder;
//   - outside the project is rejected unless Options.AllowOutside is set;
//   - existing symlinks are resolved before the containment check, so a
//     symlinked ancestor cannot redirect the write outside the project.
//
// Destination-specific rules: not the project root, must not exist as a
// non-directory. Output-specific rules: not an existing symlink or directory,
// and not inside the destination (a checkout must never overwrite the script).
func ResolveLayout(projectDir, destination, scriptPath string, opts Options) (Layout, error) {
	project, err := canonicalProject(projectDir)
	if err != nil {
		return Layout{}, inputError("project", "프로젝트 폴더를 확인하지 못했습니다.", err.Error())
	}
	dest := strings.TrimSpace(destination)
	script := strings.TrimSpace(scriptPath)
	if dest == "" {
		return Layout{}, inputError("dest", "받을 폴더를 입력하세요.", "")
	}
	if script == "" {
		return Layout{}, inputError("output", "스크립트 파일 경로를 입력하세요.", "")
	}
	if hasControl(dest) {
		return Layout{}, inputError("dest", "받을 폴더 경로에 허용되지 않는 제어 문자가 있습니다.", "")
	}
	if hasControl(script) {
		return Layout{}, inputError("output", "스크립트 경로에 허용되지 않는 제어 문자가 있습니다.", "")
	}

	destSpell := candidate(project, dest)
	scriptSpell := candidate(project, script)
	if hasGitComponent(destSpell) {
		return Layout{}, inputError("dest", "받을 폴더를 .git 내부로 지정할 수 없습니다.", dest)
	}
	if hasGitComponent(scriptSpell) {
		return Layout{}, inputError("output", "스크립트를 .git 내부에 만들 수 없습니다.", script)
	}

	destResolved, err := resolveExisting(destSpell)
	if err != nil {
		return Layout{}, inputError("dest", "받을 폴더 경로를 확인하지 못했습니다.", err.Error())
	}
	scriptResolved, err := resolveExisting(scriptSpell)
	if err != nil {
		return Layout{}, inputError("output", "스크립트 경로를 확인하지 못했습니다.", err.Error())
	}

	if !opts.AllowOutside {
		if !inside(project, destResolved) {
			return Layout{}, inputError("dest", "받을 폴더는 프로젝트 폴더 안이어야 합니다. 바깥 경로는 명시적 확인이 필요합니다.", dest)
		}
		if !inside(project, scriptResolved) {
			return Layout{}, inputError("output", "스크립트 경로는 프로젝트 폴더 안이어야 합니다. 바깥 경로는 명시적 확인이 필요합니다.", script)
		}
	}
	if destResolved == project {
		return Layout{}, inputError("dest", "프로젝트 폴더 자체를 받을 폴더로 사용할 수 없습니다.", dest)
	}
	if inside(destResolved, scriptResolved) {
		return Layout{}, inputError("output", "스크립트를 받을 폴더 안에 만들 수 없습니다. checkout이 스크립트를 덮을 수 있습니다.", script)
	}
	if filepath.Dir(scriptResolved) == destResolved {
		return Layout{}, inputError("dest", "받을 폴더가 스크립트 폴더와 같습니다. 서로 다른 폴더를 사용하세요.", dest)
	}

	if info, statErr := os.Lstat(scriptSpell); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return Layout{}, inputError("output", "기존 스크립트 경로가 심볼릭 링크입니다. 안전을 위해 덮어쓰지 않습니다.", script)
		}
		if info.IsDir() {
			return Layout{}, inputError("output", "스크립트 경로가 폴더입니다.", script)
		}
		if !info.Mode().IsRegular() {
			return Layout{}, inputError("output", "스크립트 경로가 일반 파일이 아닙니다.", script)
		}
	}
	if info, statErr := os.Lstat(destResolved); statErr == nil && !info.IsDir() {
		return Layout{}, inputError("dest", "받을 폴더 경로에 폴더가 아닌 파일이 있습니다.", dest)
	}

	layout := Layout{
		ProjectDir:     project,
		Destination:    destResolved,
		ScriptPath:     scriptResolved,
		DestinationRel: relativeOrAbsolute(project, destResolved),
		ScriptRel:      relativeOrAbsolute(project, scriptResolved),
	}
	layout.ScriptInsideProject = inside(project, scriptResolved)
	layout.ScriptToProject, err = filepath.Rel(filepath.Dir(scriptResolved), project)
	if err != nil {
		return Layout{}, inputError("output", "스크립트 위치에서 프로젝트 폴더까지의 경로를 계산하지 못했습니다.", err.Error())
	}
	return layout, nil
}

func canonicalProject(projectDir string) (string, error) {
	raw := strings.TrimSpace(projectDir)
	if raw == "" {
		return "", fmt.Errorf("empty project folder")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory")
	}
	return filepath.Clean(resolved), nil
}

func candidate(project, raw string) string {
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Clean(filepath.Join(project, raw))
}

// resolveExisting resolves symlinks for the nearest existing ancestor and
// reappends the missing tail. The file itself is not created.
func resolveExisting(path string) (string, error) {
	existing := path
	var tail []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		tail = append([]string{filepath.Base(existing)}, tail...)
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	parts := append([]string{resolved}, tail...)
	return filepath.Clean(filepath.Join(parts...)), nil
}

func inside(base, path string) bool {
	if path == base {
		return true
	}
	return strings.HasPrefix(path, base+string(filepath.Separator))
}

func relativeOrAbsolute(project, path string) string {
	if !inside(project, path) {
		return path
	}
	rel, err := filepath.Rel(project, path)
	if err != nil {
		return path
	}
	return rel
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func hasGitComponent(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".git" {
			return true
		}
	}
	return false
}
