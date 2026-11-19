package initconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
)

// Validation is the read-only Compose result of the generated content. It is
// also the authority for the project name Compose derives from the destination.
type Validation struct {
	Checked        bool     `json:"checked"`
	Command        string   `json:"command"`
	ProjectName    string   `json:"project_name,omitempty"`
	DefaultNetwork string   `json:"default_network,omitempty"`
	Services       []string `json:"services,omitempty"`
	// Volumes maps the logical volume name in the generated file to the actual
	// project-scoped volume Compose will create (for example cache ->
	// hello-world_cache).
	Volumes map[string]string `json:"volumes,omitempty"`
}

// Validator validates generated Compose content in the destination directory
// without contacting the Docker engine. Tests replace it with a fake.
type Validator interface {
	Validate(ctx context.Context, destinationDir string, content []byte) (Validation, error)
}

// ComposeValidator runs `docker compose -f - config --format json`. Passing the
// content on stdin with the destination directory as the working directory
// makes relative paths and the derived project name match the file that will be
// saved, without creating a temporary Compose file (plan/preview stays
// read-only). A dead engine is fine: config does not contact it.
type ComposeValidator struct {
	Runner execx.Runner
}

const validationCommand = "docker compose -f - config --format json"

func (v ComposeValidator) Validate(ctx context.Context, destinationDir string, content []byte) (Validation, error) {
	if v.Runner == nil {
		return Validation{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     "Compose 검증기를 준비하지 못했습니다.",
			NextActions: []string{"dcraft를 다시 빌드하거나 설치한 뒤 실행하세요."},
		})
	}
	if err := ctx.Err(); err != nil {
		return Validation{}, err
	}
	res, err := v.Runner.Run(ctx, execx.Command{
		Name:  "docker",
		Args:  []string{"compose", "-f", "-", "config", "--format", "json"},
		Dir:   destinationDir,
		Stdin: strings.NewReader(string(content)),
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Validation{}, ctxErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Validation{}, err
		}
		return Validation{}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryDockerMissing,
			Severity:    core.SeverityError,
			Message:     msgToolMissing,
			Detail:      err.Error(),
			NextActions: []string{msgToolAction},
		})
	}
	if !res.Succeeded() {
		text := res.Combined()
		if looksLikeMissingCompose(text) {
			return Validation{}, core.Fail(core.Diagnostic{
				Category:    core.CategoryComposeMissing,
				Severity:    core.SeverityError,
				Message:     msgComposeMissing,
				Detail:      truncateText(text, 800),
				NextActions: []string{msgComposeMissingAct, "생성한 파일은 저장하지 않았습니다."},
			})
		}
		return Validation{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInitInvalid,
			Severity:    core.SeverityError,
			Message:     msgComposeRejected,
			Detail:      truncateText(text, 800),
			NextActions: []string{msgComposeRejectedAct, msgComposeNoFile},
		})
	}

	var parsed struct {
		Name     string                     `json:"name"`
		Services map[string]json.RawMessage `json:"services"`
		Networks map[string]struct {
			Name string `json:"name"`
		} `json:"networks"`
		Volumes map[string]struct {
			Name string `json:"name"`
		} `json:"volumes"`
	}
	if err := json.Unmarshal(res.Stdout, &parsed); err != nil {
		return Validation{}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInitInvalid,
			Severity:    core.SeverityError,
			Message:     msgComposeOutput,
			Detail:      truncateText(string(res.Stdout)+"\n"+err.Error(), 800),
			NextActions: []string{msgComposeRejectedAct, msgComposeNoFile},
		})
	}
	if strings.TrimSpace(parsed.Name) == "" {
		return Validation{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInitInvalid,
			Severity:    core.SeverityError,
			Message:     msgComposeNameMiss,
			Detail:      truncateText(string(res.Stdout), 800),
			NextActions: []string{msgComposeRejectedAct, msgComposeNoFile},
		})
	}
	services := make([]string, 0, len(parsed.Services))
	for name := range parsed.Services {
		services = append(services, name)
	}
	sort.Strings(services)
	volumes := make(map[string]string, len(parsed.Volumes))
	for logical, volume := range parsed.Volumes {
		if volume.Name != "" {
			volumes[logical] = volume.Name
		}
	}
	return Validation{
		Checked:        true,
		Command:        validationCommand,
		ProjectName:    parsed.Name,
		DefaultNetwork: parsed.Networks["default"].Name,
		Services:       services,
		Volumes:        volumes,
	}, nil
}

func looksLikeMissingCompose(text string) bool {
	lower := strings.ToLower(text)
	for _, needle := range []string{
		"is not a docker command",
		"unknown command: docker compose",
		"unknown command \"compose\"",
		"no such file or directory",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func truncateText(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return text[:max] + "…"
}

func validationError(validation Validation, service string) error {
	for _, name := range validation.Services {
		if name == service {
			return nil
		}
	}
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryInitInvalid,
		Severity:    core.SeverityError,
		Message:     msgComposeServiceMiss,
		Detail:      fmt.Sprintf("서비스: %s (Compose 결과: %s)", service, strings.Join(validation.Services, ", ")),
		NextActions: []string{msgComposeRejectedAct, msgComposeNoFile},
	})
}
