package rollback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

// precheck resolves everything the apply needs and aborts before any change
// when it is unavailable. It never writes, pulls, builds or tags.
func (c *Controller) precheck(ctx context.Context, in core.Invocation, rec state.GoodVersion) (plan, error) {
	if err := ctx.Err(); err != nil {
		return plan{}, err
	}
	if explicit := strings.TrimSpace(in.Target.ProjectName); explicit != "" && explicit != rec.Target.ProjectName {
		return plan{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInvalidOption,
			Severity:    core.SeverityError,
			Message:     msgProjectNameConflict,
			Detail:      fmt.Sprintf("요청한 프로젝트 이름: %s; 기록된 이름: %s", explicit, rec.Target.ProjectName),
			NextActions: []string{msgProjectNameConflictAct},
		})
	}
	opTarget := recordedTarget(rec)
	if err := checkInputs(opTarget); err != nil {
		return plan{}, err
	}

	model, err := c.updater.UpdateModel(ctx, opTarget)
	if err != nil {
		return plan{}, err
	}
	byName := make(map[string]stack.ServiceDefinition, len(model.Services))
	for _, def := range model.Services {
		byName[def.Name] = def
	}
	p := plan{target: opTarget}
	for _, svc := range rec.Services {
		def, ok := byName[svc.Service]
		if !ok {
			return plan{}, core.Fail(core.Diagnostic{
				Category:    core.CategoryRollbackRecordInvalid,
				Severity:    core.SeverityError,
				Message:     msgRecordInvalid,
				Detail:      fmt.Sprintf("기록된 서비스 %q가 기록된 Compose 설정에 없음.", svc.Service),
				NextActions: []string{msgRecordAct1, msgRecordAct2},
			})
		}
		p.services = append(p.services, svc.Service)
		p.defs = append(p.defs, def)
		p.pins = append(p.pins, stack.RollbackPin{
			Service:      svc.Service,
			ImageID:      svc.ImageID,
			RetentionTag: svc.RetentionTag,
		})
	}
	sort.Strings(p.services)
	sort.Slice(p.defs, func(i, j int) bool { return p.defs[i].Name < p.defs[j].Name })
	sort.Slice(p.pins, func(i, j int) bool { return p.pins[i].Service < p.pins[j].Service })

	for _, def := range model.Services {
		if !containsService(p.services, def.Name) {
			p.extraServices = append(p.extraServices, def.Name)
		}
	}
	if len(p.extraServices) > 0 {
		sort.Strings(p.extraServices)
		p.notices = append(p.notices, fmt.Sprintf(msgPlanExtra, strings.Join(p.extraServices, ", ")))
	}

	if err := c.checkImages(ctx, p.pins); err != nil {
		return plan{}, err
	}
	if err := c.checkSecrets(ctx, opTarget, rec, p.services); err != nil {
		return plan{}, err
	}

	containers, err := c.updater.Containers(ctx, opTarget)
	if err != nil {
		return plan{}, err
	}
	p.baseline = make(map[string]int, len(containers))
	byService := make(map[string][]stack.Container, len(containers))
	orphans := map[string]bool{}
	for _, container := range containers {
		p.baseline[container.ID] = container.RestartCount
		byService[container.Service] = append(byService[container.Service], container)
		if !containsService(p.services, container.Service) {
			orphans[container.Service] = true
		}
	}
	for name := range orphans {
		p.orphanServices = append(p.orphanServices, name)
	}
	sort.Strings(p.orphanServices)
	if len(p.orphanServices) > 0 {
		p.notices = append(p.notices, fmt.Sprintf(msgPlanOrphans, strings.Join(p.orphanServices, ", ")))
	}
	p.alreadyAtVersion = len(p.pins) > 0
	for _, pin := range p.pins {
		records := byService[pin.Service]
		if len(records) == 0 {
			p.alreadyAtVersion = false
			break
		}
		for _, container := range records {
			if container.ImageID != pin.ImageID {
				p.alreadyAtVersion = false
				break
			}
		}
	}
	if p.alreadyAtVersion {
		p.notices = append(p.notices, msgSuccessAlready)
	}
	return p, nil
}

// checkInputs verifies the recorded execution inputs still exist. The default
// .env is informational: Compose loads it only when present.
func checkInputs(target core.Target) error {
	var problems []string
	if info, err := os.Stat(target.ProjectDir); err != nil {
		problems = append(problems, "대상 폴더: "+target.ProjectDir)
	} else if !info.IsDir() {
		problems = append(problems, "대상 폴더가 폴더가 아님: "+target.ProjectDir)
	}
	for _, file := range target.Files {
		if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
			problems = append(problems, "Compose 파일: "+file)
		}
	}
	for _, file := range target.EnvFiles {
		if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
			problems = append(problems, "환경 파일: "+file)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryRollbackInputMissing,
		Severity:    core.SeverityError,
		Message:     msgInputMissing,
		Detail:      strings.Join(problems, "; "),
		NextActions: []string{msgInputAct1, msgInputAct2},
	})
}

