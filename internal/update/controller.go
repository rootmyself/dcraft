// Package update implements the explicit `dcraft update` operation: the
// preview, the ordinary cached build plus image-reference refresh, the
// unchanged-service-preserving apply, bounded readiness verification and the
// single previous-good deployment record consumed by rollback.
//
// It never watches files, detects new remote images or downloads remote
// sources, and it never trusts `up --wait` as proof of health.
package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/readiness"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

// Stable stage names published for both interfaces and machine consumers.
const (
	StageResolveProject = "resolve_project"
	StageAllocateName   = "allocate_name"
	StageCoordinate     = "coordinate"
	StagePlan           = "plan"
	StageBuild          = "build"
	StagePull           = "pull"
	StageApply          = "apply"
	StageVerify         = "verify"
	StageRecord         = "record"
)

// lockStore is the part of the per-computer state store update needs. The
// concrete *state.Store implements it; tests use the real store with a temp
// directory so lock and record behavior are not mocked.
type lockStore interface {
	AcquireStackLock(ctx context.Context, target core.Target, holder state.LockHolder) (*state.StackLock, error)
	AllocateName(ctx context.Context, req state.NameRequest) (state.NameAllocation, error)
	LookupName(target core.Target) (state.NameAllocation, bool, error)
	LoadGoodVersion(target core.Target) (state.GoodVersion, bool, error)
	SaveGoodVersion(ctx context.Context, target core.Target, rec state.GoodVersion) error
}

// Controller owns update behavior. CLI and TUI both reach it through the common
// engine; the interfaces never reimplement a safety rule.
type Controller struct {
	updater  stack.Updater
	identity stack.Identity
	store    core.StateStore
	clock    core.Clock
	pid      int

	// Test seams: the readiness loop uses the injected clock and sleep so the
	// bounded behavior is exercised without real waits.
	poll   time.Duration
	settle time.Duration
	now    func() time.Time
	sleep  func(time.Duration)
}

// New builds the controller from the published narrow boundaries.
func New(updater stack.Updater, identity stack.Identity, store core.StateStore, clock core.Clock) *Controller {
	if clock == nil {
		clock = core.SystemClock{}
	}
	return &Controller{updater: updater, identity: identity, store: store, clock: clock, pid: os.Getpid(), now: time.Now}
}

// OptionsReport is the machine-readable record of the four independent choices.
type OptionsReport struct {
	NoCache        bool `json:"no_cache"`
	BuildPull      bool `json:"build_pull"`
	ImagePull      bool `json:"image_pull"`
	ForceRecreate  bool `json:"force_recreate"`
	TimeoutSeconds int  `json:"timeout_seconds"`
}

// Record status values. A record failure never turns a completed update into a
// failure, but it is never silent either.
const (
	RecordRecorded = "recorded"
	RecordFailed   = "failed"
	RecordSkipped  = "skipped"
)

// Report is the machine-readable data of an update, published as data.update.
type Report struct {
	ProjectName       string                   `json:"project_name"`
	ProjectNameSource string                   `json:"project_name_source"`
	Scope             string                   `json:"scope"`
	Services          []string                 `json:"services"`
	Options           OptionsReport            `json:"options"`
	Plan              Plan                     `json:"plan"`
	Built             []string                 `json:"built"`
	Pulled            []string                 `json:"pulled"`
	Applied           bool                     `json:"applied"`
	Verified          bool                     `json:"verified"`
	Readiness         []stack.ServiceReadiness `json:"readiness"`
	RecordStatus      string                   `json:"record_status"`
	GoodVersion       *state.GoodVersion       `json:"good_version,omitempty"`
	RecordDetail      string                   `json:"record_detail,omitempty"`
	Notices           []string                 `json:"notices"`
}

// Payload is the feature result payload. JSON consumers read data.update.
type Payload struct {
	Update Report `json:"update"`
}

