// Package projectconfig owns the single read-modify-write path for the
// shareable project-local .dcraft.yaml. Every feature that edits a section
// (secrets, hooks, environments, ...) goes through Update so one project file
// has one writer, one lock and one atomic replacement rule.
//
// The document is edited as YAML nodes: unknown top-level sections, other
// features' sections and attached comments survive, and the existing file mode
// is preserved. Resolved secret values never pass through this package.
package projectconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go.yaml.in/yaml/v3"
)

// FileName is the project-local dcraft configuration file.
const FileName = ".dcraft.yaml"

// Kind classifies a configuration failure so a caller can keep its own
// diagnostic category and user-facing wording.
type Kind string

const (
	KindUnreadable  Kind = "unreadable"
	KindMalformed   Kind = "malformed"
	KindRoot        Kind = "root"
	KindEncode      Kind = "encode"
	KindWrite       Kind = "write"
	KindLockFailed  Kind = "lock_failed"
	KindLockBusy    Kind = "lock_busy"
	KindUnsupported Kind = "unsupported"
)

// Error is a configuration failure. Detail is safe to show as diagnostic
// detail; it never contains a resolved value because this package only reads
// and writes references.
type Error struct {
	Kind   Kind
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return string(e.Kind)
	}
	return string(e.Kind) + ": " + e.Detail
}

// ErrNoChange lets a mutate function confirm that the document is already in
// the requested state. Update then skips the rewrite so an idempotent add does
// not touch the file.
var ErrNoChange = errors.New("project config unchanged")

// processMu serializes goroutines in this process. The cross-process lock is
// the project-local .dcraft.lock flock; both are required because a flock is
// not an in-process mutex on every platform.
var processMu sync.Mutex

// Update performs one locked read-modify-write of <projectDir>/.dcraft.yaml.
// The mutation runs on the freshly loaded document while the lock is held, so a
// concurrent writer from another process cannot be lost. Any error returned by
// mutate aborts the write and leaves the previous file byte-identical.
func Update(projectDir string, mutate func(root *yaml.Node) error) error {
	if mutate == nil {
		return errors.New("project config mutation is required")
	}
	processMu.Lock()
	defer processMu.Unlock()

	release, err := acquireLock(projectDir)
	if err != nil {
		return err
	}
	defer release()

	path := filepath.Join(projectDir, FileName)
	doc, err := loadDocument(path)
	if err != nil {
		return err
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return &Error{Kind: KindRoot, Detail: LineOf(FileName, root)}
	}
	if err := mutate(root); err != nil {
		if errors.Is(err, ErrNoChange) {
			return nil
		}
		return err
	}
	data, err := Encode(doc)
	if err != nil {
		return err
	}
	return replaceFile(path, data)
}

// LoadDocument reads and decodes the project file. A missing or empty file
// yields an empty mapping document, which is the same starting point an Add
// uses. It does not take the writer lock: callers that only read keep their
// existing read-only behavior.
func LoadDocument(projectDir string) (*yaml.Node, error) {
	return loadDocument(filepath.Join(projectDir, FileName))
}

func loadDocument(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyDocument(), nil
		}
		return nil, &Error{Kind: KindUnreadable, Detail: err.Error()}
	}
	doc, err := Decode(data)
	if err != nil {
		return nil, &Error{Kind: KindMalformed, Detail: err.Error()}
	}
	if doc == nil {
		return emptyDocument(), nil
	}
	return doc, nil
}

func emptyDocument() *yaml.Node {
	return &yaml.Node{
		Kind:    yaml.DocumentNode,
		Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}},
	}
}

// Decode parses one YAML document. An empty document or a null root returns
// nil so a caller can treat the file as empty.
func Decode(data []byte) (*yaml.Node, error) {
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

// Encode renders a document with the project's two-space indentation.
func Encode(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, &Error{Kind: KindEncode, Detail: err.Error()}
	}
	if err := enc.Close(); err != nil {
		return nil, &Error{Kind: KindEncode, Detail: err.Error()}
	}
	return buf.Bytes(), nil
}

// replaceFile writes a temp file in the destination directory, fsyncs it, and
// renames it over the project file. The old file mode is preserved; a new file
// is private (0600). A failure or cancellation before the rename leaves the
// previous file byte-identical.
func replaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".dcraft-*.tmp")
	if err != nil {
		return &Error{Kind: KindWrite, Detail: err.Error()}
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return &Error{Kind: KindWrite, Detail: err.Error()}
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return &Error{Kind: KindWrite, Detail: err.Error()}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return &Error{Kind: KindWrite, Detail: err.Error()}
	}
	if err := tmp.Close(); err != nil {
		return &Error{Kind: KindWrite, Detail: err.Error()}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return &Error{Kind: KindWrite, Detail: err.Error()}
	}
	// Best effort: make the rename durable without turning a completed
	// replacement into a reported failure.
	if handle, err := os.Open(dir); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}

// MappingValue returns the value node of key in a mapping node, or nil.
func MappingValue(node *yaml.Node, key string) *yaml.Node {
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

// MappingKey returns the index of key in a mapping node, or -1.
func MappingKey(node *yaml.Node, key string) int {
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

// LineOf formats a source location for a diagnostic detail.
func LineOf(file string, node *yaml.Node) string {
	if node == nil {
		return file
	}
	return fmt.Sprintf("%s:%d", file, node.Line)
}

// ResolveAlias follows YAML aliases up to a small bound.
func ResolveAlias(node *yaml.Node) *yaml.Node {
	for i := 0; node != nil && node.Kind == yaml.AliasNode && i < 8; i++ {
		node = node.Alias
	}
	return node
}
