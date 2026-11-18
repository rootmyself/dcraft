package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/stack"
)

// Up implements stack.Lifecycle. It starts the stack detached, applying the
// current Compose configuration. Readiness and health are later-ticket work;
// dcraft does not claim the application is healthy here.
func (c *Compose) Up(ctx context.Context, t core.Target, services []string) (stack.OpResult, error) {
	args := append(t.ComposeArgs(), "up", "--detach")
	args = append(args, services...)
	return c.lifecycleRun(ctx, t, "up", args)
}

// Stop implements stack.Lifecycle. Containers and data are kept.
func (c *Compose) Stop(ctx context.Context, t core.Target, services []string) (stack.OpResult, error) {
	args := append(t.ComposeArgs(), "stop")
	args = append(args, services...)
	return c.lifecycleRun(ctx, t, "stop", args)
}

// Down implements stack.Lifecycle. It is project-wide and never passes -v, so
// data volumes survive ordinary teardown.
func (c *Compose) Down(ctx context.Context, t core.Target) (stack.OpResult, error) {
	args := append(t.ComposeArgs(), "down")
	return c.lifecycleRun(ctx, t, "down", args)
}

// Restart implements stack.Lifecycle. Compose restart does not recreate
// containers, so it never applies new configuration or images. An explicit
// service selection is isolated with --no-deps.
func (c *Compose) Restart(ctx context.Context, t core.Target, services []string) (stack.OpResult, error) {
	args := append(t.ComposeArgs(), "restart")
	if len(services) > 0 {
		args = append(args, "--no-deps")
	}
	args = append(args, services...)
	return c.lifecycleRun(ctx, t, "restart", args)
}

func (c *Compose) lifecycleRun(ctx context.Context, t core.Target, action string, args []string) (stack.OpResult, error) {
	res, err := c.run(ctx, execx.Command{Name: "docker", Args: args, Dir: t.ProjectDir})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return stack.OpResult{}, ctxErr
	}
	if err != nil {
		return stack.OpResult{}, err
	}
	out := stack.OpResult{Output: summarize(res.Combined())}
	if !res.Succeeded() {
		return out, fmt.Errorf("docker compose %s exited with status %d", action, res.ExitCode)
	}
	return out, nil
}

// ConfigInfo implements stack.Identity. It decodes only the project name and
// service keys; environment values in the resolved model are dropped
// immediately.
func (c *Compose) ConfigInfo(ctx context.Context, t core.Target) (stack.ConfigInfo, error) {
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: append(t.ComposeArgs(), "config", "--format", "json"),
		Dir:  t.ProjectDir,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return stack.ConfigInfo{}, ctxErr
	}
	if err != nil || !res.Succeeded() {
		return stack.ConfigInfo{}, core.FailCause(err, classifyConfigFailure(res, err))
	}
	var doc configDoc
	if err := json.Unmarshal(res.Stdout, &doc); err != nil {
		return stack.ConfigInfo{}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgInspectOutput,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgInspectOutputAction},
		})
	}
	services := make([]string, 0, len(doc.Services))
	for name := range doc.Services {
		services = append(services, name)
	}
	sort.Strings(services)
	return stack.ConfigInfo{EffectiveName: strings.TrimSpace(doc.Name), Services: services}, nil
}