// Run executes one explicit update. Stages: resolve_project, coordinate,
// allocate_name, plan, build, pull, apply, verify, record. The change lock is
// taken before the name claim so a blocked operation changes nothing.
func (c *Controller) Run(ctx context.Context, in core.Invocation, opts Options) (Payload, error) {
	if c == nil || c.updater == nil || c.identity == nil || c.store == nil {
		return Payload{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoUpdater,
			NextActions: []string{msgNoUpdaterAct},
		})
	}
	ls, ok := c.store.(lockStore)
	if !ok {
		return Payload{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoState,
			NextActions: []string{msgNoStateAct},
		})
	}
	opts = opts.withDefaults()
	clock := c.clock
	if in.Clock != nil {
		clock = in.Clock
	}

	services, err := normalizeServices(in.Services)
	if err != nil {
		return Payload{}, err
	}
	target := in.Target
	var notices []string

	// Project identity follows the same published rules as every stack change:
	// explicit -p, declared Compose name, recorded allocation, then the smallest
	// free readable name. Name allocation is serialized by the state store.
	declared := ""
	declaredSource := state.NameSource("")
	base := ""
	var allocation state.NameAllocation
	err = core.RunStage(in.Reporter, StageResolveProject, msgStageResolveProject, func() error {
		cfg, err := c.identity.ConfigInfo(ctx, target)
		if err != nil {
			return err
		}
		if err := validateServices(services, cfg.Services); err != nil {
			return err
		}
		explicit := strings.TrimSpace(target.ProjectName) != ""
		if explicit {
			declared = cfg.EffectiveName
			if declared == "" {
				declared = strings.TrimSpace(target.ProjectName)
			}
			declaredSource = state.NameSourceExplicit
			return nil
		}
		derived, err := c.identity.DerivedProjectName(ctx, target)
		if err != nil {
			return err
		}
		base = derived
		if cfg.EffectiveName != "" && cfg.EffectiveName != derived {
			declared = cfg.EffectiveName
			declaredSource = state.NameSourceConfigured
		}
		return nil
	})
	if err != nil {
		return Payload{}, err
	}

	// Same-stack coordination starts before the name claim. The lock key is
	// already known (project folder + environment), so a concurrent up/update/
	// backup on this stack fails as stack_busy without persisting a rename
	// claim that a later run would adopt as a second project. The lock covers
	// plan (baseline read), build, pull, apply, verify and record, and is always
	// released.
	var lock *state.StackLock
	err = core.RunStage(in.Reporter, StageCoordinate, msgStageCoordinate, func() error {
		var err error
		lock, err = ls.AcquireStackLock(ctx, target, state.LockHolder{
			Action: "update", PID: c.pid, StartedAt: clock.Now(),
		})
		if err == nil {
			return nil
		}
		var busy *state.BusyError
		if errors.As(err, &busy) {
			return core.Fail(stackBusyDiagnostic(busy))
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return core.FailCause(err, stateFailedDiagnostic(err))
	})
	if err != nil {
		return Payload{}, err
	}
	defer func() { _ = lock.Release() }()

	err = core.RunStage(in.Reporter, StageAllocateName, msgStageAllocateName, func() error {
		projects, err := c.identity.Projects(ctx)
		if err != nil {
			return err
		}
		external := make([]state.ExternalProject, 0, len(projects))
		for _, p := range projects {
			external = append(external, state.ExternalProject{Name: p.Name, ProjectDir: p.ProjectDir})
		}
		allocation, err = ls.AllocateName(ctx, state.NameRequest{
			Target:         target,
			DeclaredName:   declared,
			DeclaredSource: declaredSource,
			BaseName:       base,
			External:       external,
		})
		if err != nil {
			return translateNameError(err, target)
		}
		return nil
	})
	if err != nil {
		return Payload{}, err
	}
	opTarget := target
	opTarget.ProjectName = allocation.Name

	var model stack.UpdateModel
	var baseline map[string]int
	var plan Plan
	err = core.RunStage(in.Reporter, StagePlan, msgStagePlan, func() error {
		var err error
		model, err = c.updater.UpdateModel(ctx, opTarget)
		if err != nil {
			return err
		}
		containers, err := c.updater.Containers(ctx, opTarget)
		if err != nil {
			return err
		}
		baseline = make(map[string]int, len(containers))
		for _, container := range containers {
			baseline[container.ID] = container.RestartCount
		}
		plan = buildPlan(in, allocation.Name, model, services, opts)
		if !opts.Pull {
			// Fail before building or applying when the user disabled the image
			// refresh and a required local image is absent.
			if err := c.checkLocalImages(ctx, model, imageOnlyTargets(model, services)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Payload{}, err
	}
	defs := targetDefinitions(model, services)

	// Capture the deployment that is running *before* anything is built,
	// pulled or recreated. A verified-good update publishes this captured
	// deployment as the single previous-good record, so rollback restores the
	// version that actually preceded the update. Tags are created here, before
	// apply, so the images survive replacement even after a failed run.
	capture, captureErr := c.capturePreUpdate(ctx, in, opTarget, allocation.Name, model)
	if captureErr != nil && isCanceled(ctx, captureErr) {
		return Payload{}, ctxError(ctx, captureErr)
	}

	built := []string{}
	pulled := []string{}
	if len(plan.BuildServices) == 0 {
		skipStage(in.Reporter, StageBuild, msgStageBuild, msgBuildSkipped)
		notices = append(notices, msgBuildSkipped)
		if opts.NoCache || opts.BuildPull {
			notices = append(notices, msgBuildOptIgnore)
		}
	} else {
		err = core.RunStage(in.Reporter, StageBuild, msgStageBuild, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			result, buildErr := c.updater.Build(ctx, opTarget, plan.BuildServices, stack.BuildOptions{
				NoCache: opts.NoCache,
				Pull:    opts.BuildPull,
			})
			notices = append(notices, result.Notices...)
			if buildErr != nil {
				if ctx.Err() != nil || errors.Is(buildErr, context.Canceled) || errors.Is(buildErr, context.DeadlineExceeded) {
					return ctxError(ctx, buildErr)
				}
				return c.stageFailure(ctx, opTarget, msgStageBuild, buildErr, result.Output)
			}
			return nil
		})
		if err != nil {
			return Payload{}, err
		}
		built = append(built, plan.BuildServices...)
	}

	if !opts.Pull {
		skipStage(in.Reporter, StagePull, msgStagePull, msgNoPullNotice)
		notices = append(notices, msgNoPullNotice)
	} else if len(plan.PullServices) == 0 {
		skipStage(in.Reporter, StagePull, msgStagePull, msgPullSkipped)
		notices = append(notices, msgPullSkipped)
	} else {
		err = core.RunStage(in.Reporter, StagePull, msgStagePull, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			result, pullErr := c.updater.Pull(ctx, opTarget, plan.PullServices)
			if pullErr != nil {
				hint := ""
				if looksLikePullDenied(result.Output + " " + pullErr.Error()) {
					hint = msgPullFailedHint
				}
				return c.stageFailure(ctx, opTarget, msgStagePull, pullErr, hint)
			}
			return nil
		})
		if err != nil {
			return Payload{}, err
		}
		pulled = append(pulled, plan.PullServices...)
	}

	err = core.RunStage(in.Reporter, StageApply, msgStageApply, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		policy := "missing"
		if !opts.Pull {
			policy = "never"
		}
		result, applyErr := c.updater.Apply(ctx, opTarget, services, stack.ApplyOptions{
			ForceRecreate: opts.ForceRecreate,
			PullPolicy:    policy,
		})
		if applyErr != nil {
			if ctx.Err() != nil || errors.Is(applyErr, context.Canceled) || errors.Is(applyErr, context.DeadlineExceeded) {
				return ctxError(ctx, applyErr)
			}
			return c.stageFailure(ctx, opTarget, msgStageApply, applyErr, result.Output)
		}
		return nil
	})
	if err != nil {
		return Payload{}, err
	}
	if err := ctx.Err(); err != nil {
		return Payload{}, err
	}

	var outcome readiness.Outcome
	if in.Reporter != nil {
		in.Reporter.Begin(StageVerify, msgStageVerify)
	}
	outcome, err = c.observe(ctx, opTarget, defs, baseline, opts.Timeout)
	if err != nil {
		if in.Reporter != nil {
			status := core.StageFailed
			if isCanceled(ctx, err) {
				status = core.StageCanceled
			}
			in.Reporter.End(StageVerify, status, "")
		}
		return Payload{}, ctxError(ctx, err)
	}
	if !readiness.AllSatisfied(outcome.Services) {
		if in.Reporter != nil {
			in.Reporter.End(StageVerify, core.StageFailed, readiness.Summary(outcome.Services))
		}
		return Payload{}, readinessFailure(outcome, opts.Timeout)
	}
	if in.Reporter != nil {
		in.Reporter.End(StageVerify, core.StageSucceeded, readiness.Summary(outcome.Services))
	}

	payload := Payload{Update: Report{
		ProjectName:       allocation.Name,
		ProjectNameSource: string(allocation.Source),
		Scope:             scopeOf(services),
		Services:          append([]string{}, services...),
		Options: OptionsReport{
			NoCache:        opts.NoCache,
			BuildPull:      opts.BuildPull,
			ImagePull:      opts.Pull,
			ForceRecreate:  opts.ForceRecreate,
			TimeoutSeconds: int(opts.Timeout / time.Second),
		},
		Plan:         plan,
		Built:        built,
		Pulled:       pulled,
		Applied:      true,
		Verified:     outcome.Verified,
		Readiness:    outcome.Services,
		RecordStatus: RecordSkipped,
		Notices:      notices,
	}}
	if len(services) > 0 {
		payload.Update.Notices = append(payload.Update.Notices, msgGoodMultiNotice)
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return Payload{}, ctxErr
	}
	if captureErr != nil {
		payload.Update.RecordStatus = RecordFailed
		payload.Update.RecordDetail = recordErrorDetail(captureErr)
		payload.Update.Notices = append(payload.Update.Notices, msgGoodRecordFailed)
		skipStage(in.Reporter, StageRecord, msgStageRecord, msgGoodRecordFailed)
	} else if capture.record == nil {
		// There was no healthy running pre-update deployment. dcraft does not
		// invent a record and never overwrites an existing one.
		payload.Update.RecordStatus = RecordSkipped
		payload.Update.Notices = append(payload.Update.Notices, msgGoodNotCaptured)
		skipStage(in.Reporter, StageRecord, msgStageRecord, msgGoodNotCaptured)
	} else {
		var recorded *state.GoodVersion
		recordErr := core.RunStage(in.Reporter, StageRecord, msgStageRecord, func() error {
			rec, err := c.publishCaptured(ctx, in, *capture.record)
			if err != nil {
				return err
			}
			recorded = &rec
			return nil
		})
		if recordErr != nil {
			if isCanceled(ctx, recordErr) {
				return Payload{}, ctxError(ctx, recordErr)
			}
			payload.Update.RecordStatus = RecordFailed
			payload.Update.RecordDetail = recordErrorDetail(recordErr)
			payload.Update.Notices = append(payload.Update.Notices, msgGoodRecordFailed)
		} else {
			// SaveGoodVersion checks cancellation immediately before publishing.
			// A cancellation that arrives after the rename means the record is
			// already complete; don't roll back a completed commit, and don't
			// report the whole verified update as canceled. Report success with a
			// notice instead.
			if ctx.Err() != nil {
				payload.Update.Notices = append(payload.Update.Notices, msgGoodLateCancel)
			}
			payload.Update.RecordStatus = RecordRecorded
			payload.Update.GoodVersion = recorded
			payload.Update.Notices = append(payload.Update.Notices, msgGoodRecorded, msgGoodRetentionNote)
		}
	}
	payload.Update.Notices = nonNil(payload.Update.Notices)
	return payload, nil
}

