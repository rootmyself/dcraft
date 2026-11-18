// Package secretref owns the project-local secret reference configuration and
// the runtime delivery mechanics. It stores references only: never resolved
// values, never previous values and no secret store.
package secretref

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/rootmyself/dcraft/internal/core"
)

// Phase is the delivery phase of a reference. Only runtime is implemented in
// this ticket; build is part of the published contract for ticket #10.
type Phase string

const (
	PhaseRuntime Phase = "runtime"
	PhaseBuild   Phase = "build"
)

// Delivery is how the value reaches the service.
type Delivery string

const (
	// DeliveryEnvironment adds the value as a container environment variable.
	// Docker-authorized users can read it.
	DeliveryEnvironment Delivery = "environment"
	// DeliveryFile mounts the value at /run/secrets/<target>. The application
	// must read that path.
	DeliveryFile Delivery = "file"
)

// SourceType is where the reference resolves its value from.
type SourceType string

const (
	SourceEnv  SourceType = "env"
	SourceFile SourceType = "file"
	// SourceAWS is the published extension point for ticket #11. It is
	// recognized here so the contract is stable, but not implemented.
	SourceAWS SourceType = "aws"
)

// Reference is one saved connection. It contains only the service, phase,
// delivery, source type, source reference and target name.
type Reference struct {
	Service  string     `yaml:"service" json:"service"`
	Phase    Phase      `yaml:"phase" json:"phase"`
	Delivery Delivery   `yaml:"delivery" json:"delivery"`
	Source   SourceType `yaml:"source" json:"source"`
	Ref      string     `yaml:"ref" json:"ref"`
	Target   string     `yaml:"target,omitempty" json:"target"`
}

// TargetPath is the container path for file delivery. Environment delivery
// has no path.
func (r Reference) TargetPath() string {
	if r.Delivery != DeliveryFile {
		return ""
	}
	return "/run/secrets/" + r.EffectiveTarget()
}

// EffectiveTarget resolves the explicit target or derives one from the source
// reference. Environment delivery defaults to the source environment variable
// name. File delivery defaults to the source file's base name without its last
// extension.
func (r Reference) EffectiveTarget() string {
	if target := strings.TrimSpace(r.Target); target != "" {
		return target
	}
	if r.Delivery == DeliveryEnvironment {
		return strings.TrimSpace(r.Ref)
	}
	base := filepath.Base(strings.TrimSpace(r.Ref))
	if base == "." || base == ".." || base == string(filepath.Separator) || base == "" {
		return ""
	}
	if ext := filepath.Ext(base); ext != "" && ext != base {
		if trimmed := strings.TrimSuffix(base, ext); trimmed != "" {
			base = trimmed
		}
	}
	return base
}

// Key identifies one reference for duplicate detection: one target per
// service and delivery method.
func (r Reference) Key() string {
	return r.Service + "\x00" + string(r.Delivery) + "\x00" + r.EffectiveTarget()
}

func sameReference(a, b Reference) bool {
	return a.Service == b.Service &&
		a.Phase == b.Phase &&
		a.Delivery == b.Delivery &&
		a.Source == b.Source &&
		a.Ref == b.Ref &&
		a.EffectiveTarget() == b.EffectiveTarget()
}

// Catalog is the parsed secret section of one project. Path is the config file
// path that was read, whether or not it exists.
type Catalog struct {
	Path       string
	ProjectDir string
	References []Reference
}

// ForServices returns the references whose service is in services. An empty
// services list means the whole stack.
func (c Catalog) ForServices(services []string) []Reference {
	if len(services) == 0 {
		return append([]Reference{}, c.References...)
	}
	selected := make(map[string]bool, len(services))
	for _, name := range services {
		selected[name] = true
	}
	out := make([]Reference, 0, len(c.References))
	for _, ref := range c.References {
		if selected[ref.Service] {
			out = append(out, ref)
		}
	}
	return out
}

// ForPhase returns only the references for one delivery phase. Runtime and
// build references are prepared by separate lifecycles; a phase filter keeps
// one phase from being delivered at the other.
func (c Catalog) ForPhase(phase Phase) []Reference {
	out := make([]Reference, 0, len(c.References))
	for _, ref := range c.References {
		if ref.Phase == phase {
			out = append(out, ref)
		}
	}
	return out
}

