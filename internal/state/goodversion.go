package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

// GoodVersionVersion is the on-disk format version of the previous-good
// deployment record. Later versions may add fields; a record written by a newer
// dcraft is refused instead of being misinterpreted.
const GoodVersionVersion = 1

// GoodVersion is the single previous good deployment dcraft keeps per stack and
// environment. Ticket #12 (rollback) consumes it. It records immutable image
// identities and the original execution references; it never contains resolved
// secret values.
type GoodVersion struct {
	Version     int           `json:"version"`
	RecordedAt  time.Time     `json:"recorded_at"`
	OperationID string        `json:"operation_id"`
	Target      GoodTarget    `json:"target"`
	Services    []GoodService `json:"services"`
}

// GoodTarget is the reproducible original execution configuration: the Compose
// file list in order, explicit env files, profiles, effective project name,
// environment alias and secret references. The default .env is informational
// only; Compose re-applies it from the folder.
type GoodTarget struct {
	ProjectDir     string            `json:"project_dir"`
	Files          []string          `json:"files"`
	EnvFiles       []string          `json:"env_files"`
	Profiles       []string          `json:"profiles"`
	ProjectName    string            `json:"project_name"`
	Environment    string            `json:"environment"`
	DefaultEnvFile string            `json:"default_env_file,omitempty"`
	SecretRefs     []SecretReference `json:"secret_refs"`
}

// SecretReference identifies a secret source for re-resolution at rollback
// time. Only references are stored. Ticket #9/#10 own the secret source
// contract; this neutral shape keeps the record reproducible without storing
// values or inventing a second secret store.
type SecretReference struct {
	Service  string `json:"service,omitempty"`
	Phase    string `json:"phase,omitempty"`
	Delivery string `json:"delivery,omitempty"`
	Source   string `json:"source,omitempty"`
	// Name is the source reference: an environment variable name or a
	// project-relative/absolute file path.
	Name string `json:"name"`
	// Target is the delivery target: an environment variable name or the
	// /run/secrets file name. It is recorded so a later rollback can explain
	// and reproduce the same delivery without reading stale configuration.
	Target string `json:"target,omitempty"`
	// Field is reserved for structured sources such as AWS Secrets Manager.
	Field string `json:"field,omitempty"`
}

// GoodService is the recorded image identity of one service. ImageID is the
// authoritative immutable identity (the full sha256:... image ID of the running
// container). RetentionTag is a dcraft-owned tag that keeps resolving to that
// image after the container is replaced; a movable service tag alone must never
// be used as the recovery target.
type GoodService struct {
	Service      string `json:"service"`
	ImageID      string `json:"image_id"`
	ImageRef     string `json:"image_ref,omitempty"`
	RetentionTag string `json:"retention_tag"`
	Build        bool   `json:"build"`
	Healthcheck  bool   `json:"healthcheck"`
}

const goodVersionFile = "state.json"

// GoodVersionPath is where the previous-good record lives for a target.
func (s *Store) GoodVersionPath(target core.Target) (string, error) {
	dir, err := s.StackDir(target)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, goodVersionFile), nil
}

// LoadGoodVersion reads the previous-good record. ok is false when no update
// has recorded one yet; a pre-existing stack therefore has no invented history.
func (s *Store) LoadGoodVersion(target core.Target) (GoodVersion, bool, error) {
	path, err := s.GoodVersionPath(target)
	if err != nil {
		return GoodVersion{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return GoodVersion{}, false, nil
		}
		return GoodVersion{}, false, fmt.Errorf("read good version: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return GoodVersion{}, false, nil
	}
	var rec GoodVersion
	if err := json.Unmarshal(data, &rec); err != nil {
		return GoodVersion{}, false, fmt.Errorf("parse good version: %w", err)
	}
	if rec.Version > GoodVersionVersion {
		return GoodVersion{}, false, fmt.Errorf("good version record format %d is newer than this dcraft", rec.Version)
	}
	if rec.Version == 0 {
		rec.Version = GoodVersionVersion
	}
	return rec, true, nil
}

// SaveGoodVersion replaces the previous-good record atomically. The caller must
// hold the per-stack change lock (state.AcquireStackLock), so concurrent
// updates, rollbacks or backups cannot replace it out of order.
func (s *Store) SaveGoodVersion(target core.Target, rec GoodVersion) error {
	path, err := s.GoodVersionPath(target)
	if err != nil {
		return err
	}
	if rec.Version == 0 {
		rec.Version = GoodVersionVersion
	}
	if rec.Version > GoodVersionVersion {
		return fmt.Errorf("refuse to write good version format %d", rec.Version)
	}
	if rec.RecordedAt.IsZero() {
		rec.RecordedAt = time.Now().UTC()
	}
	if rec.Target.Files == nil {
		rec.Target.Files = []string{}
	}
	if rec.Target.EnvFiles == nil {
		rec.Target.EnvFiles = []string{}
	}
	if rec.Target.Profiles == nil {
		rec.Target.Profiles = []string{}
	}
	if rec.Target.SecretRefs == nil {
		rec.Target.SecretRefs = []SecretReference{}
	}
	if rec.Services == nil {
		rec.Services = []GoodService{}
	}

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode good version: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create stack state dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create good version temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("good version permissions: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write good version: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync good version: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close good version: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace good version: %w", err)
	}
	return nil
}
