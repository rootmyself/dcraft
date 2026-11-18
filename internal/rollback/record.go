package rollback

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/state"
)

// recordedTarget rebuilds the effective Compose execution target from the
// record. The recorded file order, env files, profiles, project name and
// environment win over the current discovery: rollback reproduces the
// deployment update verified, not today's selection.
func recordedTarget(rec state.GoodVersion) core.Target {
	return core.Target{
		ProjectDir:     rec.Target.ProjectDir,
		Files:          append([]string{}, rec.Target.Files...),
		EnvFiles:       append([]string{}, rec.Target.EnvFiles...),
		Profiles:       append([]string{}, rec.Target.Profiles...),
		ProjectName:    rec.Target.ProjectName,
		Environment:    rec.Target.Environment,
		DefaultEnvFile: rec.Target.DefaultEnvFile,
	}
}

// validateRecord confirms the stored record belongs to this stack and
// environment and carries usable immutable identities. A malformed record is
// never repaired or guessed around; update is the only writer.
func validateRecord(rec state.GoodVersion, current core.Target) error {
	fail := func(detail string) error {
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryRollbackRecordInvalid,
			Severity:    core.SeverityError,
			Message:     msgRecordInvalid,
			Detail:      detail,
			NextActions: []string{msgRecordAct1, msgRecordAct2},
		})
	}
	if rec.Version > state.GoodVersionVersion {
		return fail(fmt.Sprintf("record format %d is newer than this dcraft (%d)", rec.Version, state.GoodVersionVersion))
	}
	if strings.TrimSpace(rec.Target.ProjectDir) == "" {
		return fail("record has no project folder")
	}
	if !sameFolder(rec.Target.ProjectDir, current.ProjectDir) {
		return fail(fmt.Sprintf("record folder %s does not match target folder %s", rec.Target.ProjectDir, current.ProjectDir))
	}
	if rec.Target.Environment != current.Environment {
		return fail(fmt.Sprintf("record environment %q does not match selected environment %q", rec.Target.Environment, current.Environment))
	}
	if len(rec.Target.Files) == 0 {
		return fail("record has no Compose files")
	}
	if strings.TrimSpace(rec.Target.ProjectName) == "" {
		return fail("record has no effective project name")
	}
	if len(rec.Services) == 0 {
		return fail("record has no service image identities")
	}
	seen := map[string]bool{}
	for _, svc := range rec.Services {
		name := strings.TrimSpace(svc.Service)
		if name == "" {
			return fail("record has a service without a name")
		}
		if seen[name] {
			return fail("record has duplicate service " + name)
		}
		seen[name] = true
		if !validImageID(svc.ImageID) {
			return fail("service " + name + " has an invalid image ID: " + svc.ImageID)
		}
		if !validRetentionTag(svc.ImageID, svc.RetentionTag) {
			return fail("service " + name + " has an invalid retention tag: " + svc.RetentionTag)
		}
	}
	return nil
}

// validImageID accepts exactly the immutable identity update records: a full
// sha256: ID, never a movable tag or a short ID.
func validImageID(id string) bool {
	if !strings.HasPrefix(id, "sha256:") || len(id) != len("sha256:")+64 {
		return false
	}
	for _, r := range strings.TrimPrefix(id, "sha256:") {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// validRetentionTag confirms the tag has the dcraft-owned shape and names the
// recorded image ID. Whether the tag still resolves to that ID is checked
// against the engine, not by string comparison.
func validRetentionTag(imageID, tag string) bool {
	if !strings.HasPrefix(tag, "dcraft-good/") {
		return false
	}
	repo, ref, ok := strings.Cut(tag, ":")
	if !ok || repo == "" || ref == "" {
		return false
	}
	if len(ref) != 16 {
		return false
	}
	return strings.HasPrefix(strings.TrimPrefix(imageID, "sha256:"), ref)
}

func sameFolder(a, b string) bool {
	ca, errA := filepath.EvalSymlinks(a)
	cb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return filepath.Clean(ca) == filepath.Clean(cb)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func noRecordDiagnostic(target core.Target) core.Diagnostic {
	detail := target.ProjectDir
	if target.Environment != "" {
		detail += " (환경 " + target.Environment + ")"
	}
	return core.Diagnostic{
		Category:    core.CategoryRollbackUnavailable,
		Severity:    core.SeverityError,
		Message:     msgNoRecord,
		Detail:      detail,
		NextActions: []string{msgNoRecordAct1, msgNoRecordAct2, msgNoRecordAct3},
	}
}

func stackBusyDiagnostic(busy *state.BusyError) core.Diagnostic {
	detail := busy.Error()
	if busy.HasHolder && busy.Holder.StartedAt.IsZero() {
		detail = fmt.Sprintf("진행 중인 작업: %s (PID %d)", busy.Holder.Action, busy.Holder.PID)
	}
	return core.Diagnostic{
		Category:    core.CategoryStackBusy,
		Severity:    core.SeverityError,
		Message:     msgStackBusy,
		Detail:      detail,
		NextActions: []string{msgStackBusyAct1, msgStackBusyAct2},
	}
}

func stateFailedDiagnostic(err error) core.Diagnostic {
	return core.Diagnostic{
		Category:    core.CategoryStateFailed,
		Severity:    core.SeverityError,
		Message:     msgStateFailed,
		Detail:      truncate(err.Error(), 500),
		NextActions: []string{msgStateAct1, msgStateAct2},
	}
}

func truncate(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return text[:max] + "…"
}