// ForPhaseServices is ForPhase restricted to the selected services. An empty
// services list means the whole stack.
func (c Catalog) ForPhaseServices(phase Phase, services []string) []Reference {
	refs := c.ForPhase(phase)
	if len(services) == 0 {
		return refs
	}
	selected := make(map[string]bool, len(services))
	for _, name := range services {
		selected[name] = true
	}
	out := make([]Reference, 0, len(refs))
	for _, ref := range refs {
		if selected[ref.Service] {
			out = append(out, ref)
		}
	}
	return out
}

// HasPhase reports whether the catalog contains a reference for the phase.
func (c Catalog) HasPhase(phase Phase) bool {
	for _, ref := range c.References {
		if ref.Phase == phase {
			return true
		}
	}
	return false
}

// Has reports whether the catalog contains any reference.
func (c Catalog) Has() bool { return len(c.References) > 0 }

// ValidationError is one invalid field value. Unsupported distinguishes an
// enum value that is part of the published contract but not implemented yet
// from a value that is simply invalid.
type ValidationError struct {
	Field       string
	Reason      string
	Unsupported bool
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Reason
}

var (
	envNameRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	serviceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	secretNameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	controlRe     = regexp.MustCompile(`[\x00-\x1f\x7f]`)
)

// Validate checks one reference without touching the filesystem or Compose.
func Validate(r Reference) error {
	service := strings.TrimSpace(r.Service)
	if service == "" {
		return &ValidationError{Field: "service", Reason: "서비스 이름이 비어 있습니다."}
	}
	if !serviceNameRe.MatchString(service) || strings.ContainsAny(service, " \t") {
		return &ValidationError{Field: "service", Reason: "서비스 이름에 사용할 수 없는 문자가 있습니다: " + service}
	}

	switch r.Phase {
	case PhaseRuntime:
	case PhaseBuild:
		// Build values reach the Dockerfile only through a BuildKit secret
		// mount. There is no environment or ARG/ENV fallback.
		if r.Delivery != DeliveryFile {
			return &ValidationError{
				Field:  "delivery",
				Reason: "빌드 시점 비밀값은 BuildKit secret mount(file)로만 전달합니다. delivery를 file로 지정하거나 생략하세요.",
			}
		}
	default:
		return &ValidationError{Field: "phase", Reason: "지원하지 않는 단계입니다: " + string(r.Phase)}
	}

	switch r.Delivery {
	case DeliveryEnvironment, DeliveryFile:
	default:
		return &ValidationError{Field: "delivery", Reason: "delivery 값이 지원되지 않습니다: " + string(r.Delivery)}
	}

	switch r.Source {
	case SourceEnv:
		ref := strings.TrimSpace(r.Ref)
		if ref == "" {
			return &ValidationError{Field: "ref", Reason: "환경변수 이름이 비어 있습니다."}
		}
		if !envNameRe.MatchString(ref) {
			return &ValidationError{Field: "ref", Reason: "환경변수 이름이 올바르지 않습니다: " + ref}
		}
	case SourceFile:
		ref := strings.TrimSpace(r.Ref)
		if ref == "" {
			return &ValidationError{Field: "ref", Reason: "파일 경로가 비어 있습니다."}
		}
		if controlRe.MatchString(ref) {
			return &ValidationError{Field: "ref", Reason: "파일 경로에 제어 문자를 사용할 수 없습니다."}
		}
		if strings.HasSuffix(ref, string(filepath.Separator)) {
			return &ValidationError{Field: "ref", Reason: "파일 경로가 폴더로 끝납니다: " + ref}
		}
	case SourceAWS:
		return &ValidationError{Field: "source", Reason: "AWS Secrets Manager 원본은 아직 지원하지 않습니다.", Unsupported: true}
	default:
		return &ValidationError{Field: "source", Reason: "지원하지 않는 원본 종류입니다: " + string(r.Source)}
	}

	target := r.EffectiveTarget()
	if target == "" {
		return &ValidationError{Field: "target", Reason: "전달 대상 이름을 정할 수 없습니다. target을 지정하세요."}
	}
	if controlRe.MatchString(target) || strings.ContainsAny(target, " \t/\\") || target == "." || target == ".." {
		return &ValidationError{Field: "target", Reason: "target 이름이 올바르지 않습니다: " + target}
	}
	if r.Delivery == DeliveryEnvironment && !envNameRe.MatchString(target) {
		return &ValidationError{Field: "target", Reason: "target 환경변수 이름이 올바르지 않습니다: " + target}
	}
	if r.Delivery == DeliveryFile && !secretNameRe.MatchString(target) {
		return &ValidationError{Field: "target", Reason: "target 파일 비밀값 이름이 올바르지 않습니다: " + target}
	}
	return nil
}

