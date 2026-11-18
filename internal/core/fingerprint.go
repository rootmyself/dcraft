package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/rootmyself/dcraft/internal/fsidentity"
)

// PlanStateReader is the optional state-store capability that binds an
// approved plan to the name map it was previewed against. A store that does
// not implement it contributes an empty state fingerprint (tests and
// alternate wiring keep the previous behavior).
type PlanStateReader interface {
	PlanStateFingerprint(target Target) string
}

// planFingerprintVersion is part of the fingerprint input. Change it when the
// meaning of the covered inputs changes, never when only the hash algorithm
// detail changes silently.
const planFingerprintVersion = 1

// planFingerprint identifies the exact resolved inputs a plan was previewed
// against: the target fields, the effective Compose/env files and
// `.dcraft.yaml` content, the resolved identity/project name and the state
// name map. Run recomputes it before the feature starts; a different value
// means the approval no longer describes what would run.
func (e *Engine) planFingerprint(feature string, target Target) string {
	doc := planFingerprintDoc{
		Version:         planFingerprintVersion,
		Feature:         feature,
		ProjectIdentity: fsidentity.Key(target.ProjectDir),
		Environment:     target.Environment,
		ProjectName:     target.ProjectName,
		Profiles:        append([]string{}, target.Profiles...),
		Files:           fingerprintFiles(target.Files),
		EnvFiles:        fingerprintFiles(target.EnvFiles),
		DefaultEnvFile:  fingerprintFile(target.DefaultEnvFile),
		ProjectConfig:   fingerprintFile(filepath.Join(target.ProjectDir, ".dcraft.yaml")),
	}
	if reader, ok := e.State.(PlanStateReader); ok {
		doc.NameState = reader.PlanStateFingerprint(target)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type planFingerprintDoc struct {
	Version         int               `json:"version"`
	Feature         string            `json:"feature"`
	ProjectIdentity string            `json:"project_identity"`
	Environment     string            `json:"environment"`
	ProjectName     string            `json:"project_name"`
	Profiles        []string          `json:"profiles"`
	Files           []fileFingerprint `json:"files"`
	EnvFiles        []fileFingerprint `json:"env_files"`
	DefaultEnvFile  fileFingerprint   `json:"default_env_file"`
	ProjectConfig   fileFingerprint   `json:"project_config"`
	NameState       string            `json:"name_state,omitempty"`
}

type fileFingerprint struct {
	Path   string `json:"path"`
	State  string `json:"state"`
	SHA256 string `json:"sha256,omitempty"`
}

func fingerprintFiles(paths []string) []fileFingerprint {
	out := make([]fileFingerprint, 0, len(paths))
	for _, path := range paths {
		out = append(out, fingerprintFile(path))
	}
	return out
}

func fingerprintFile(path string) fileFingerprint {
	if path == "" {
		return fileFingerprint{State: "none"}
	}
	fp := fileFingerprint{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fp.State = "missing"
		} else {
			fp.State = "unreadable"
		}
		return fp
	}
	fp.State = "present"
	sum := sha256.Sum256(data)
	fp.SHA256 = hex.EncodeToString(sum[:])
	return fp
}