// checkImages verifies every recorded image by its full ID and by the
// dcraft-owned retention tag. Either missing or a moved tag aborts; no newer
// image is ever substituted.
func (c *Controller) checkImages(ctx context.Context, pins []stack.RollbackPin) error {
	var problems []string
	for _, pin := range pins {
		if err := ctx.Err(); err != nil {
			return err
		}
		got, err := c.updater.ImageID(ctx, pin.ImageID)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			var missing *stack.ImageMissingError
			if errors.As(err, &missing) {
				problems = append(problems, fmt.Sprintf(msgImageIDMissing, pin.ImageID))
				continue
			}
			return err
		}
		if got != pin.ImageID {
			problems = append(problems, fmt.Sprintf(msgImageTagMoved, pin.ImageID, pin.ImageID, got))
			continue
		}
		tagID, err := c.updater.ImageID(ctx, pin.RetentionTag)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			var missing *stack.ImageMissingError
			if errors.As(err, &missing) {
				problems = append(problems, fmt.Sprintf(msgImageTagMissing, pin.RetentionTag))
				continue
			}
			return err
		}
		if tagID != pin.ImageID {
			problems = append(problems, fmt.Sprintf(msgImageTagMoved, pin.RetentionTag, pin.ImageID, tagID))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryRollbackImageMissing,
		Severity:    core.SeverityError,
		Message:     msgImageMissing,
		Detail:      strings.Join(problems, "; "),
		NextActions: []string{msgImageAct1, msgImageAct2},
	})
}

// checkSecrets re-resolves exactly the secret references stored in the record.
// The project's current .dcraft.yaml is never substituted: rollback restores
// the deployment that was recorded, including its original sources and
// targets. Missing sources, unavailable AWS references and unknown sources
// abort before any change. Values are read for availability only and never
// stored or printed; for AWS, the runtime fetches and discards the value.
func (c *Controller) checkSecrets(ctx context.Context, target core.Target, rec state.GoodVersion, services []string) error {
	var problems []string
	for _, ref := range rec.Target.SecretRefs {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			problems = append(problems, "기록된 비밀값 참조에 이름이 없음 (service="+ref.Service+")")
			continue
		}
		// Runtime and build-phase references are both re-checked for source
		// availability. Rollback never rebuilds, so a build reference is not
		// delivered here; only its source is proven readable.
		phase := strings.ToLower(strings.TrimSpace(ref.Phase))
		switch phase {
		case "", string(secretref.PhaseRuntime), string(secretref.PhaseBuild):
		default:
			problems = append(problems, fmt.Sprintf("기록된 비밀값 참조의 단계를 알 수 없음: %q (service=%s)", ref.Phase, ref.Service))
			continue
		}
		source := strings.ToLower(strings.TrimSpace(ref.Source))
		switch source {
		case "env":
			if strings.TrimSpace(c.getenv(name)) == "" {
				problems = append(problems, "환경변수 원본이 없음: "+name)
			}
		case "file":
			path := name
			if !filepath.IsAbs(path) {
				path = filepath.Join(target.ProjectDir, path)
			}
			if err := readableFile(path); err != nil {
				problems = append(problems, "파일 원본을 읽을 수 없음: "+name)
			}
		case "aws":
			awsRef := secretref.Reference{
				Service: ref.Service, Phase: secretref.Phase(ref.Phase),
				Delivery: secretref.Delivery(ref.Delivery), Source: secretref.SourceAWS,
				Ref: name, Field: ref.Field, Region: ref.Region, Profile: ref.Profile,
			}
			if err := c.checkAWSReference(ctx, awsRef); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				problems = append(problems, fmt.Sprintf("%s:%s AWS 원본 확인 불가: %s", ref.Service, name, diagnosticMessage(err)))
			}
		default:
			problems = append(problems, fmt.Sprintf("지원하지 않는 비밀값 원본: %q", ref.Source))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryRollbackSecretUnavailable,
		Severity:    core.SeverityError,
		Message:     msgSecretUnavailable,
		Detail:      strings.Join(problems, "; "),
		NextActions: []string{msgSecretAct1, msgSecretAct2},
	})
}

// checkAWSReference proves an AWS reference is resolvable. A controller built
// without the secret runtime refuses instead of silently treating an AWS
// reference as available.
func (c *Controller) checkAWSReference(ctx context.Context, ref secretref.Reference) error {
	if c.awsChecker == nil {
		return errors.New("AWS 비밀값 조회 연결이 준비되지 않았음")
	}
	return c.awsChecker.CheckAWS(ctx, ref)
}

// diagnosticMessage returns the Korean guidance from a classified error
// without tool output or a value. It is used in the precheck summary.
func diagnosticMessage(err error) string {
	var ce *core.Error
	if errors.As(err, &ce) && len(ce.Diagnostics) > 0 {
		return ce.Diagnostics[0].Message
	}
	return err.Error()
}

func readableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return errors.New("empty file")
	}
	return nil
}

func containsService(services []string, name string) bool {
	for _, service := range services {
		if service == name {
			return true
		}
	}
	return false
}

// recordedSecretRefs converts the neutral record shape back to the shared
// secret reference contract. Only the saved fields are used; no current
// configuration is consulted.
func recordedSecretRefs(rec state.GoodVersion) []secretref.Reference {
	refs := make([]secretref.Reference, 0, len(rec.Target.SecretRefs))
	for _, ref := range rec.Target.SecretRefs {
		refs = append(refs, secretref.Reference{
			Service:  ref.Service,
			Phase:    secretref.Phase(ref.Phase),
			Delivery: secretref.Delivery(ref.Delivery),
			Source:   secretref.SourceType(ref.Source),
			Ref:      ref.Name,
			Target:   ref.Target,
			Field:    ref.Field,
			Region:   ref.Region,
			Profile:  ref.Profile,
		})
	}
	return refs
}