// ValidateReference validates one reference for a user-facing add/save path and
// maps the field error to the stable input categories. Build-phase environment
// delivery and AWS are user input errors here; build-phase delivery is part of
// the contract but must use the BuildKit mount.
func ValidateReference(r Reference) error {
	if err := Validate(r); err != nil {
		return inputError(err)
	}
	return nil
}

// ValidateServices checks that every reference targets a configured Compose
// service. It returns a service_unknown error (exit 2) listing the available
// services.
func ValidateServices(cat Catalog, services []string) error {
	if services == nil {
		return nil
	}
	known := make(map[string]bool, len(services))
	for _, name := range services {
		known[name] = true
	}
	var unknown []string
	seen := map[string]bool{}
	for _, ref := range cat.References {
		if !known[ref.Service] && !seen[ref.Service] {
			unknown = append(unknown, ref.Service)
			seen[ref.Service] = true
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryServiceUnknown,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf(msgUnknownService, strings.Join(unknown, ", ")),
		Detail:      msgAvailableServices + strings.Join(services, ", "),
		NextActions: []string{msgUnknownServiceActionOne, msgUnknownServiceActionTwo},
	})
}

// Load reads <projectDir>/.dcraft.yaml and parses only its secrets section.
// Other top-level sections (environments, future features) are ignored here.
// A missing file is an empty catalog.
func Load(projectDir string) (Catalog, error) {
	path := filepath.Join(projectDir, ".dcraft.yaml")
	cat := Catalog{Path: path, ProjectDir: projectDir, References: []Reference{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cat, nil
		}
		return cat, configError(msgConfigUnreadable, err.Error())
	}
	doc, err := decodeDoc(data)
	if err != nil {
		return cat, configError(msgConfigMalformed, err.Error())
	}
	if doc == nil {
		return cat, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return cat, configError(msgConfigRoot, lineOf(root))
	}
	node := mappingValue(root, "secrets")
	if node == nil || node.Tag == "!!null" {
		return cat, nil
	}
	if node.Kind != yaml.SequenceNode {
		return cat, configError(msgSecretsNotList, lineOf(node))
	}
	refs, err := parseReferences(node)
	if err != nil {
		return cat, err
	}
	if err := validateCatalog(refs, core.CategorySecretInvalid); err != nil {
		return cat, err
	}
	for i := range refs {
		refs[i].Target = refs[i].EffectiveTarget()
	}
	cat.References = refs
	return cat, nil
}

// Add validates and appends one reference, then writes the secrets section
// back to .dcraft.yaml. It returns the updated catalog.
func Add(projectDir string, ref Reference) (Catalog, error) {
	cat, err := Load(projectDir)
	if err != nil {
		return cat, err
	}
	if err := Validate(ref); err != nil {
		return cat, inputError(err)
	}
	ref.Target = ref.EffectiveTarget()
	// Re-adding the same saved connection is idempotent: it must not create a
	// duplicate or change the stored reference.
	for _, existing := range cat.References {
		if sameReference(existing, ref) {
			return cat, nil
		}
	}
	refs := append([]Reference{}, cat.References...)
	refs = append(refs, ref)
	if err := validateCatalog(refs, core.CategorySecretInputInvalid); err != nil {
		return cat, err
	}
	if err := writeCatalog(projectDir, refs); err != nil {
		return cat, err
	}
	cat.References = refs
	return cat, nil
}

