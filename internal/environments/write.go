package environments

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/rootmyself/dcraft/internal/core"
)

// InputError is invalid caller input for alias creation or removal. It is not
// an operation failure: the feature maps it to a usage diagnostic (exit 2).
// Configuration-file and filesystem failures stay *core.Error values.
type InputError struct {
	Message string
	Detail  string
}

func (e *InputError) Error() string {
	if strings.TrimSpace(e.Detail) == "" {
		return e.Message
	}
	return e.Message + ": " + e.Detail
}

// processConfigMu serializes goroutines in this process in addition to the
// cross-process .dcraft.lock flock. The hooks package uses the same lock file,
// so an environments write never loses a concurrent hooks update.
var processConfigMu sync.Mutex

// Add validates one alias and appends it to the project's environments
// section. The existing document is edited as YAML nodes so unknown top-level
// keys, the secrets/hooks sections and attached comments survive. The write is
// serialized with the project config lock and replaced atomically, preserving
// the file mode.
func Add(projectDir string, env core.Environment) (Catalog, error) {
	release, err := acquireConfigLock(projectDir)
	if err != nil {
		return Catalog{}, err
	}
	defer release()

	prepared, err := prepareEnvironment(projectDir, env)
	if err != nil {
		return Catalog{}, err
	}
	cat, err := Load(projectDir)
	if err != nil {
		return cat, err
	}
	if _, exists := cat.Find(prepared.Name); exists {
		return cat, &InputError{Message: fmt.Sprintf(msgWriteDuplicateAlias, prepared.Name)}
	}
	if err := mutateConfig(projectDir, func(root *yaml.Node) error {
		node := mappingValue(root, "environments")
		if node == nil || node.Tag == "!!null" {
			value := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			if node != nil {
				for i := 0; i+1 < len(root.Content); i += 2 {
					if root.Content[i].Value == "environments" {
						root.Content[i+1] = value
						break
					}
				}
			} else {
				root.Content = append(root.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "environments"},
					value,
				)
			}
			node = value
		}
		if node.Kind != yaml.MappingNode {
			return invalidConfig(msgConfigEnvironments, lineOf(node))
		}
		node.Content = append(node.Content, scalarNode(prepared.Name), sectionNode(prepared))
		return nil
	}); err != nil {
		return cat, err
	}
	return Load(projectDir)
}

// Remove deletes one alias. A missing alias is environment_unknown (exit 2).
// Other sections, comments and unknown keys are preserved.
func Remove(projectDir, name string) (Catalog, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Catalog{}, &InputError{Message: msgWriteNameMissing}
	}
	release, err := acquireConfigLock(projectDir)
	if err != nil {
		return Catalog{}, err
	}
	defer release()

	cat, err := Load(projectDir)
	if err != nil {
		return cat, err
	}
	if _, ok := cat.Find(name); !ok {
		return cat, unknownEnvironment(name, cat.Names())
	}
	if err := mutateConfig(projectDir, func(root *yaml.Node) error {
		node := mappingValue(root, "environments")
		if node == nil || node.Kind != yaml.MappingNode {
			return unknownEnvironment(name, cat.Names())
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == name {
				node.Content = append(node.Content[:i], node.Content[i+2:]...)
				return nil
			}
		}
		return unknownEnvironment(name, cat.Names())
	}); err != nil {
		return cat, err
	}
	return Load(projectDir)
}

// RenderAdd validates one alias and returns the exact YAML section Add would
// write, without touching the filesystem. It never reads env-file values.
func RenderAdd(projectDir string, env core.Environment) (string, error) {
	prepared, err := prepareEnvironment(projectDir, env)
	if err != nil {
		return "", err
	}
	cat, err := Load(projectDir)
	if err != nil {
		return "", err
	}
	if _, exists := cat.Find(prepared.Name); exists {
		return "", &InputError{Message: fmt.Sprintf(msgWriteDuplicateAlias, prepared.Name)}
	}
	root := &yaml.Node{
		Kind: yaml.MappingNode, Tag: "!!map",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "environments"},
			{
				Kind: yaml.MappingNode, Tag: "!!map",
				Content: []*yaml.Node{scalarNode(prepared.Name), sectionNode(prepared)},
			},
		},
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return "", invalidConfig(msgConfigEncode, err.Error())
	}
	if err := enc.Close(); err != nil {
		return "", invalidConfig(msgConfigEncode, err.Error())
	}
	return buf.String(), nil
}