func normalizeServices(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, value := range raw {
		name := strings.TrimSpace(value)
		if name == "" {
			return nil, core.Fail(core.Diagnostic{
				Category:    core.CategoryServiceUnknown,
				Severity:    core.SeverityError,
				Message:     msgEmptyService,
				NextActions: []string{msgEmptyServiceAct},
			})
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

func validateServices(selected, configured []string) error {
	if len(selected) == 0 {
		return nil
	}
	known := map[string]bool{}
	for _, name := range configured {
		known[name] = true
	}
	var unknown []string
	for _, name := range selected {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryServiceUnknown,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf("갱신 대상 서비스를 찾지 못했습니다: %s", strings.Join(unknown, ", ")),
		Detail:      "사용 가능한 서비스: " + strings.Join(configured, ", "),
		NextActions: []string{"-s/--service에 설정된 서비스 이름을 사용하세요.", "dcraft status로 서비스 목록을 확인하세요."},
	})
}

func scopeOf(services []string) string {
	if len(services) > 0 {
		return "services"
	}
	return "stack"
}

func imageOnlyTargets(model stack.UpdateModel, selected []string) []string {
	var names []string
	for _, def := range targetDefinitions(model, selected) {
		if !def.Build && def.Image != "" {
			names = append(names, def.Name)
		}
	}
	return names
}

func (c *Controller) checkLocalImages(ctx context.Context, model stack.UpdateModel, services []string) error {
	byName := map[string]stack.ServiceDefinition{}
	for _, def := range model.Services {
		byName[def.Name] = def
	}
	var missing []string
	for _, name := range services {
		def := byName[name]
		if _, err := c.updater.ImageID(ctx, def.Image); err != nil {
			var missingErr *stack.ImageMissingError
			if errors.As(err, &missingErr) {
				missing = append(missing, def.Image)
				continue
			}
			return err
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryImageMissing,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf(msgImageMissing, strings.Join(missing, ", ")),
		Detail:      "no_pull",
		NextActions: []string{msgImageMissingAct, msgImageMissingAct2},
	})
}

// stageFailure builds the 'failed stage + observed state + recovery actions'
// diagnostic. Tool output is included as detail but never secret values are
// fetched by update.
func (c *Controller) stageFailure(ctx context.Context, target core.Target, stageTitle string, cause error, toolOutput string) error {
	observed := c.observedState(ctx, target)
	var output []string
	if strings.TrimSpace(toolOutput) != "" {
		output = append(output, strings.TrimSpace(toolOutput))
	}
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		output = append(output, cause.Error())
	}
	detail := strings.Join(output, "\n")
	actions := []string{msgUpdateFailedOne, msgUpdateFailedTwo, msgUpdateFailedThree, msgUpdateFailedFour}
	if strings.Contains(strings.ToLower(detail), "pull access denied") || strings.Contains(strings.ToLower(detail), "repository does not exist") {
		detail += "\n" + msgPullFailedHint
	}
	return core.FailCause(cause, core.Diagnostic{
		Category:    core.CategoryUpdateFailed,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf(msgUpdateFailed, stageTitle, observed),
		Detail:      truncate(detail, 900),
		NextActions: actions,
	})
}

func (c *Controller) observedState(ctx context.Context, target core.Target) string {
	containers, err := c.updater.Containers(ctx, target)
	if err != nil || ctx.Err() != nil {
		return msgStateUnreadable
	}
	if len(containers) == 0 {
		return msgStateNoContainer
	}
	running, restarts, exited := 0, 0, 0
	for _, container := range containers {
		if container.Running {
			running++
		}
		restarts += container.RestartCount
		if strings.EqualFold(container.State, "exited") {
			exited++
		}
	}
	return fmt.Sprintf("컨테이너 %d개 중 실행 %d개 · 재시작 합계 %d회 · 종료 %d개", len(containers), running, restarts, exited)
}

// preUpdateCapture is the deployment observed immediately before an update.
// A nil record with fresh=false means the pre-update deployment was not fully
// running and healthy, so no record may be published or replaced.
type preUpdateCapture struct {
	record *state.GoodVersion
}

// capturePreUpdate reads the currently running deployment and retains its
// images with dcraft-owned retention tags. It uses the existing record's
// execution configuration and secret references when that record still
// describes the running images; otherwise it records the current target and
// saved references. It never resolves or stores a secret value.
func (c *Controller) capturePreUpdate(ctx context.Context, in core.Invocation, target core.Target, projectName string, model stack.UpdateModel) (preUpdateCapture, error) {
	containers, err := c.updater.Containers(ctx, target)
	if err != nil {
		return preUpdateCapture{}, err
	}
	if len(containers) == 0 {
		return preUpdateCapture{}, nil
	}
	byService := map[string][]stack.Container{}
	for _, container := range containers {
		byService[container.Service] = append(byService[container.Service], container)
	}

	ls := c.store.(lockStore)
	capturedTarget := in.Target
	capturedTarget.ProjectDir = target.ProjectDir
	capturedTarget.ProjectName = projectName
	var secretRefs []state.SecretReference
	existing, found, loadErr := ls.LoadGoodVersion(target)
	if loadErr == nil && found && sameDeployment(existing, model, byService) {
		// The record describes exactly the images that are running now, so
		// its file list and references are the pre-update configuration even
		// if .dcraft.yaml already changed for the upcoming update.
		capturedTarget.Files = append([]string{}, existing.Target.Files...)
		capturedTarget.EnvFiles = append([]string{}, existing.Target.EnvFiles...)
		capturedTarget.Profiles = append([]string{}, existing.Target.Profiles...)
		capturedTarget.DefaultEnvFile = existing.Target.DefaultEnvFile
		capturedTarget.Environment = in.Target.Environment
		secretRefs = append([]state.SecretReference{}, existing.Target.SecretRefs...)
	} else {
		secretRefs, err = goodVersionSecretRefs(target)
		if err != nil {
			return preUpdateCapture{}, err
		}
	}

	services := make([]state.GoodService, 0, len(model.Services))
	for _, def := range model.Services {
		records := byService[def.Name]
		if len(records) == 0 {
			return preUpdateCapture{}, nil
		}
		imageID := records[0].ImageID
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				return preUpdateCapture{}, err
			}
			if !record.Running || !strings.EqualFold(record.State, "running") {
				return preUpdateCapture{}, nil
			}
			if def.Healthcheck && !strings.EqualFold(record.Health, "healthy") {
				return preUpdateCapture{}, nil
			}
			if record.ImageID == "" || record.ImageID != imageID {
				return preUpdateCapture{}, nil
			}
		}
		if imageID == "" {
			return preUpdateCapture{}, nil
		}
		if err := ctx.Err(); err != nil {
			return preUpdateCapture{}, err
		}
		imageRef := records[0].ImageRef
		if imageRef == "" {
			imageRef = def.Image
		}
		tag := retentionTag(projectName, def.Name, imageID)
		if err := c.tagPreUpdateImage(ctx, imageID, imageRef, tag); err != nil {
			return preUpdateCapture{}, err
		}
		services = append(services, state.GoodService{
			Service:      def.Name,
			ImageID:      imageID,
			ImageRef:     imageRef,
			RetentionTag: tag,
			Build:        def.Build,
			Healthcheck:  def.Healthcheck,
		})
	}
	if len(services) == 0 {
		return preUpdateCapture{}, nil
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Service < services[j].Service })
	return preUpdateCapture{record: &state.GoodVersion{
		Version: state.GoodVersionVersion,
		Target: state.GoodTarget{
			ProjectDir:     capturedTarget.ProjectDir,
			Files:          append([]string{}, capturedTarget.Files...),
			EnvFiles:       append([]string{}, capturedTarget.EnvFiles...),
			Profiles:       append([]string{}, capturedTarget.Profiles...),
			ProjectName:    projectName,
			Environment:    in.Target.Environment,
			DefaultEnvFile: capturedTarget.DefaultEnvFile,
			SecretRefs:     secretRefs,
		},
		Services: services,
	}}, nil
}