// Remove deletes one reference identified by service, delivery and target and
// writes the secrets section back. It returns the updated catalog. A missing
// reference is secret_input_invalid.
func Remove(projectDir, service string, delivery Delivery, target string) (Catalog, error) {
	cat, err := Load(projectDir)
	if err != nil {
		return cat, err
	}
	service = strings.TrimSpace(service)
	target = strings.TrimSpace(target)
	refs := make([]Reference, 0, len(cat.References))
	removed := false
	for _, ref := range cat.References {
		if ref.Service == service && ref.Delivery == delivery && ref.EffectiveTarget() == target {
			removed = true
			continue
		}
		refs = append(refs, ref)
	}
	if !removed {
		return cat, core.Fail(core.Diagnostic{
			Category:    core.CategorySecretInputInvalid,
			Severity:    core.SeverityError,
			Message:     fmt.Sprintf(msgRemoveNotFound, service, string(delivery), target),
			NextActions: []string{msgListAction},
		})
	}
	if err := writeCatalog(projectDir, refs); err != nil {
		return cat, err
	}
	cat.References = refs
	return cat, nil
}

// Write writes the given references as the secrets section while preserving
// every other section of .dcraft.yaml.
func Write(projectDir string, refs []Reference) error {
	if err := validateCatalog(refs, core.CategorySecretInvalid); err != nil {
		return err
	}
	for i := range refs {
		refs[i].Target = refs[i].EffectiveTarget()
	}
	return writeCatalog(projectDir, refs)
}

func validateCatalog(refs []Reference, category core.Category) error {
	seen := map[string]bool{}
	for i, ref := range refs {
		if err := Validate(ref); err != nil {
			var ve *ValidationError
			if errors.As(err, &ve) {
				if ve.Unsupported && category == core.CategorySecretInputInvalid {
					category = core.CategorySecretUnsupported
				}
				return core.Fail(core.Diagnostic{
					Category:    category,
					Severity:    core.SeverityError,
					Message:     ve.Reason,
					Detail:      fmt.Sprintf("secrets[%d] field=%s", i, ve.Field),
					NextActions: []string{msgInputActionOne, msgInputActionTwo},
				})
			}
			return inputOrConfigError(err, i)
		}
		key := ref.Key()
		if seen[key] {
			return core.Fail(core.Diagnostic{
				Category:    category,
				Severity:    core.SeverityError,
				Message:     fmt.Sprintf(msgDuplicateTarget, ref.Service, string(ref.Delivery), ref.EffectiveTarget()),
				NextActions: []string{msgDuplicateAction},
			})
		}
		seen[key] = true
	}
	return nil
}

