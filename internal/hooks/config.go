// Package hooks owns the explicit project registration of host scripts, the
// machine-local script trust record, and process-group execution around `up`
// and `update` only. It is consumed by the engine (ordering and approvals) and
// by generated-script features (#14/#15) through the same registration record.
package hooks

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/projectconfig"
)

// FileName is the optional project-local dcraft config that carries the hooks
// section. Other sections are preserved verbatim.
const FileName = ".dcraft.yaml"

// Stage is the execution point of a registered script.
type Stage = core.HookStage

const (
	StagePre  = core.HookStagePre
	StagePost = core.HookStagePost
)

// Registration is one explicitly registered local script. Both stages apply to
// `up` and `update`; there is no filename convention and no automatic
// discovery.
type Registration struct {
	Path  string `yaml:"path" json:"path"`
	Stage Stage  `yaml:"stage" json:"stage"`
}

// Catalog is the parsed hooks section of one project.
type Catalog struct {
	Path          string
	ProjectDir    string
	Registrations []Registration
}

// Has reports whether at least one script is registered.
func (c Catalog) Has() bool { return len(c.Registrations) > 0 }

var hookPathControl = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// Load reads <projectDir>/.dcraft.yaml and parses only its hooks section. A
// missing file is empty. Structural problems are hook_invalid errors so a
// malformed registration never silently disables a script.
func Load(projectDir string) (Catalog, error) {
	path := filepath.Join(projectDir, FileName)
	cat := Catalog{Path: path, ProjectDir: projectDir, Registrations: []Registration{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cat, nil
		}
		return cat, invalid(msgConfigUnreadable, err.Error())
	}
	doc, err := projectconfig.Decode(data)
	if err != nil {
		return cat, invalid(msgConfigMalformed, err.Error())
	}
	if doc == nil {
		return cat, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return cat, invalid(msgConfigRoot, projectconfig.LineOf(FileName, root))
	}
	return parseCatalogRoot(projectDir, root)
}

// parseCatalogRoot reads only the hooks section from an already decoded
// document, so the locked writer never reads the file twice.
func parseCatalogRoot(projectDir string, root *yaml.Node) (Catalog, error) {
	cat := Catalog{
		Path:          filepath.Join(projectDir, FileName),
		ProjectDir:    projectDir,
		Registrations: []Registration{},
	}
	node := projectconfig.MappingValue(root, "hooks")
	if node == nil || node.Tag == "!!null" {
		return cat, nil
	}
	if node.Kind != yaml.SequenceNode {
		return cat, invalid(msgConfigHooksList, projectconfig.LineOf(FileName, node))
	}
	regs, err := parseRegistrations(projectDir, node)
	if err != nil {
		return cat, err
	}
	cat.Registrations = regs
	return cat, nil
}

// Add validates one registration and writes the hooks section. The referenced
// file must already exist and be a regular file; Add never creates scripts and
// never grants script trust. A bounded project-config lock serializes
// concurrent registrations so no update is lost.
func Add(projectDir string, reg Registration) (Catalog, error) {
	cat, err := Load(projectDir)
	if err != nil {
		return cat, err
	}
	if err := validateRegistration(projectDir, reg, true); err != nil {
		return cat, err
	}
	resolved, _ := ResolvePath(projectDir, reg.Path)
	reg.Path = strings.TrimSpace(reg.Path)
	err = projectconfig.Update(projectDir, func(root *yaml.Node) error {
		current, err := parseCatalogRoot(projectDir, root)
		if err != nil {
			return err
		}
		cat = current
		for _, existing := range current.Registrations {
			existingPath, _ := ResolvePath(projectDir, existing.Path)
			if existingPath == resolved && existing.Stage == reg.Stage {
				return projectconfig.ErrNoChange
			}
		}
		regs := append(append([]Registration{}, current.Registrations...), reg)
		if err := setHooks(root, regs); err != nil {
			return err
		}
		cat.Registrations = regs
		return nil
	})
	if err != nil {
		return cat, configWriteError(err)
	}
	return cat, nil
}