// tagPreUpdateImage retains the pre-update image. The full image ID is the
// authoritative identity, but some container image stores briefly refuse
// `docker tag` on a just-started container's ID while the Compose tag already
// resolves to the same image. Both spellings are bounded retries; the tag is
// verified afterwards by TagImage, so the recorded image ID never changes.
func (c *Controller) tagPreUpdateImage(ctx context.Context, imageID, imageRef, tag string) error {
	refs := []string{imageID}
	if imageRef != "" && imageRef != imageID {
		refs = append(refs, imageRef)
	}
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		for _, ref := range refs {
			if err := c.updater.TagImageFrom(ctx, ref, imageID, tag); err != nil {
				if isCanceled(ctx, err) {
					return err
				}
				last = err
				continue
			}
			return nil
		}
	}
	return last
}

// sameDeployment reports whether an existing record still describes every
// running container of the current model. It is the guard that keeps a stale
// record's references from being attributed to a different deployment.
func sameDeployment(existing state.GoodVersion, model stack.UpdateModel, byService map[string][]stack.Container) bool {
	if len(existing.Services) != len(model.Services) || len(existing.Target.Files) == 0 {
		return false
	}
	recorded := make(map[string]string, len(existing.Services))
	for _, svc := range existing.Services {
		recorded[svc.Service] = svc.ImageID
	}
	for _, def := range model.Services {
		wantID, ok := recorded[def.Name]
		if !ok {
			return false
		}
		records := byService[def.Name]
		if len(records) == 0 {
			return false
		}
		for _, record := range records {
			if record.ImageID != wantID {
				return false
			}
		}
	}
	return true
}