func parseReferences(node *yaml.Node) ([]Reference, error) {
	refs := make([]Reference, 0, len(node.Content))
	for _, item := range node.Content {
		item := resolveAlias(item)
		if item == nil || item.Kind != yaml.MappingNode {
			return nil, configError(msgSecretNotMapping, lineOf(item))
		}
		values := map[string]string{}
		seen := map[string]int{}
		for i := 0; i+1 < len(item.Content); i += 2 {
			keyNode, valueNode := item.Content[i], item.Content[i+1]
			if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
				return nil, configError(msgSecretFieldKey, lineOf(keyNode))
			}
			field := keyNode.Value
			if !allowedReferenceFields[field] {
				return nil, configError(fmt.Sprintf(msgSecretUnknownField, field), lineOf(keyNode))
			}
			if prev, dup := seen[field]; dup {
				return nil, configError(fmt.Sprintf(msgSecretDuplicateField, field), fmt.Sprintf("%s and %s", lineOf(keyNode), fmt.Sprintf("%s:%d", ".dcraft.yaml", prev)))
			}
			seen[field] = keyNode.Line
			valueNode = resolveAlias(valueNode)
			if valueNode == nil || valueNode.Kind != yaml.ScalarNode || valueNode.Tag != "!!str" {
				return nil, configError(fmt.Sprintf(msgSecretFieldString, field), lineOf(valueNode))
			}
			values[field] = valueNode.Value
		}
		for _, required := range requiredReferenceFields {
			if _, ok := values[required]; !ok {
				return nil, configError(fmt.Sprintf(msgSecretFieldMissing, required), lineOf(item))
			}
		}
		ref := Reference{
			Service:  strings.TrimSpace(values["service"]),
			Phase:    Phase(strings.TrimSpace(values["phase"])),
			Delivery: Delivery(strings.TrimSpace(values["delivery"])),
			Source:   SourceType(strings.TrimSpace(values["source"])),
			Ref:      strings.TrimSpace(values["ref"]),
			Target:   strings.TrimSpace(values["target"]),
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

var (
	allowedReferenceFields = map[string]bool{
		"service": true, "phase": true, "delivery": true,
		"source": true, "ref": true, "target": true,
	}
	requiredReferenceFields = []string{"service", "phase", "delivery", "source", "ref"}
)

func writeCatalog(projectDir string, refs []Reference) error {
	path := filepath.Join(projectDir, ".dcraft.yaml")
	data, err := os.ReadFile(path)
	var doc *yaml.Node
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return configError(msgConfigUnreadable, err.Error())
		}
		doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	} else {
		doc, err = decodeDoc(data)
		if err != nil {
			return configError(msgConfigMalformed, err.Error())
		}
		if doc == nil {
			doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
		}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return configError(msgConfigRoot, lineOf(root))
	}

	key := mappingKey(root, "secrets")
	if len(refs) == 0 {
		if key >= 0 {
			root.Content = append(root.Content[:key], root.Content[key+2:]...)
		}
	} else {
		value := &yaml.Node{}
		if err := value.Encode(refs); err != nil {
			return configError(msgConfigEncode, err.Error())
		}
		if key >= 0 {
			root.Content[key+1] = value
		} else {
			root.Content = append(root.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "secrets"},
				value,
			)
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return configError(msgConfigEncode, err.Error())
	}
	if err := enc.Close(); err != nil {
		return configError(msgConfigEncode, err.Error())
	}
	return replaceFile(path, buf.Bytes())
}

func replaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".dcraft-*.tmp")
	if err != nil {
		return configError(msgConfigWrite, err.Error())
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		// Preserve the project's existing file mode. The config contains
		// references only, so a team-shared 0644 file is normal.
		mode = info.Mode().Perm()
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return configError(msgConfigWrite, err.Error())
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return configError(msgConfigWrite, err.Error())
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return configError(msgConfigWrite, err.Error())
	}
	if err := tmp.Close(); err != nil {
		return configError(msgConfigWrite, err.Error())
	}
	if err := os.Rename(tmpName, path); err != nil {
		return configError(msgConfigWrite, err.Error())
	}
	return nil
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

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func mappingKey(node *yaml.Node, key string) int {
	if node == nil || node.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func lineOf(node *yaml.Node) string {
	if node == nil {
		return ".dcraft.yaml"
	}
	return fmt.Sprintf(".dcraft.yaml:%d", node.Line)
}

func resolveAlias(node *yaml.Node) *yaml.Node {
	for i := 0; node != nil && node.Kind == yaml.AliasNode && i < 8; i++ {
		node = node.Alias
	}
	return node
}

func configError(message, detail string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategorySecretInvalid,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      detail,
		NextActions: []string{msgConfigActionOne, msgConfigActionTwo},
	})
}

func inputError(err error) *core.Error {
	var ve *ValidationError
	if errors.As(err, &ve) {
		category := core.CategorySecretInputInvalid
		if ve.Unsupported {
			category = core.CategorySecretUnsupported
		}
		return core.Fail(core.Diagnostic{
			Category:    category,
			Severity:    core.SeverityError,
			Message:     ve.Reason,
			Detail:      "field=" + ve.Field,
			NextActions: []string{msgInputActionOne, msgInputActionTwo},
		})
	}
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategorySecretInputInvalid,
		Severity:    core.SeverityError,
		Message:     msgInputInvalid,
		NextActions: []string{msgInputActionOne, msgInputActionTwo},
	})
}

func inputOrConfigError(err error, index int) *core.Error {
	wrapped := inputError(err)
	wrapped.Diagnostics[0].Category = core.CategorySecretInvalid
	wrapped.Diagnostics[0].Detail = fmt.Sprintf("secrets[%d] %s", index, wrapped.Diagnostics[0].Detail)
	return wrapped
}
