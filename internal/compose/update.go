package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/stack"
)

// UpdateModel implements stack.Updater. It decodes only the project name and
// per-service image/build/healthcheck presence; the resolved model also carries
// environment values, and those are dropped immediately.
func (c *Compose) UpdateModel(ctx context.Context, t core.Target) (stack.UpdateModel, error) {
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: append(t.ComposeArgs(), "config", "--format", "json"),
		Dir:  t.ProjectDir,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return stack.UpdateModel{}, ctxErr
	}
	if err != nil || !res.Succeeded() {
		return stack.UpdateModel{}, core.FailCause(err, classifyConfigFailure(res, err))
	}
	var doc configDoc
	if err := json.Unmarshal(res.Stdout, &doc); err != nil {
		return stack.UpdateModel{}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgUpdateModelFailed,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgUpdateModelAction},
		})
	}
	model := stack.UpdateModel{ProjectName: strings.TrimSpace(doc.Name), Services: []stack.ServiceDefinition{}}
	names := make([]string, 0, len(doc.Services))
	for name := range doc.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		svc, err := parseService(doc.Services[name])
		if err != nil {
			return stack.UpdateModel{}, core.FailCause(err, core.Diagnostic{
				Category:    core.CategoryInternal,
				Severity:    core.SeverityError,
				Message:     msgUpdateModelFailed,
				Detail:      summarize(err.Error()),
				NextActions: []string{msgUpdateModelAction},
			})
		}
		model.Services = append(model.Services, stack.ServiceDefinition{
			Name:        name,
			Image:       svc.Image,
			Build:       hasBuild(svc.Build),
			Healthcheck: hasHealthcheck(svc.Healthcheck),
		})
	}
	return model, nil
}