// publishCaptured stores the pre-update deployment captured before apply. It
// is only called after readiness verified the update and while the stack lock
// is held. Cancellation is checked immediately before the atomic publish.
func (c *Controller) publishCaptured(ctx context.Context, in core.Invocation, rec state.GoodVersion) (state.GoodVersion, error) {
	recordedAt := c.clock.Now()
	if in.Clock != nil {
		recordedAt = in.Clock.Now()
	}
	rec.RecordedAt = recordedAt
	rec.Version = state.GoodVersionVersion
	if err := ctx.Err(); err != nil {
		return state.GoodVersion{}, err
	}
	ls := c.store.(lockStore)
	if err := ls.SaveGoodVersion(ctx, in.Target, rec); err != nil {
		return state.GoodVersion{}, err
	}
	return rec, nil
}

// recordErrorDetail keeps the diagnostic detail (never a secret value) so a
// failed record write is actionable instead of only its category.
func recordErrorDetail(err error) string {
	if err == nil {
		return ""
	}
	var ce *core.Error
	if errors.As(err, &ce) && len(ce.Diagnostics) > 0 {
		parts := make([]string, 0, len(ce.Diagnostics))
		for _, d := range ce.Diagnostics {
			line := string(d.Category) + ": " + d.Message
			if strings.TrimSpace(d.Detail) != "" {
				line += " (" + truncate(d.Detail, 300) + ")"
			}
			parts = append(parts, line)
		}
		return strings.Join(parts, "; ")
	}
	return err.Error()
}

