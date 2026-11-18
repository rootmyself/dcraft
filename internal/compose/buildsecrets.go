package compose

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/secretref"
)

// configBuild is the resolved build section of one Compose service. Compose
// normalizes a string build context to this object.
type configBuild struct {
	Context          string `json:"context"`
	Dockerfile       string `json:"dockerfile"`
	DockerfileInline string `json:"dockerfile_inline"`
}

// buildHints reads the resolved build context and Dockerfile locations. The
// files are only inspected to warn that a BuildKit mount must be consumed;
// dcraft never edits them.
func (c *Compose) buildHints(ctx context.Context, t core.Target) (map[string]secretref.BuildServiceHint, error) {
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: append(t.ComposeArgs(), "config", "--format", "json"),
		Dir:  t.ProjectDir,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil || !res.Succeeded() {
		return nil, core.FailCause(err, classifyConfigFailure(res, err))
	}
	var doc configDoc
	if err := json.Unmarshal(res.Stdout, &doc); err != nil {
		return nil, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgUpdateModelFailed,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgUpdateModelAction},
		})
	}
	hints := make(map[string]secretref.BuildServiceHint, len(doc.Services))
	for name, raw := range doc.Services {
		var svc configService
		if err := json.Unmarshal(raw, &svc); err != nil {
			continue
		}
		hint := secretref.BuildServiceHint{}
		if len(svc.Build) > 0 && strings.TrimSpace(string(svc.Build)) != "null" {
			var build configBuild
			if err := json.Unmarshal(svc.Build, &build); err == nil {
				hint.HasBuild = true
			} else {
				// The legacy string form is still a build service; the
				// Dockerfile path is unknown but the reference is valid.
				var contextDir string
				if json.Unmarshal(svc.Build, &contextDir) == nil {
					build.Context = contextDir
					hint.HasBuild = true
				}
			}
			if hint.HasBuild {
				contextDir := strings.TrimSpace(build.Context)
				if contextDir == "" || !filepath.IsAbs(contextDir) {
					if contextDir == "" {
						contextDir = t.ProjectDir
					} else {
						contextDir = filepath.Join(t.ProjectDir, contextDir)
					}
				}
				if inline := strings.TrimSpace(build.DockerfileInline); inline != "" {
					hint.InlineDockerfiles = append(hint.InlineDockerfiles, inline)
				}
				dockerfile := strings.TrimSpace(build.Dockerfile)
				if dockerfile == "" && len(hint.InlineDockerfiles) == 0 {
					dockerfile = "Dockerfile"
				}
				if dockerfile != "" {
					if !filepath.IsAbs(dockerfile) {
						dockerfile = filepath.Join(contextDir, dockerfile)
					}
					hint.Dockerfiles = append(hint.Dockerfiles, dockerfile)
				}
			}
		}
		hints[name] = hint
	}
	return hints, nil
}

// checkBuildSecretsSupported fails before any build when the installed builder
// cannot deliver BuildKit secret mounts. dcraft never silently degrades to a
// build argument or an environment value.
func (c *Compose) checkBuildSecretsSupported(ctx context.Context) error {
	if strings.TrimSpace(c.getenv("DOCKER_BUILDKIT")) == "0" {
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryComposeUnsupported,
			Severity:    core.SeverityError,
			Message:     msgBuildKitDisabled,
			Detail:      "DOCKER_BUILDKIT=0",
			NextActions: []string{msgBuildKitActionOne, msgBuildKitActionTwo},
		})
	}
	res, err := c.run(ctx, execx.Command{Name: "docker", Args: []string{"buildx", "version"}})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil || !res.Succeeded() {
		return core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryComposeUnsupported,
			Severity:    core.SeverityError,
			Message:     msgBuildKitUnavailable,
			Detail:      summarize(res.Combined()),
			NextActions: []string{msgBuildKitActionOne, msgBuildKitActionTwo},
		})
	}
	return nil
}

// prepareBuildSecrets resolves and materializes only the build-phase
// references of the selected services. It returns an empty session when the
// project has no build references, so an ordinary build keeps exactly the
// user's Compose inputs.
func (c *Compose) prepareBuildSecrets(ctx context.Context, t core.Target, services []string) (secretref.BuildSession, error) {
	cat, err := secretref.Load(t.ProjectDir)
	if err != nil {
		return secretref.BuildSession{}, err
	}
	refs := cat.ForPhaseServices(secretref.PhaseBuild, services)
	if len(refs) > 0 {
		if c.secrets == nil {
			return secretref.BuildSession{}, secretref.Unavailable()
		}
		if err := c.checkBuildSecretsSupported(ctx); err != nil {
			return secretref.BuildSession{}, err
		}
		hints, err := c.buildHints(ctx, t)
		if err != nil {
			return secretref.BuildSession{}, err
		}
		return c.secrets.PrepareBuild(ctx, t, services, hints)
	}
	if c.secrets == nil {
		return secretref.BuildSession{}, nil
	}
	// A manual config edit may have removed the last build reference. Clear a
	// stale managed build override so the user's inputs are the effective
	// inputs again.
	return c.secrets.PrepareBuild(ctx, t, services, nil)
}
