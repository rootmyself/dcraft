package compose

import (
	"context"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/stack"
)

// ApplyRollback implements stack.Rollbacker. It writes a dcraft-managed
// override that pins every recorded service to its exact image ID, prepares
// exactly the secret references stored in the record (never current project
// configuration), and applies detached with --pull never and --no-build. Compose therefore cannot pull or
// build a different version; a missing image fails the apply instead of being
// replaced.
func (c *Compose) ApplyRollback(ctx context.Context, t core.Target, services []string, pins []stack.RollbackPin, refs []secretref.Reference) (stack.OpResult, error) {
	if len(pins) == 0 {
		return stack.OpResult{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgRollbackNoPins,
			Detail:      "no recorded image pins",
			NextActions: []string{msgRollbackNoPinsAct},
		})
	}
	var prep secretref.Session
	if len(refs) > 0 {
		if c.secrets == nil {
			return stack.OpResult{}, secretref.Unavailable()
		}
		var err error
		prep, err = c.secrets.PrepareReferences(ctx, t, refs, services, secretref.ModeCreate)
		if err != nil {
			return stack.OpResult{}, err
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		prep.Abort()
		return stack.OpResult{}, ctxErr
	}
	overridePath, cleanup, err := writeRollbackOverride(pins)
	if err != nil {
		prep.Abort()
		return stack.OpResult{}, err
	}
	defer cleanup()

	files := append([]string{overridePath}, prep.Files...)
	args := composeArgsWith(t, files, append([]string{"up", "--detach", "--pull", "never", "--no-build"}, services...)...)
	res, err := c.run(ctx, execx.Command{Name: "docker", Args: args, Dir: t.ProjectDir, Env: composeEnv(prep.Env)})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return stack.OpResult{Output: prep.Redact(summarize(res.Combined())), Notices: prep.Notices}, ctxErr
	}
	if err != nil {
		// The Compose process never started, so no container can reference
		// the prepared materialization.
		prep.Abort()
		return stack.OpResult{}, err
	}
	out := stack.OpResult{Output: prep.Redact(summarize(res.Combined())), Notices: prep.Notices}
	if !res.Succeeded() {
		// Compose ran and may have partially recreated containers; keep the
		// materialized secret files a restart may still need.
		return out, fmt.Errorf("docker compose up exited with status %d", res.ExitCode)
	}
	prep.Commit()
	return out, nil
}

type rollbackOverrideDoc struct {
	Services map[string]rollbackOverrideService `yaml:"services"`
}

type rollbackOverrideService struct {
	Image string `yaml:"image"`
}

// writeRollbackOverride writes the pin file outside the project folder and
// returns its path plus a cleanup function. The file contains only image IDs and
// is removed after the Compose call.
func writeRollbackOverride(pins []stack.RollbackPin) (string, func(), error) {
	doc := rollbackOverrideDoc{Services: make(map[string]rollbackOverrideService, len(pins))}
	for _, pin := range pins {
		doc.Services[pin.Service] = rollbackOverrideService{Image: pin.ImageID}
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return "", func() {}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgRollbackOverrideFailed,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgRollbackOverrideAction},
		})
	}
	file, err := os.CreateTemp("", "dcraft-rollback-*.yaml")
	if err != nil {
		return "", func() {}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgRollbackOverrideFailed,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgRollbackOverrideAction},
		})
	}
	name := file.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgRollbackOverrideFailed,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgRollbackOverrideAction},
		})
	}
	header := "# dcraft-managed previous-version override. Do not edit; dcraft generates and removes it.\n"
	if _, err := file.Write(append([]byte(header), data...)); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgRollbackOverrideFailed,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgRollbackOverrideAction},
		})
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgRollbackOverrideFailed,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgRollbackOverrideAction},
		})
	}
	return name, cleanup, nil
}
