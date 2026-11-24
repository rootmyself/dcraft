package exec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
)

// Recent commands are the attach screen's memory of what the reader ran in this
// project before. The store lives inside the project folder so it never mixes
// projects, and it is dcraft-owned so a reader can see and remove it.
const (
	// historyDirName is dcraft's own folder inside the project. The dot keeps
	// it out of the way and marks it as tool state rather than project sources.
	historyDirName = ".dcraft"
	// historyFileName is the file inside that folder.
	historyFileName = "exec-history.json"
	// historyLimit is how many recent commands the screen offers.
	historyLimit = 5
	// historyIgnoreLine is the ignore rule dcraft tells the reader to add the
	// first time it creates the file, so a command that carries a credential
	// cannot be committed by accident.
	historyIgnoreLine = ".dcraft/"
)

// History is the project-local list of recent attach commands, newest first.
// It stores exactly what the reader typed; dcraft never adds a secret of its
// own.
type History struct {
	// ProjectDir is the folder the store belongs to.
	ProjectDir string
	// Commands is newest first and de-duplicated.
	Commands []string
	// created reports that this load created the file, so the caller can print
	// the ignore guidance once.
	created bool
}

// HistoryPath is the store location for one project.
func HistoryPath(projectDir string) string {
	return filepath.Join(projectDir, historyDirName, historyFileName)
}

// LoadHistory reads the project's recent commands. A missing file is an empty
// history, not an error: the first attach is the common case. A corrupt file is
// reported, because silently forgetting a reader's commands is worse than
// stating that the store could not be read.
//
// create makes LoadHistory create the folder and an empty file when they are
// absent, which is when the caller prints the ignore guidance.
func LoadHistory(projectDir string, create bool) (History, error) {
	h := History{ProjectDir: projectDir, Commands: []string{}}
	path := HistoryPath(projectDir)
	data, err := os.ReadFile(path)
	if err == nil {
		var stored struct {
			Commands []string `json:"commands"`
		}
		if jsonErr := json.Unmarshal(data, &stored); jsonErr != nil {
			return h, fmt.Errorf("최근 명령 기록을 읽지 못함: %s", path)
		}
		h.Commands = dedupeCommands(stored.Commands)
		return h, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return h, fmt.Errorf("최근 명령 기록을 읽지 못함: %s", path)
	}
	if !create {
		return h, nil
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o700); mkdirErr != nil {
		return h, fmt.Errorf("최근 명령 기록을 만들지 못함: %s", path)
	}
	h.created = true
	if saveErr := h.save(); saveErr != nil {
		return h, saveErr
	}
	return h, nil
}

// Remember puts one command at the front, de-duplicated and capped at
// historyLimit.
func (h *History) Remember(command []string) error {
	line := strings.Join(command, " ")
	if strings.TrimSpace(line) == "" {
		return nil
	}
	next := []string{line}
	for _, existing := range h.Commands {
		if existing != line {
			next = append(next, existing)
		}
	}
	if len(next) > historyLimit {
		next = next[:historyLimit]
	}
	h.Commands = next
	return h.save()
}

// Delete removes one stored command. A command that is not stored is not an
// error: the screen acts on what it drew.
func (h *History) Delete(command string) error {
	next := make([]string, 0, len(h.Commands))
	for _, existing := range h.Commands {
		if existing != command {
			next = append(next, existing)
		}
	}
	h.Commands = next
	return h.save()
}

// save writes the store atomically so an interrupted write cannot leave a
// half-written history behind.
func (h *History) save() error {
	path := HistoryPath(h.ProjectDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("최근 명령 기록을 만들지 못함: %s", path)
	}
	body, err := json.MarshalIndent(struct {
		Commands []string `json:"commands"`
	}{Commands: h.Commands}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".exec-history-*.tmp")
	if err != nil {
		return fmt.Errorf("최근 명령 기록을 만들지 못함: %s", path)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("최근 명령 기록을 만들지 못함: %s", path)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("최근 명령 기록을 만들지 못함: %s", path)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("최근 명령 기록을 만들지 못함: %s", path)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("최근 명령 기록을 만들지 못함: %s", path)
	}
	return nil
}

// IgnoreGuidance is the one-time instruction the screen prints when it first
// creates the store.
func (h History) IgnoreGuidance() string {
	if !h.created {
		return ""
	}
	return fmt.Sprintf(msgHistoryIgnore, historyIgnoreLine)
}

func dedupeCommands(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if len(out) == historyLimit {
			break
		}
	}
	return out
}

// trackingHistory keeps the history load off the attach path's error channel:
// a store that cannot be read is reported on screen and the attach continues.
func trackingHistory(projectDir string) (History, string) {
	history, err := LoadHistory(projectDir, true)
	if err != nil {
		return History{ProjectDir: projectDir}, err.Error()
	}
	return history, ""
}

var _ = core.SchemaVersion