func readinessFailure(outcome readiness.Outcome, timeout time.Duration) error {
	summary := readiness.Summary(outcome.Services)
	message := fmt.Sprintf(msgReadinessFailed, summary)
	if outcome.TimedOut {
		message = fmt.Sprintf(msgReadinessTimeout, formatTimeout(timeout), summary)
	}
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryReadinessFailed,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      readiness.InstanceSummary(outcome.Services, msgReadinessNoState, false),
		NextActions: []string{msgReadinessFailedOne, msgReadinessFailedTwo, msgReadinessFailed3, msgReadinessFailed4},
	})
}

func translateNameError(err error, target core.Target) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var conflict *state.NameConflictError
	if errors.As(err, &conflict) {
		owner := conflict.OwnerDir
		if conflict.OwnerEnvironment != "" {
			owner += " (환경 " + conflict.OwnerEnvironment + ")"
		}
		if conflict.External {
			owner += " (dcraft 관리 밖의 기존 Compose 프로젝트)"
		}
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryProjectNameConflict,
			Severity:    core.SeverityError,
			Message:     fmt.Sprintf("%s: %s", msgNameConflict, conflict.Name),
			Detail:      fmt.Sprintf("요청한 폴더: %s; 사용 중인 폴더: %s", target.ProjectDir, owner),
			NextActions: []string{msgNameConflict1, msgNameConflict2, msgNameConflict3},
		})
	}
	return core.FailCause(err, stateFailedDiagnostic(err))
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
		NextActions: []string{msgStackBusyOne, msgStackBusyTwo},
	}
}