type inspectContainer struct {
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	Created      string `json:"Created"`
	Image        string `json:"Image"`
	RestartCount int    `json:"RestartCount"`
	State        struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Restarting bool   `json:"Restarting"`
		ExitCode   int    `json:"ExitCode"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

// Containers implements stack.Updater. Service identity, health and restart
// count come from the engine, not from Compose's display strings: Compose's
// ExitCode is 0 for restarting containers and its ID column is truncated.
func (c *Compose) Containers(ctx context.Context, t core.Target) ([]stack.Container, error) {
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: append(t.ComposeArgs(), "ps", "-a", "-q"),
		Dir:  t.ProjectDir,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil || !res.Succeeded() {
		return nil, core.FailCause(err, classifyPSFailure(res, err))
	}
	ids := strings.Fields(string(res.Stdout))
	if len(ids) == 0 {
		return []stack.Container{}, nil
	}

	inspectRes, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: append([]string{"inspect"}, ids...),
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
	if !inspectRes.Succeeded() {
		text := inspectRes.Combined()
		if looksLikeEngineFailure(strings.ToLower(text)) {
			return nil, core.FailCause(nil, engineDiagnostic(inspectRes))
		}
		return nil, core.FailCause(nil, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgUpdateInspectFailed,
			Detail:      summarize(text),
			NextActions: []string{msgUpdateInspectAction},
		})
	}
	var docs []inspectContainer
	if err := json.Unmarshal(inspectRes.Stdout, &docs); err != nil {
		return nil, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgUpdateInspectFailed,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgUpdateInspectAction},
		})
	}
	containers := make([]stack.Container, 0, len(docs))
	for _, doc := range docs {
		svc := doc.Config.Labels["com.docker.compose.service"]
		container := stack.Container{
			ID:           doc.ID,
			Name:         strings.TrimPrefix(doc.Name, "/"),
			Service:      svc,
			ImageID:      doc.Image,
			ImageRef:     doc.Config.Image,
			State:        strings.ToLower(doc.State.Status),
			Running:      doc.State.Running,
			Restarting:   doc.State.Restarting,
			ExitCode:     doc.State.ExitCode,
			RestartCount: doc.RestartCount,
			Created:      doc.Created,
		}
		if doc.State.Health != nil {
			container.HasHealth = true
			container.Health = strings.ToLower(doc.State.Health.Status)
		}
		containers = append(containers, container)
	}
	sort.Slice(containers, func(i, j int) bool {
		if containers[i].Service != containers[j].Service {
			return containers[i].Service < containers[j].Service
		}
		return containers[i].ID < containers[j].ID
	})
	return containers, nil
}

// Build implements stack.Updater. The default build uses the normal cache;
// cache bypass and base-image refresh are separate explicit choices.
func (c *Compose) Build(ctx context.Context, t core.Target, services []string, opts stack.BuildOptions) (stack.OpResult, error) {
	args := append(t.ComposeArgs(), "build")
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	if opts.Pull {
		args = append(args, "--pull")
	}
	args = append(args, services...)
	return c.lifecycleRun(ctx, t, "build", args)
}

// Pull implements stack.Updater. It fetches only the configured references of
// image-only services; local build services and their base images are not
// affected.
func (c *Compose) Pull(ctx context.Context, t core.Target, services []string) (stack.OpResult, error) {
	args := append(t.ComposeArgs(), "pull")
	args = append(args, services...)
	return c.lifecycleRun(ctx, t, "pull", args)
}

// Apply implements stack.Updater. It is detached and never waits: readiness is
// observed separately because `up --wait` returns success for crash-looping and
// no-healthcheck containers. Runtime secret delivery is prepared and committed
// through the same boundary as Up, so an update recreates containers with the
// current environment values and file materializations.
func (c *Compose) Apply(ctx context.Context, t core.Target, services []string, opts stack.ApplyOptions) (stack.OpResult, error) {
	prep, err := c.prepareSecrets(ctx, t, services, secretref.ModeCreate)
	if err != nil {
		return stack.OpResult{}, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		// The operation was canceled before any Compose process started, so
		// no container can reference the new materialization.
		prep.Abort()
		return stack.OpResult{}, ctxErr
	}
	args := []string{"up", "--detach"}
	if opts.ForceRecreate {
		args = append(args, "--force-recreate")
	}
	policy := opts.PullPolicy
	if policy == "" {
		policy = "missing"
	}
	args = append(args, "--pull", policy)
	args = append(args, services...)
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: composeArgsWith(t, prep.Files, args...),
		Dir:  t.ProjectDir,
		Env:  composeEnv(prep.Env),
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return stack.OpResult{Output: prep.Redact(summarize(res.Combined())), Notices: prep.Notices}, ctxErr
	}
	if err != nil {
		// The Compose process never started, so no container can reference
		// the new materialization.
		prep.Abort()
		return stack.OpResult{}, err
	}
	out := stack.OpResult{Output: prep.Redact(summarize(res.Combined())), Notices: prep.Notices}
	if !res.Succeeded() {
		// Compose ran and may have created containers; keep the files a
		// restart may need. The next up/down cleans stale files.
		return out, fmt.Errorf("docker compose up exited with status %d", res.ExitCode)
	}
	prep.Commit()
	return out, nil
}

// ImageID implements stack.Updater. The full sha256: ID is the authoritative
// identity; a movable tag or the container's com.docker.compose.image label is
// not.
func (c *Compose) ImageID(ctx context.Context, ref string) (string, error) {
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: []string{"image", "inspect", ref, "--format", "{{.Id}}"},
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
		return "", &stack.ImageMissingError{Ref: ref, Detail: summarize(text)}
	}
	id := strings.TrimSpace(string(res.Stdout))
	if !strings.HasPrefix(id, "sha256:") {
		return "", core.FailCause(nil, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgUpdateInspectFailed,
			Detail:      summarize("unexpected image ID: " + id),
			NextActions: []string{msgUpdateInspectAction},
		})
	}
	return id, nil
}

// TagImage implements stack.Updater. It adds a dcraft-owned retention tag and
// verifies that the tag resolves to the recorded image ID, so a later moving
// service tag cannot redirect the recovery target.
func (c *Compose) TagImage(ctx context.Context, imageID, tag string) error {
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: []string{"tag", imageID, tag},
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return core.FailCause(err, core.Diagnostic{
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
			return core.FailCause(nil, engineDiagnostic(res))
		}
		return core.FailCause(nil, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgUpdateTagFailed,
			Detail:      summarize(text),
			NextActions: []string{msgUpdateTagAction},
		})
	}
	got, err := c.ImageID(ctx, tag)
	if err != nil {
		return err
	}
	if got != imageID {
		return core.FailCause(nil, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgUpdateTagFailed,
			Detail:      fmt.Sprintf("%s resolves to %s, want %s", tag, got, imageID),
			NextActions: []string{msgUpdateTagAction},
		})
	}
	return nil
}