func prepareEnvironment(projectDir string, env core.Environment) (core.Environment, error) {
	name := strings.TrimSpace(env.Name)
	if name == "" {
		return core.Environment{}, &InputError{Message: msgWriteNameMissing}
	}
	if err := validateAlias(name); err != nil {
		return core.Environment{}, &InputError{
			Message: fmt.Sprintf(msgWriteAliasInvalid, name),
			Detail:  err.Error(),
		}
	}
	files, err := normalizeFiles(projectDir, env.Files, msgWriteComposeLabel)
	if err != nil {
		return core.Environment{}, err
	}
	envFiles, err := normalizeFiles(projectDir, env.EnvFiles, msgWriteEnvFileLabel)
	if err != nil {
		return core.Environment{}, err
	}
	profiles, err := normalizeProfiles(env.Profiles)
	if err != nil {
		return core.Environment{}, err
	}
	if len(files) == 0 && len(envFiles) == 0 && len(profiles) == 0 {
		return core.Environment{}, &InputError{Message: fmt.Sprintf(msgWriteNoInputs, name)}
	}
	return core.Environment{Name: name, Files: files, EnvFiles: envFiles, Profiles: profiles}, nil
}

func normalizeFiles(projectDir string, values []string, label string) ([]string, error) {
	stored := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, &InputError{Message: fmt.Sprintf(msgWriteListEmpty, label)}
		}
		path := value
		if !filepath.IsAbs(path) {
			path = filepath.Join(projectDir, path)
		}
		path = filepath.Clean(path)
		if seen[path] {
			return nil, &InputError{Message: fmt.Sprintf(msgWriteListDuplicate, label, value)}
		}
		seen[path] = true
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, &InputError{Message: fmt.Sprintf(msgWriteFileMissing, label, value), Detail: path}
			}
			return nil, &InputError{Message: fmt.Sprintf(msgWriteFileUnreadable, label, value), Detail: err.Error()}
		}
		if !info.Mode().IsRegular() {
			return nil, &InputError{Message: fmt.Sprintf(msgWriteFileNotRegular, label, value), Detail: path}
		}
		stored = append(stored, storedPath(projectDir, path))
	}
	return stored, nil
}

func normalizeProfiles(values []string) ([]string, error) {
	profiles := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, raw := range values {
		profile := strings.TrimSpace(raw)
		if profile == "" {
			return nil, &InputError{Message: fmt.Sprintf(msgWriteListEmpty, msgWriteProfileLabel)}
		}
		if strings.ContainsAny(profile, " \t") {
			return nil, &InputError{Message: fmt.Sprintf(msgWriteProfileSpace, profile)}
		}
		if seen[profile] {
			return nil, &InputError{Message: fmt.Sprintf(msgWriteListDuplicate, msgWriteProfileLabel, profile)}
		}
		seen[profile] = true
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

// storedPath keeps team-shareable paths relative to the project folder. A file
// outside the project is stored as an absolute path; dcraft never copies it.
func storedPath(projectDir, path string) string {
	rel, err := filepath.Rel(projectDir, path)
	if err == nil {
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return path
}

func sectionNode(env core.Environment) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if len(env.Files) > 0 {
		node.Content = append(node.Content, scalarNode("compose_files"), stringSeq(env.Files))
	}
	if len(env.EnvFiles) > 0 {
		node.Content = append(node.Content, scalarNode("env_files"), stringSeq(env.EnvFiles))
	}
	if len(env.Profiles) > 0 {
		node.Content = append(node.Content, scalarNode("profiles"), stringSeq(env.Profiles))
	}
	return node
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func stringSeq(values []string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		node.Content = append(node.Content, scalarNode(value))
	}
	return node
}

func mutateConfig(projectDir string, mutate func(*yaml.Node) error) error {
	path := filepath.Join(projectDir, FileName)
	data, err := os.ReadFile(path)
	var doc *yaml.Node
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return invalidConfig(msgConfigUnreadable, err.Error())
		}
		doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	} else {
		doc, err = decodeDoc(data)
		if err != nil {
			return invalidConfig(msgConfigMalformed, err.Error())
		}
		if doc == nil {
			doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
		}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return invalidConfig(msgConfigRoot, lineOf(root))
	}
	if err := mutate(root); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return invalidConfig(msgConfigEncode, err.Error())
	}
	if err := enc.Close(); err != nil {
		return invalidConfig(msgConfigEncode, err.Error())
	}
	return replaceFile(path, buf.Bytes())
}

func decodeDoc(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	if doc.Content[0].Tag == "!!null" {
		return nil, nil
	}
	return &doc, nil
}

func replaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".dcraft-environments-*.tmp")
	if err != nil {
		return invalidConfig(msgWriteFailed, err.Error())
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return invalidConfig(msgWriteFailed, err.Error())
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return invalidConfig(msgWriteFailed, err.Error())
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return invalidConfig(msgWriteFailed, err.Error())
	}
	if err := tmp.Close(); err != nil {
		return invalidConfig(msgWriteFailed, err.Error())
	}
	if err := os.Rename(tmpName, path); err != nil {
		return invalidConfig(msgWriteFailed, err.Error())
	}
	// Best effort: make the rename durable without turning a completed
	// replacement into a reported failure.
	if handle, err := os.Open(dir); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}

func unknownEnvironment(name string, available []string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryEnvironmentUnknown,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf(msgWriteRemoveUnknown, name),
		Detail:      strings.Join(available, ", "),
		NextActions: []string{msgWriteRemoveActionOne, msgWriteRemoveActionTwo},
	})
}