func stateFailedDiagnostic(err error) core.Diagnostic {
	return core.Diagnostic{
		Category:    core.CategoryStateFailed,
		Severity:    core.SeverityError,
		Message:     msgStateFailed,
		Detail:      truncate(err.Error(), 500),
		NextActions: []string{msgStateFailOne, msgStateFailTwo},
	}
}

func skipStage(r core.Reporter, name, title, detail string) {
	if r == nil {
		return
	}
	r.Begin(name, title)
	r.End(name, core.StageSkipped, detail)
}

func isCanceled(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return ctx.Err() != nil
}

func ctxError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return err
}

func looksLikePullDenied(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "pull access denied") || strings.Contains(lower, "repository does not exist")
}

func truncate(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return text[:max] + "…"
}

func nonNil(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

// goodVersionSecretRefs maps the project's saved references to the neutral
// record shape. It reads only .dcraft.yaml; no source is resolved and no value
// can enter the record.
func goodVersionSecretRefs(target core.Target) ([]state.SecretReference, error) {
	cat, err := secretref.Load(target.ProjectDir)
	if err != nil {
		return nil, err
	}
	refs := make([]state.SecretReference, 0, len(cat.References))
	for _, ref := range cat.References {
		refs = append(refs, state.SecretReference{
			Service:  ref.Service,
			Phase:    string(ref.Phase),
			Delivery: string(ref.Delivery),
			Source:   string(ref.Source),
			Name:     ref.Ref,
			Target:   ref.EffectiveTarget(),
			Field:    ref.Field,
			Region:   ref.Region,
			Profile:  ref.Profile,
		})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Service != refs[j].Service {
			return refs[i].Service < refs[j].Service
		}
		if refs[i].Delivery != refs[j].Delivery {
			return refs[i].Delivery < refs[j].Delivery
		}
		return refs[i].Target < refs[j].Target
	})
	return refs, nil
}