// Remove deletes one registration identified by script path and stage. A
// missing registration is a hook_invalid error. It takes the same bounded
// project-config lock as Add.
func Remove(projectDir, scriptPath string, stage Stage) (Catalog, error) {
	cat, err := Load(projectDir)
	if err != nil {
		return cat, err
	}
	resolved, err := ResolvePath(projectDir, scriptPath)
	if err != nil {
		return cat, err
	}
	err = projectconfig.Update(projectDir, func(root *yaml.Node) error {
		current, err := parseCatalogRoot(projectDir, root)
		if err != nil {
			return err
		}
		cat = current
		regs := make([]Registration, 0, len(current.Registrations))
		removed := false
		for _, existing := range current.Registrations {
			existingPath, _ := ResolvePath(projectDir, existing.Path)
			if existingPath == resolved && existing.Stage == stage {
				removed = true
				continue
			}
			regs = append(regs, existing)
		}
		if !removed {
			return invalid(fmt.Sprintf(msgRemoveNotFound, scriptPath, stage), "")
		}
		if err := setHooks(root, regs); err != nil {
			return err
		}
		cat.Registrations = regs
		return nil
	})
	if err != nil {
		return cat, configWriteError(err)
	}
	return cat, nil
}

// ResolvePath turns a configured path into a canonical absolute path. Relative
// paths must stay inside the project folder; absolute local scripts are
// allowed for machine-local automation.
func ResolvePath(projectDir, configured string) (string, error) {
	raw := strings.TrimSpace(configured)
	if raw == "" {
		return "", invalid(msgPathEmpty, "")
	}
	if hookPathControl.MatchString(raw) {
		return "", invalid(msgPathControl, "")
	}
	if filepath.IsAbs(raw) {
		return canonicalExisting(filepath.Clean(raw)), nil
	}
	base, err := filepath.Abs(projectDir)
	if err != nil {
		return "", invalid(msgPathInvalid, err.Error())
	}
	resolved := filepath.Clean(filepath.Join(base, raw))
	if escapes(base, resolved) {
		return "", invalid(msgPathEscape, raw)
	}
	if canonical, err := filepath.EvalSymlinks(resolved); err == nil {
		if escapes(canonicalExisting(base), canonical) {
			return "", invalid(msgPathEscape, raw)
		}
		return canonical, nil
	}
	return resolved, nil
}

func escapes(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// canonicalExisting resolves symlinks when the path exists so the trust key is
// the same file identity from any spelling. A missing path falls back to the
// cleaned path; Add reports the missing file separately.
func canonicalExisting(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func validateRegistration(projectDir string, reg Registration, requireFile bool) error {
	if reg.Stage != StagePre && reg.Stage != StagePost {
		return invalid(fmt.Sprintf(msgUnknownStage, reg.Stage), "")
	}
	resolved, err := ResolvePath(projectDir, reg.Path)
	if err != nil {
		return err
	}
	if !requireFile {
		return nil
	}
	info, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return invalid(fmt.Sprintf(msgScriptMissing, reg.Path), resolved)
		}
		return invalid(msgScriptUnreadable, err.Error())
	}
	if info.IsDir() {
		return invalid(fmt.Sprintf(msgScriptDirectory, reg.Path), resolved)
	}
	if !info.Mode().IsRegular() {
		return invalid(fmt.Sprintf(msgScriptNotRegular, reg.Path), resolved)
	}
	return nil
}

