package update

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

// Plan is the read-only preview of an update: the target, the services, the
// planned build/pull work and the Korean explanation of the chosen options. It
// is a step plan, never an exact future-state diff.
type Plan struct {
	Text          string   `json:"text"`
	Services      []string `json:"services"`
	BuildServices []string `json:"build_services"`
	PullServices  []string `json:"pull_services"`
}

// Preview resolves the same target inputs and Compose model as Run but changes
// nothing: no name allocation, no lock, no build, no pull, no state write. It is
// the read-only plan both interfaces show before an ordinary update.
func (c *Controller) Preview(ctx context.Context, in core.Invocation) (Plan, error) {
	opts, err := ParseOptions(in)
	if err != nil {
		return Plan{}, err
	}
	if c == nil || c.updater == nil || c.identity == nil {
		return Plan{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoUpdater,
			NextActions: []string{msgNoUpdaterAct},
		})
	}

	cfg, err := c.identity.ConfigInfo(ctx, in.Target)
	if err != nil {
		return Plan{}, err
	}
	services, err := normalizeServices(in.Services)
	if err != nil {
		return Plan{}, err
	}
	if err := validateServices(services, cfg.Services); err != nil {
		return Plan{}, err
	}
	model, err := c.updater.UpdateModel(ctx, in.Target)
	if err != nil {
		return Plan{}, err
	}
	name := c.previewProjectName(ctx, in.Target, model)
	return buildPlan(in, name, model, services, opts), nil
}

// previewProjectName mirrors the lifecycle name priority without writing: an
// explicit -p, a declared Compose name, a recorded allocation, then Compose's
// folder default marked as "will be allocated".
func (c *Controller) previewProjectName(ctx context.Context, target core.Target, model stack.UpdateModel) string {
	if explicit := strings.TrimSpace(target.ProjectName); explicit != "" {
		if model.ProjectName != "" {
			return model.ProjectName
		}
		return explicit
	}
	derived, derivedErr := c.identity.DerivedProjectName(ctx, target)
	if derivedErr == nil && model.ProjectName != "" && model.ProjectName != derived {
		return model.ProjectName
	}
	if ls, ok := c.store.(lockStore); ok {
		if alloc, found, err := ls.LookupName(target); err == nil && found {
			return alloc.Name
		}
	}
	if derivedErr == nil && derived != "" {
		return derived + msgPlanAutoName
	}
	return model.ProjectName
}

