// Package environments loads the optional project-local environment alias
// config. An alias only maps a name to a Compose file list, explicit env files
// and profiles; it is not a merge language and it never modifies Compose files.
//
// The config file lives next to the Compose files and is optional. When absent,
// standard Compose discovery applies and no alias can be selected.
package environments

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

// FileName is the optional project-local config file. It is a dcraft file, not
// a Compose file: it is never passed to Compose and never replaces or edits a
// Compose configuration.
const FileName = ".dcraft.yaml"

// Catalog is the parsed environment config of one project folder.
type Catalog struct {
	// Path is the config file path that was read (whether or not it exists).
	Path string
	// Environments preserve config file order.
	Environments []core.Environment
}

// Names returns the registered aliases in config order.
func (c Catalog) Names() []string {
	names := make([]string, 0, len(c.Environments))
	for _, env := range c.Environments {
		names = append(names, env.Name)
	}
	return names
}

// Find looks up one alias.
func (c Catalog) Find(name string) (core.Environment, bool) {
	for _, env := range c.Environments {
		if env.Name == name {
			return env, true
		}
	}
	return core.Environment{}, false
}

// Expand returns the alias with its relative inputs resolved against the
// project folder and verifies that every referenced file exists. It does not
// read or merge the Compose files themselves; Compose owns that.
func (c Catalog) Expand(name string) (core.Environment, error) {
	env, ok := c.Find(name)
	if !ok {
		return core.Environment{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryEnvironmentUnknown,
			Severity:    core.SeverityError,
			Message:     unknownEnvironmentMessage(name, c.Names()),
			Detail:      strings.Join(c.Names(), ", "),
			NextActions: []string{msgUnknownEnvActionOne, msgUnknownEnvActionTwo},
		})
	}
	for _, path := range env.Files {
		if err := requireFile(path); err != nil {
			return core.Environment{}, core.FailCause(err, core.Diagnostic{
				Category:    core.CategoryEnvironmentInvalid,
				Severity:    core.SeverityError,
				Message:     fmt.Sprintf(msgEnvComposeFileMissing, env.Name),
				Detail:      path,
				NextActions: []string{msgEnvFileAction},
			})
		}
	}
	for _, path := range env.EnvFiles {
		if err := requireFile(path); err != nil {
			return core.Environment{}, core.FailCause(err, core.Diagnostic{
				Category:    core.CategoryEnvironmentInvalid,
				Severity:    core.SeverityError,
				Message:     fmt.Sprintf(msgEnvEnvFileMissing, env.Name),
				Detail:      path,
				NextActions: []string{msgEnvFileAction},
			})
		}
	}
	env.Files = append([]string{}, env.Files...)
	env.EnvFiles = append([]string{}, env.EnvFiles...)
	env.Profiles = append([]string{}, env.Profiles...)
	if env.Files == nil {
		env.Files = []string{}
	}
	if env.EnvFiles == nil {
		env.EnvFiles = []string{}
	}
	if env.Profiles == nil {
		env.Profiles = []string{}
	}
	return env, nil
}

// Load reads <projectDir>/.dcraft.yaml. A missing file is not an error and
// yields an empty catalog. Structural problems are environment_invalid errors;
// referenced files are verified later by Expand so a broken unused alias does
// not block the aliases that work.
func Load(projectDir string) (Catalog, error) {
	path := filepath.Join(projectDir, FileName)
	cat := Catalog{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cat, nil
		}
		return cat, invalidConfig(msgConfigUnreadable, err.Error())
	}

	doc, err := projectconfig.Decode(data)
	if err != nil {
		return cat, invalidConfig(msgConfigMalformed, err.Error())
	}
	if doc == nil {
		return cat, nil
	}
	root := doc.Content[0]
	if root.Kind == 0 || root.Tag == "!!null" {
		return cat, nil
	}
	if root.Kind != yaml.MappingNode {
		return cat, invalidConfig(msgConfigRoot, projectconfig.LineOf(FileName, root))
	}
	return parseCatalogRoot(projectDir, root)
}

// parseCatalogRoot reads only the environments section from an already
// decoded document, so the locked writer never reads the file twice.
func parseCatalogRoot(projectDir string, root *yaml.Node) (Catalog, error) {
	cat := Catalog{Path: filepath.Join(projectDir, FileName)}
	envNode := projectconfig.MappingValue(root, "environments")
	if envNode == nil || envNode.Tag == "!!null" {
		return cat, nil
	}
	if envNode.Kind != yaml.MappingNode {
		return cat, invalidConfig(msgConfigEnvironments, projectconfig.LineOf(FileName, envNode))
	}

	seen := map[string]int{}
	for i := 0; i+1 < len(envNode.Content); i += 2 {
		keyNode, valNode := envNode.Content[i], envNode.Content[i+1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
			return cat, invalidConfig(msgConfigAliasKey, projectconfig.LineOf(FileName, keyNode))
		}
		name := keyNode.Value
		if err := validateAlias(name); err != nil {
			return cat, invalidConfig(fmt.Sprintf(msgConfigAliasInvalid, name), err.Error())
		}
		if prev, dup := seen[name]; dup {
			return cat, invalidConfig(fmt.Sprintf(msgConfigAliasDuplicate, name), fmt.Sprintf("%s:%d and %s:%d", FileName, prev, FileName, keyNode.Line))
		}
		seen[name] = keyNode.Line

		env, err := parseEnvironment(projectDir, name, valNode)
		if err != nil {
			return cat, err
		}
		cat.Environments = append(cat.Environments, env)
	}
	return cat, nil
}

var aliasRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func validateAlias(name string) error {
	if aliasRe.MatchString(name) {
		return nil
	}
	return fmt.Errorf("alias must match %s", aliasRe)
}

var allowedEnvironmentFields = map[string]bool{
	"compose_files": true,
	"env_files":     true,
	"profiles":      true,
}

func parseEnvironment(projectDir, name string, node *yaml.Node) (core.Environment, error) {
	env := core.Environment{Name: name, Files: []string{}, EnvFiles: []string{}, Profiles: []string{}}
	if node.Kind == 0 || node.Tag == "!!null" {
		return env, invalidConfig(fmt.Sprintf(msgEnvEmpty, name), projectconfig.LineOf(FileName, node))
	}
	if node.Kind != yaml.MappingNode {
		return env, invalidConfig(fmt.Sprintf(msgEnvNotMapping, name), projectconfig.LineOf(FileName, node))
	}

	seenFields := map[string]int{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		fieldNode, valueNode := node.Content[i], node.Content[i+1]
		if fieldNode.Kind != yaml.ScalarNode || fieldNode.Tag != "!!str" {
			return env, invalidConfig(fmt.Sprintf(msgEnvUnknownField, name, fieldNode.Value), projectconfig.LineOf(FileName, fieldNode))
		}
		field := fieldNode.Value
		if !allowedEnvironmentFields[field] {
			return env, invalidConfig(fmt.Sprintf(msgEnvUnknownField, name, field), projectconfig.LineOf(FileName, fieldNode))
		}
		if prev, dup := seenFields[field]; dup {
			return env, invalidConfig(fmt.Sprintf(msgEnvDuplicateField, name, field), fmt.Sprintf("%s:%d and %s:%d", FileName, prev, FileName, fieldNode.Line))
		}
		seenFields[field] = fieldNode.Line

		var values []string
		if field == "compose_files" || field == "env_files" {
			var err error
			values, err = stringList(projectDir, name, field, valueNode)
			if err != nil {
				return env, err
			}
		} else {
			var err error
			values, err = profileList(name, valueNode)
			if err != nil {
				return env, err
			}
		}
		switch field {
		case "compose_files":
			env.Files = values
		case "env_files":
			env.EnvFiles = values
		case "profiles":
			env.Profiles = values
		}
	}
	if len(env.Files) == 0 && len(env.EnvFiles) == 0 && len(env.Profiles) == 0 {
		return env, invalidConfig(fmt.Sprintf(msgEnvEmpty, name), projectconfig.LineOf(FileName, node))
	}
	return env, nil
}

func stringList(projectDir, name, field string, node *yaml.Node) ([]string, error) {
	if node.Kind == 0 || node.Tag == "!!null" {
		return []string{}, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, invalidConfig(fmt.Sprintf(msgEnvNotList, name, field), projectconfig.LineOf(FileName, node))
	}
	out := make([]string, 0, len(node.Content))
	seen := map[string]bool{}
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return nil, invalidConfig(fmt.Sprintf(msgEnvListItem, name, field), projectconfig.LineOf(FileName, item))
		}
		raw := strings.TrimSpace(item.Value)
		if raw == "" {
			return nil, invalidConfig(fmt.Sprintf(msgEnvListEmpty, name, field), projectconfig.LineOf(FileName, item))
		}
		path := raw
		if !filepath.IsAbs(path) {
			path = filepath.Join(projectDir, path)
		}
		path = filepath.Clean(path)
		if seen[path] {
			return nil, invalidConfig(fmt.Sprintf(msgEnvListDuplicate, name, field, raw), projectconfig.LineOf(FileName, item))
		}
		seen[path] = true
		out = append(out, path)
	}
	return out, nil
}

func profileList(name string, node *yaml.Node) ([]string, error) {
	if node.Kind == 0 || node.Tag == "!!null" {
		return []string{}, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, invalidConfig(fmt.Sprintf(msgEnvNotList, name, "profiles"), projectconfig.LineOf(FileName, node))
	}
	out := make([]string, 0, len(node.Content))
	seen := map[string]bool{}
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return nil, invalidConfig(fmt.Sprintf(msgEnvListItem, name, "profiles"), projectconfig.LineOf(FileName, item))
		}
		profile := strings.TrimSpace(item.Value)
		if profile == "" {
			return nil, invalidConfig(fmt.Sprintf(msgEnvListEmpty, name, "profiles"), projectconfig.LineOf(FileName, item))
		}
		if strings.ContainsAny(profile, " \t") {
			return nil, invalidConfig(fmt.Sprintf(msgEnvProfileInvalid, name, profile), projectconfig.LineOf(FileName, item))
		}
		if seen[profile] {
			return nil, invalidConfig(fmt.Sprintf(msgEnvListDuplicate, name, "profiles", profile), projectconfig.LineOf(FileName, item))
		}
		seen[profile] = true
		out = append(out, profile)
	}
	return out, nil
}

func requireFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("file not found: %s", path)
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("not a regular file: %s", path)
	}
	return nil
}

func invalidConfig(message, detail string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryEnvironmentInvalid,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      detail,
		NextActions: []string{msgConfigActionOne, msgConfigActionTwo},
	})
}

func unknownEnvironmentMessage(name string, available []string) string {
	if len(available) == 0 {
		return fmt.Sprintf(msgUnknownEnvNone, name)
	}
	return fmt.Sprintf(msgUnknownEnv, name, strings.Join(available, ", "))
}