func parseRegistrations(projectDir string, node *yaml.Node) ([]Registration, error) {
	regs := make([]Registration, 0, len(node.Content))
	seen := map[string]string{}
	for _, item := range node.Content {
		item := projectconfig.ResolveAlias(item)
		if item == nil || item.Kind != yaml.MappingNode {
			return nil, invalid(msgRegistrationMapping, projectconfig.LineOf(FileName, item))
		}
		values := map[string]string{}
		lines := map[string]int{}
		for i := 0; i+1 < len(item.Content); i += 2 {
			keyNode, valueNode := item.Content[i], item.Content[i+1]
			if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
				return nil, invalid(msgRegistrationFieldKey, projectconfig.LineOf(FileName, keyNode))
			}
			field := keyNode.Value
			if field != "path" && field != "stage" {
				return nil, invalid(fmt.Sprintf(msgUnknownField, field), projectconfig.LineOf(FileName, keyNode))
			}
			if prev, dup := lines[field]; dup {
				return nil, invalid(fmt.Sprintf(msgDuplicateField, field), fmt.Sprintf("%s:%d and %s:%d", FileName, prev, FileName, keyNode.Line))
			}
			lines[field] = keyNode.Line
			valueNode = projectconfig.ResolveAlias(valueNode)
			if valueNode == nil || valueNode.Kind != yaml.ScalarNode || valueNode.Tag != "!!str" {
				return nil, invalid(fmt.Sprintf(msgFieldString, field), projectconfig.LineOf(FileName, valueNode))
			}
			values[field] = valueNode.Value
		}
		reg := Registration{
			Path:  strings.TrimSpace(values["path"]),
			Stage: Stage(strings.TrimSpace(values["stage"])),
		}
		if err := validateRegistration(projectDir, reg, false); err != nil {
			return nil, err
		}
		resolved, _ := ResolvePath(projectDir, reg.Path)
		key := resolved + "\x00" + string(reg.Stage)
		if prev, dup := seen[key]; dup {
			return nil, invalid(fmt.Sprintf(msgDuplicateRegistration, reg.Path, reg.Stage), prev)
		}
		seen[key] = fmt.Sprintf("%s:%d", FileName, item.Line)
		regs = append(regs, reg)
	}
	return regs, nil
}

func setHooks(root *yaml.Node, regs []Registration) error {
	key := projectconfig.MappingKey(root, "hooks")
	if len(regs) == 0 {
		if key >= 0 {
			root.Content = append(root.Content[:key], root.Content[key+2:]...)
		}
		return nil
	}
	value := &yaml.Node{}
	if err := value.Encode(regs); err != nil {
		return invalid(msgConfigEncode, err.Error())
	}
	if key >= 0 {
		root.Content[key+1] = value
	} else {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "hooks"},
			value,
		)
	}
	return nil
}

// configWriteError keeps the feature's diagnostic category while mapping the
// shared writer's failure kinds onto the existing messages.
func configWriteError(err error) error {
	if err == nil {
		return nil
	}
	var ce *core.Error
	if errors.As(err, &ce) {
		return err
	}
	var pe *projectconfig.Error
	if errors.As(err, &pe) {
		switch pe.Kind {
		case projectconfig.KindUnreadable:
			return invalid(msgConfigUnreadable, pe.Detail)
		case projectconfig.KindMalformed:
			return invalid(msgConfigMalformed, pe.Detail)
		case projectconfig.KindRoot:
			return invalid(msgConfigRoot, pe.Detail)
		case projectconfig.KindEncode:
			return invalid(msgConfigEncode, pe.Detail)
		case projectconfig.KindLockBusy:
			return configLockBusy(pe.Detail)
		case projectconfig.KindLockFailed, projectconfig.KindUnsupported:
			return configLockError(pe)
		}
		return invalid(msgConfigWrite, pe.Detail)
	}
	return invalid(msgConfigWrite, err.Error())
}

func invalid(message, detail string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryHookInvalid,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      detail,
		NextActions: []string{msgConfigActionOne, msgConfigActionTwo},
	})
}

func configLockError(err error) *core.Error {
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryHookInvalid,
		Severity:    core.SeverityError,
		Message:     msgConfigLockFailed,
		Detail:      err.Error(),
		NextActions: []string{msgConfigLockActionOne, msgConfigLockActionTwo},
	})
}

func configLockBusy(path string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category: core.CategoryHookInvalid,
		Severity: core.SeverityError,
		Message:  msgConfigLocked,
		Detail:   path,
		NextActions: []string{
			msgConfigLockActionOne,
			"다른 dcraft 명령이 끝난 뒤 다시 실행하세요.",
		},
	})
}