// DerivedProjectName implements stack.Identity. Compose itself computes its
// default project name for the target's project directory (the first file's
// directory). Using Compose avoids reimplementing name normalization and keeps
// automatic names identical to the names Compose would use.
func (c *Compose) DerivedProjectName(ctx context.Context, t core.Target) (string, error) {
	dir := t.ProjectDir
	if len(t.Files) > 0 && t.Files[0] != "" {
		dir = filepath.Dir(t.Files[0])
	}
	// An empty env file suppresses the project's .env and the probe environment
	// drops COMPOSE_PROJECT_NAME. Both are user declarations; comparing them
	// against themselves would hide a declared name. A declaration in .env, an
	// exported COMPOSE_PROJECT_NAME or a project `name:` therefore differs from
	// this true folder default and is honored as declared.
	args := []string{"compose", "--env-file", os.DevNull, "-f", "-", "--project-directory", dir, "config", "--format", "json"}
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: args,
		Dir:  t.ProjectDir,
		Env:  withoutComposeProjectName(os.Environ()),
		// The probe document carries no user configuration. It is fed on
		// stdin and creates nothing.
		Stdin: strings.NewReader(probeDocument),
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if err != nil {
		return "", core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryDockerMissing,
			Severity:    core.SeverityError,
			Message:     msgDockerNotExecutable,
			Detail:      err.Error(),
			NextActions: []string{msgDockerMissingActionTwo},
		})
	}
	if !res.Succeeded() {
		text := res.Combined()
		if looksLikeEngineFailure(strings.ToLower(text)) {
			return "", core.FailCause(nil, engineDiagnostic(res))
		}
		return "", core.FailCause(nil, unsupportedComposeDiagnostic(text))
	}
	var probe struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(res.Stdout, &probe); err != nil || strings.TrimSpace(probe.Name) == "" {
		return "", core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryComposeUnsupported,
			Severity:    core.SeverityError,
			Message:     msgDerivedNameUnknown,
			Detail:      summarize(res.Combined()),
			NextActions: []string{msgDerivedNameUnknownActionOne, msgDerivedNameUnknownActionTwo},
		})
	}
	return strings.TrimSpace(probe.Name), nil
}

// Projects implements stack.Identity using Compose's read-only project list.
// ProjectDir is the directory of the first config file, which is Compose's
// project directory and identifies the owning folder.
func (c *Compose) Projects(ctx context.Context) ([]stack.ProjectInfo, error) {
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: []string{"compose", "ls", "-a", "--format", "json"},
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryDockerMissing,
			Severity:    core.SeverityError,
			Message:     msgDockerNotExecutable,
			Detail:      err.Error(),
			NextActions: []string{msgDockerMissingActionTwo},
		})
	}
	if !res.Succeeded() {
		text := res.Combined()
		lower := strings.ToLower(text)
		if strings.Contains(lower, "unknown flag") || strings.Contains(lower, "unknown command") {
			return nil, core.FailCause(nil, unsupportedComposeDiagnostic(text))
		}
		if looksLikeEngineFailure(lower) {
			return nil, core.FailCause(nil, engineDiagnostic(res))
		}
		return nil, core.FailCause(nil, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgProjectListFailed,
			Detail:      summarize(text),
			NextActions: []string{msgProjectListFailedActionOne, msgProjectListFailedActionTwo},
		})
	}
	trimmed := strings.TrimSpace(string(res.Stdout))
	if trimmed == "" || trimmed == "null" {
		return []stack.ProjectInfo{}, nil
	}
	var rows []struct {
		Name        string `json:"Name"`
		Status      string `json:"Status"`
		ConfigFiles string `json:"ConfigFiles"`
	}
	if err := json.Unmarshal(res.Stdout, &rows); err != nil {
		return nil, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgProjectListFailed,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgProjectListFailedActionOne, msgProjectListFailedActionTwo},
		})
	}
	projects := make([]stack.ProjectInfo, 0, len(rows))
	for _, row := range rows {
		files := splitConfigFiles(row.ConfigFiles)
		info := stack.ProjectInfo{Name: row.Name, Status: row.Status, ConfigFiles: files}
		if len(files) > 0 {
			info.ProjectDir = filepath.Dir(files[0])
		}
		projects = append(projects, info)
	}
	return projects, nil
}

func withoutComposeProjectName(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, "COMPOSE_PROJECT_NAME=") {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func splitConfigFiles(raw string) []string {
	var files []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			files = append(files, part)
		}
	}
	return files
}