func buildPlan(in core.Invocation, projectName string, model stack.UpdateModel, selected []string, opts Options) Plan {
	defs := targetDefinitions(model, selected)
	build := make([]string, 0)
	pull := make([]string, 0)
	var planned []string
	for _, def := range defs {
		switch {
		case def.Build:
			build = append(build, def.Name)
			mode := "(일반 캐시)"
			if opts.NoCache {
				mode = "(캐시 사용 안 함)"
			}
			if opts.BuildPull {
				mode += " · 베이스 이미지 새로 받기"
			}
			planned = append(planned, fmt.Sprintf("  - %s: 로컬 빌드 %s 후 적용", def.Name, mode))
		case def.Image != "":
			if opts.Pull {
				pull = append(pull, def.Name)
				planned = append(planned, fmt.Sprintf("  - %s: 이미지 취득(%s) 후 적용", def.Name, def.Image))
			} else {
				planned = append(planned, fmt.Sprintf("  - %s: 이미지 새로 고침 생략, 로컬 이미지(%s) 사용", def.Name, def.Image))
			}
		default:
			planned = append(planned, fmt.Sprintf("  - %s: 적용", def.Name))
		}
	}
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	if len(planned) == 0 {
		planned = append(planned, "  - "+msgSuccessNoServices)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", msgPlanHeader)
	fmt.Fprintf(&b, "  %s: %s\n", msgSuccessFolder, in.Target.ProjectDir)
	fmt.Fprintf(&b, "  %s: %s\n", msgSuccessFiles, joinOrNone(in.Target.Files))
	if len(in.Target.EnvFiles) > 0 {
		fmt.Fprintf(&b, "  %s: %s\n", msgSuccessEnvFiles, strings.Join(in.Target.EnvFiles, ", "))
	} else if in.Target.DefaultEnvFile != "" {
		fmt.Fprintf(&b, "  %s: %s (자동 적용)\n", msgSuccessEnvFiles, in.Target.DefaultEnvFile)
	}
	if len(in.Target.Profiles) > 0 {
		fmt.Fprintf(&b, "  %s: %s\n", msgSuccessProfiles, strings.Join(in.Target.Profiles, ", "))
	}
	fmt.Fprintf(&b, "  %s: %s\n", msgSuccessProject, projectName)
	if in.Target.Environment != "" {
		fmt.Fprintf(&b, "  %s: %s\n", msgSuccessEnv, in.Target.Environment)
	}
	if len(selected) > 0 {
		fmt.Fprintf(&b, "  %s: %s\n", msgSuccessServices, strings.Join(selected, ", "))
	} else {
		fmt.Fprintf(&b, "  %s: %s (%d개)\n", msgSuccessServices, msgSuccessWhole, len(names))
	}
	b.WriteString(msgPlanWork + "\n")
	b.WriteString(strings.Join(planned, "\n") + "\n")
	b.WriteString(msgPlanFlagHeader + "\n")
	fmt.Fprintf(&b, "  캐시 사용 안 함: %s\n", yesNo(opts.NoCache))
	fmt.Fprintf(&b, "  베이스 이미지 새로 받기: %s\n", yesNo(opts.BuildPull))
	fmt.Fprintf(&b, "  이미지 새로 고침: %s\n", yesNo(opts.Pull))
	fmt.Fprintf(&b, "  강제 재생성: %s\n", yesNo(opts.ForceRecreate))
	if opts.NoCache {
		b.WriteString(msgPlanNoCacheCost + "\n")
	}
	if opts.BuildPull {
		b.WriteString(msgPlanBuildPullCost + "\n")
	}
	if !opts.Pull {
		b.WriteString(msgPlanNoPullCost + "\n")
	}
	if opts.ForceRecreate {
		b.WriteString(msgPlanForceCost + "\n")
		b.WriteString(msgPlanForceRecreate + "\n")
	} else {
		b.WriteString(msgPlanDefaultRecreate + "\n")
	}
	if len(selected) > 0 {
		b.WriteString(msgPlanServiceOnly + "\n")
	} else {
		b.WriteString(msgPlanWholeStack + "\n")
	}
	fmt.Fprintf(&b, msgPlanVerify+"\n", formatTimeout(opts.Timeout))
	b.WriteString(msgPlanDisclaimer)
	return Plan{Text: b.String(), Services: names, BuildServices: build, PullServices: pull}
}

func targetDefinitions(model stack.UpdateModel, selected []string) []stack.ServiceDefinition {
	if len(selected) == 0 {
		return model.Services
	}
	wanted := map[string]bool{}
	for _, name := range selected {
		wanted[name] = true
	}
	var defs []stack.ServiceDefinition
	for _, def := range model.Services {
		if wanted[def.Name] {
			defs = append(defs, def)
		}
	}
	return defs
}

func formatTimeout(d time.Duration) string {
	seconds := int(d / time.Second)
	return fmt.Sprintf("%d초", seconds)
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return msgPlanNoEnv
	}
	return strings.Join(items, ", ")
}

func yesNo(v bool) string {
	if v {
		return "예"
	}
	return "아니오"
}

// retentionTag is the deterministic dcraft-owned tag that keeps a recorded
// image alive after its container is replaced. It is unique per project,
// service and image ID so it never moves to another image.
func retentionTag(project, service, imageID string) string {
	return sanitizeRepo("dcraft-good/"+project+"-"+service) + ":" + shortID(imageID)
}

func shortID(imageID string) string {
	id := strings.TrimPrefix(strings.TrimSpace(imageID), "sha256:")
	if len(id) > 16 {
		id = id[:16]
	}
	return id
}

func sanitizeRepo(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '/', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-/.")
	if out == "" {
		return "dcraft-good/image"
	}
	return out
}

// goodVersionSummary renders the recorded identity for reports.
func goodVersionSummary(rec *state.GoodVersion) []string {
	if rec == nil {
		return nil
	}
	lines := make([]string, 0, len(rec.Services))
	for _, svc := range rec.Services {
		lines = append(lines, fmt.Sprintf("  - %s: %s (%s)", svc.Service, svc.ImageID, svc.RetentionTag))
	}
	return lines
}
