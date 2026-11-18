package rollback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

// fakeAWSChecker proves availability without a value and records the neutral
// reference rollback asked about.
type fakeAWSChecker struct {
	err  error
	refs []secretref.Reference
}

func (f *fakeAWSChecker) CheckAWS(_ context.Context, ref secretref.Reference) error {
	f.refs = append(f.refs, ref)
	return f.err
}

// ---- fakes -------------------------------------------------------------

type fakeUpdater struct {
	model      stack.UpdateModel
	containers []stack.Container
	images     map[string]string
	modelErr   error
}

func (f *fakeUpdater) UpdateModel(context.Context, core.Target) (stack.UpdateModel, error) {
	return f.model, f.modelErr
}

func (f *fakeUpdater) Containers(context.Context, core.Target) ([]stack.Container, error) {
	return f.containers, nil
}

func (f *fakeUpdater) Build(context.Context, core.Target, []string, stack.BuildOptions) (stack.OpResult, error) {
	return stack.OpResult{}, errors.New("rollback must not build")
}

func (f *fakeUpdater) Pull(context.Context, core.Target, []string) (stack.OpResult, error) {
	return stack.OpResult{}, errors.New("rollback must not pull")
}

func (f *fakeUpdater) Apply(context.Context, core.Target, []string, stack.ApplyOptions) (stack.OpResult, error) {
	return stack.OpResult{}, errors.New("rollback must not use the update apply path")
}

func (f *fakeUpdater) ImageID(_ context.Context, ref string) (string, error) {
	if id, ok := f.images[ref]; ok {
		return id, nil
	}
	return "", &stack.ImageMissingError{Ref: ref}
}

func (f *fakeUpdater) TagImage(context.Context, string, string) error {
	return errors.New("rollback must not create tags")
}

type fakeApplier struct {
	calls    int
	targets  []core.Target
	services [][]string
	pins     [][]stack.RollbackPin
	err      error
	after    func()
}

func (f *fakeApplier) ApplyRollback(_ context.Context, target core.Target, services []string, pins []stack.RollbackPin) (stack.OpResult, error) {
	f.calls++
	f.targets = append(f.targets, target)
	f.services = append(f.services, append([]string{}, services...))
	f.pins = append(f.pins, append([]stack.RollbackPin{}, pins...))
	if f.after != nil {
		f.after()
	}
	return stack.OpResult{Output: "applied"}, f.err
}

type fakeIdentity struct {
	cfg stack.ConfigInfo
}

func (f *fakeIdentity) ConfigInfo(context.Context, core.Target) (stack.ConfigInfo, error) {
	return f.cfg, nil
}

func (f *fakeIdentity) DerivedProjectName(context.Context, core.Target) (string, error) {
	return "project", nil
}

func (f *fakeIdentity) Projects(context.Context) ([]stack.ProjectInfo, error) {
	return []stack.ProjectInfo{}, nil
}

type stageLog struct {
	begun []string
	ended []string
}

func (r *stageLog) Begin(name, _ string) { r.begun = append(r.begun, name) }
func (r *stageLog) End(name string, status core.StageStatus, _ string) {
	r.ended = append(r.ended, name+":"+string(status))
}

// ---- fixtures ----------------------------------------------------------

const testImage = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
const testOtherImage = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
const testTag = "dcraft-good/project-web:1111111111111111"

func newTestTarget(t *testing.T) core.Target {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return core.Target{
		ProjectDir:  dir,
		Files:       []string{filepath.Join(dir, "compose.yaml")},
		ProjectName: "project",
	}
}

func testRecord(target core.Target) state.GoodVersion {
	return state.GoodVersion{
		Version:    state.GoodVersionVersion,
		RecordedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
		Target: state.GoodTarget{
			ProjectDir:  target.ProjectDir,
			Files:       append([]string{}, target.Files...),
			EnvFiles:    []string{},
			Profiles:    []string{},
			ProjectName: target.ProjectName,
			SecretRefs:  []state.SecretReference{},
		},
		Services: []state.GoodService{{
			Service: "web", ImageID: testImage, RetentionTag: testTag, Healthcheck: true,
		}},
	}
}

func testModel() stack.UpdateModel {
	return stack.UpdateModel{
		ProjectName: "project",
		Services: []stack.ServiceDefinition{{
			Name: "web", Image: "registry/web:1", Healthcheck: true,
		}},
	}
}

func saveRecord(t *testing.T, store *state.Store, target core.Target, rec state.GoodVersion) {
	t.Helper()
	if err := store.SaveGoodVersion(target, rec); err != nil {
		t.Fatal(err)
	}
}

func newController(u stack.Updater, a stack.Rollbacker, store core.StateStore) *Controller {
	c := New(u, a, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}}, func(string) string { return "" }, store, core.SystemClock{})
	configureTestClock(c)
	return c
}

func configureTestClock(c *Controller) {
	c.poll = time.Second
	c.settle = 2 * time.Second
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	c.sleep = func(d time.Duration) { now = now.Add(d) }
}

func healthyContainer(service, imageID string, hasHealth bool, health string) stack.Container {
	return stack.Container{
		ID: "c-" + service, Name: "project-" + service + "-1", Service: service,
		ImageID: imageID, State: "running", Running: true,
		Health: health, HasHealth: hasHealth,
	}
}

func invocationFor(target core.Target) core.Invocation {
	return core.Invocation{
		Target:   target,
		Options:  map[string]string{},
		Clock:    core.SystemClock{},
		Reporter: &stageLog{},
	}
}

func newControllerWithEnv(u stack.Updater, a stack.Rollbacker, store core.StateStore, getenv func(string) string) *Controller {
	c := New(u, a, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}}, getenv, store, core.SystemClock{})
	configureTestClock(c)
	return c
}

func requireCategory(t *testing.T, err error, category core.Category) {
	t.Helper()
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not *core.Error", err)
	}
	for _, d := range ce.Diagnostics {
		if d.Category == category {
			return
		}
	}
	t.Fatalf("diagnostics = %+v, want category %q", ce.Diagnostics, category)
}

// ---- tests -------------------------------------------------------------

// A stack without a previous-good record (first run, or a stack created before
// dcraft managed it) fails before any change and never invents a version.
func TestRunWithoutRecordFailsBeforeChanging(t *testing.T) {
	target := newTestTarget(t)
	store := state.New(t.TempDir())
	applier := &fakeApplier{}
	c := newController(&fakeUpdater{images: map[string]string{}}, applier, store)

	_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
	requireCategory(t, err, core.CategoryRollbackUnavailable)
	if applier.calls != 0 {
		t.Fatalf("rollback changed the stack without a record: %d apply calls", applier.calls)
	}
}

// A missing image ID or retention tag (or a tag moved to another image) stops
// the rollback before the stack changes; a newer image is never substituted.
func TestRunMissingRecordedImageAbortsBeforeApply(t *testing.T) {
	cases := []struct {
		name   string
		images map[string]string
	}{
		{"image id missing", map[string]string{testTag: testImage}},
		{"retention tag missing", map[string]string{testImage: testImage}},
		{"retention tag moved", map[string]string{testImage: testImage, testTag: testOtherImage}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := newTestTarget(t)
			store := state.New(t.TempDir())
			saveRecord(t, store, target, testRecord(target))
			applier := &fakeApplier{}
			u := &fakeUpdater{model: testModel(), images: tc.images}
			c := newController(u, applier, store)

			_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
			requireCategory(t, err, core.CategoryRollbackImageMissing)
			if applier.calls != 0 {
				t.Fatalf("rollback applied despite a missing image: %d calls", applier.calls)
			}
		})
	}
}

// A recorded Compose file that no longer exists aborts before any change.
func TestRunMissingRecordedFileAbortsBeforeApply(t *testing.T) {
	target := newTestTarget(t)
	store := state.New(t.TempDir())
	saveRecord(t, store, target, testRecord(target))
	applier := &fakeApplier{}
	c := newController(&fakeUpdater{model: testModel(), images: map[string]string{
		testImage: testImage, testTag: testImage,
	}}, applier, store)

	if err := os.Remove(target.Files[0]); err != nil {
		t.Fatal(err)
	}
	_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
	requireCategory(t, err, core.CategoryRollbackInputMissing)
	if applier.calls != 0 {
		t.Fatalf("rollback applied despite a missing recorded file: %d calls", applier.calls)
	}
}

// A valid record applies exactly the recorded images with the recorded
// execution configuration, verifies the real image ID and keeps the record.
func TestRunAppliesRecordedImagesAndVerifies(t *testing.T) {
	target := newTestTarget(t)
	store := state.New(t.TempDir())
	saveRecord(t, store, target, testRecord(target))
	recordPath, err := store.GoodVersionPath(target)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	applier := &fakeApplier{}
	u := &fakeUpdater{
		model:      testModel(),
		images:     map[string]string{testImage: testImage, testTag: testImage},
		containers: []stack.Container{healthyContainer("web", testImage, true, "healthy")},
	}
	c := newController(u, applier, store)

	payload, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if applier.calls != 1 {
		t.Fatalf("apply calls = %d, want 1", applier.calls)
	}
	if got := applier.services[0]; len(got) != 1 || got[0] != "web" {
		t.Fatalf("applied services = %v", got)
	}
	if got := applier.pins[0]; len(got) != 1 || got[0].ImageID != testImage || got[0].RetentionTag != testTag {
		t.Fatalf("applied pins = %+v", got)
	}
	if applier.targets[0].ProjectName != "project" || applier.targets[0].Files[0] != target.Files[0] {
		t.Fatalf("applied target = %+v", applier.targets[0])
	}
	report := payload.Rollback
	if !report.Applied || !report.Verified || !report.ImageIDsVerified {
		t.Fatalf("report = %+v", report)
	}
	if report.RecordStatus != RecordUnchanged || report.DataRestored || report.HooksRun {
		t.Fatalf("report honesty fields = %+v", report)
	}
	if len(report.Readiness) != 1 || report.Readiness[0].Status != stack.ReadinessHealthy {
		t.Fatalf("readiness = %+v", report.Readiness)
	}
	after, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("rollback changed the good record:\nbefore=%s\nafter=%s", before, after)
	}
}

// A successful apply whose running image differs from the record is a
// verification failure, never success.
func TestRunImageMismatchAfterApplyFails(t *testing.T) {
	target := newTestTarget(t)
	store := state.New(t.TempDir())
	saveRecord(t, store, target, testRecord(target))
	applier := &fakeApplier{}
	u := &fakeUpdater{
		model:      testModel(),
		images:     map[string]string{testImage: testImage, testTag: testImage},
		containers: []stack.Container{healthyContainer("web", testOtherImage, true, "healthy")},
	}
	c := newController(u, applier, store)

	_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: 30 * time.Second})
	requireCategory(t, err, core.CategoryRollbackVerifyFailed)
}

// Unhealthy, exited and restarting states after apply are failures, not
// partial successes.
func TestRunUnhealthyAfterApplyFails(t *testing.T) {
	cases := []struct {
		name      string
		container stack.Container
		timeout   time.Duration
		want      core.Category
	}{
		{
			name:      "unhealthy",
			container: healthyContainer("web", testImage, true, "unhealthy"),
			timeout:   30 * time.Second,
			want:      core.CategoryReadinessFailed,
		},
		{
			name: "early exit",
			container: stack.Container{
				ID: "c-web", Name: "project-web-1", Service: "web",
				ImageID: testImage, State: "exited", ExitCode: 3,
			},
			timeout: 30 * time.Second,
			want:    core.CategoryReadinessFailed,
		},
		{
			name: "crash loop",
			container: stack.Container{
				ID: "c-web", Name: "project-web-1", Service: "web",
				ImageID: testImage, State: "restarting", Restarting: true, RestartCount: 4,
			},
			timeout: 30 * time.Second,
			want:    core.CategoryReadinessFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := newTestTarget(t)
			store := state.New(t.TempDir())
			saveRecord(t, store, target, testRecord(target))
			applier := &fakeApplier{}
			u := &fakeUpdater{
				model:      testModel(),
				images:     map[string]string{testImage: testImage, testTag: testImage},
				containers: []stack.Container{tc.container},
			}
			c := newController(u, applier, store)
			_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: tc.timeout})
			requireCategory(t, err, tc.want)
		})
	}
}

// A canceled apply is reported as canceled, never as success, and leaves the
// record untouched.
func TestRunCanceledApplyIsNotSuccess(t *testing.T) {
	target := newTestTarget(t)
	store := state.New(t.TempDir())
	saveRecord(t, store, target, testRecord(target))
	recordPath, err := store.GoodVersionPath(target)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	applier := &fakeApplier{err: context.Canceled}
	u := &fakeUpdater{
		model:  testModel(),
		images: map[string]string{testImage: testImage, testTag: testImage},
	}
	c := newController(u, applier, store)

	_, err = c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	after, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("canceled rollback changed the good record")
	}
}

// Repeating the same rollback is idempotent: the same record applies the same
// images and reports that no older version exists.
func TestRunRepeatedRollbackKeepsSameVersion(t *testing.T) {
	target := newTestTarget(t)
	store := state.New(t.TempDir())
	saveRecord(t, store, target, testRecord(target))
	recordPath, err := store.GoodVersionPath(target)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	applier := &fakeApplier{}
	u := &fakeUpdater{
		model:      testModel(),
		images:     map[string]string{testImage: testImage, testTag: testImage},
		containers: []stack.Container{healthyContainer("web", testOtherImage, true, "healthy")},
	}
	applier.after = func() {
		u.containers = []stack.Container{healthyContainer("web", testImage, true, "healthy")}
	}
	c := newController(u, applier, store)

	first, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("first rollback: %v", err)
	}
	if first.Rollback.AlreadyAtVersion {
		t.Fatal("the first rollback was reported as already at the recorded version")
	}
	second, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("second rollback: %v", err)
	}
	if !second.Rollback.AlreadyAtVersion {
		t.Fatal("repeated rollback did not report that the recorded version is already running")
	}
	if !strings.Contains(strings.Join(second.Rollback.Notices, "\n"), "더 오래된 버전은 없습니다") {
		t.Fatalf("notices = %v", second.Rollback.Notices)
	}
	after, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("repeated rollback changed the good record")
	}
}

// Recorded secret references are re-resolved before any change. Missing,
// empty, unsupported and unknown sources abort; values are never reported.
func TestRunMissingSecretSourceAbortsBeforeApply(t *testing.T) {
	const envSecret = "rb-secret-value"

	t.Run("recorded env missing", func(t *testing.T) {
		target := newTestTarget(t)
		store := state.New(t.TempDir())
		rec := testRecord(target)
		rec.Target.SecretRefs = []state.SecretReference{{
			Service: "web", Phase: "runtime", Delivery: "environment", Source: "env", Name: "APP_TOKEN",
		}}
		saveRecord(t, store, target, rec)
		applier := &fakeApplier{}
		u := &fakeUpdater{model: testModel(), images: map[string]string{testImage: testImage, testTag: testImage}}
		c := newControllerWithEnv(u, applier, store, func(string) string { return "" })

		_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
		requireCategory(t, err, core.CategoryRollbackSecretUnavailable)
		if applier.calls != 0 {
			t.Fatalf("rollback applied with a missing recorded secret source: %d calls", applier.calls)
		}
	})

	t.Run("recorded file missing", func(t *testing.T) {
		target := newTestTarget(t)
		store := state.New(t.TempDir())
		rec := testRecord(target)
		rec.Target.SecretRefs = []state.SecretReference{{
			Service: "web", Phase: "runtime", Delivery: "file", Source: "file", Name: "missing.secret",
		}}
		saveRecord(t, store, target, rec)
		applier := &fakeApplier{}
		u := &fakeUpdater{model: testModel(), images: map[string]string{testImage: testImage, testTag: testImage}}
		c := newController(u, applier, store)

		_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
		requireCategory(t, err, core.CategoryRollbackSecretUnavailable)
		if applier.calls != 0 {
			t.Fatalf("rollback applied with a missing recorded file secret: %d calls", applier.calls)
		}
	})

	t.Run("recorded aws unavailable", func(t *testing.T) {
		target := newTestTarget(t)
		store := state.New(t.TempDir())
		rec := testRecord(target)
		rec.Target.SecretRefs = []state.SecretReference{{
			Service: "web", Phase: "runtime", Delivery: "environment", Source: "aws", Name: "prod/app", Field: "token",
		}}
		saveRecord(t, store, target, rec)
		applier := &fakeApplier{}
		u := &fakeUpdater{model: testModel(), images: map[string]string{testImage: testImage, testTag: testImage}}
		c := newController(u, applier, store)
		c.SetAWSSecretChecker(&fakeAWSChecker{err: core.Fail(core.Diagnostic{
			Category: core.CategorySecretAWSAccessDenied,
			Severity: core.SeverityError,
			Message:  "AWS Secrets Manager 원본에 접근할 권한이 없습니다.",
		})})

		_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
		requireCategory(t, err, core.CategoryRollbackSecretUnavailable)
		if strings.Contains(err.Error(), envSecret) {
			t.Fatal("error leaked a secret value")
		}
		if applier.calls != 0 {
			t.Fatalf("rollback applied with an unavailable recorded AWS secret: %d calls", applier.calls)
		}
	})

	t.Run("recorded aws available", func(t *testing.T) {
		target := newTestTarget(t)
		store := state.New(t.TempDir())
		rec := testRecord(target)
		rec.Target.SecretRefs = []state.SecretReference{{
			Service: "web", Phase: "runtime", Delivery: "environment", Source: "aws",
			Name: "prod/app", Field: "token", Region: "ap-northeast-2", Profile: "ops",
		}}
		saveRecord(t, store, target, rec)
		applier := &fakeApplier{}
		u := &fakeUpdater{
			model:      testModel(),
			images:     map[string]string{testImage: testImage, testTag: testImage},
			containers: []stack.Container{healthyContainer("web", testImage, true, "healthy")},
		}
		checker := &fakeAWSChecker{}
		c := newController(u, applier, store)
		c.SetAWSSecretChecker(checker)

		if _, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: 30 * time.Second}); err != nil {
			t.Fatalf("run: %v", err)
		}
		if applier.calls != 1 {
			t.Fatalf("apply calls = %d, want 1", applier.calls)
		}
		if len(checker.refs) != 1 {
			t.Fatalf("AWS checks = %+v, want one", checker.refs)
		}
		got := checker.refs[0]
		if got.Ref != "prod/app" || got.Field != "token" || got.Region != "ap-northeast-2" || got.Profile != "ops" || got.Source != secretref.SourceAWS {
			t.Fatalf("AWS check reference = %+v", got)
		}
	})

	t.Run("recorded aws without a checker refuses", func(t *testing.T) {
		target := newTestTarget(t)
		store := state.New(t.TempDir())
		rec := testRecord(target)
		rec.Target.SecretRefs = []state.SecretReference{{
			Service: "web", Phase: "runtime", Delivery: "environment", Source: "aws", Name: "prod/app",
		}}
		saveRecord(t, store, target, rec)
		applier := &fakeApplier{}
		u := &fakeUpdater{model: testModel(), images: map[string]string{testImage: testImage, testTag: testImage}}
		c := newController(u, applier, store)
		_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
		requireCategory(t, err, core.CategoryRollbackSecretUnavailable)
		if applier.calls != 0 {
			t.Fatalf("rollback applied without an AWS checker: %d calls", applier.calls)
		}
	})

	t.Run("current env source missing", func(t *testing.T) {
		target := newTestTarget(t)
		if err := os.WriteFile(filepath.Join(target.ProjectDir, ".dcraft.yaml"), []byte(`secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: APP_TOKEN
    target: APP_TOKEN
`), 0o644); err != nil {
			t.Fatal(err)
		}
		store := state.New(t.TempDir())
		saveRecord(t, store, target, testRecord(target))
		applier := &fakeApplier{}
		u := &fakeUpdater{model: testModel(), images: map[string]string{testImage: testImage, testTag: testImage}}
		c := newController(u, applier, store)

		_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
		requireCategory(t, err, core.CategoryRollbackSecretUnavailable)
		if applier.calls != 0 {
			t.Fatalf("rollback applied with a missing current secret source: %d calls", applier.calls)
		}
	})
}

// An invalid record is refused and never partly applied.
func TestRunInvalidRecordAbortsBeforeApply(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*state.GoodVersion)
	}{
		{"movable image ref", func(rec *state.GoodVersion) { rec.Services[0].ImageID = "registry/web:1" }},
		{"missing image id", func(rec *state.GoodVersion) { rec.Services[0].ImageID = "" }},
		{"bad retention tag", func(rec *state.GoodVersion) { rec.Services[0].RetentionTag = "web:latest" }},
		{"no services", func(rec *state.GoodVersion) { rec.Services = nil }},
		{"no files", func(rec *state.GoodVersion) { rec.Target.Files = nil }},
		{"no project name", func(rec *state.GoodVersion) { rec.Target.ProjectName = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := newTestTarget(t)
			store := state.New(t.TempDir())
			rec := testRecord(target)
			tc.mutate(&rec)
			saveRecord(t, store, target, rec)
			applier := &fakeApplier{}
			u := &fakeUpdater{model: testModel(), images: map[string]string{testImage: testImage, testTag: testImage}}
			c := newController(u, applier, store)

			_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
			requireCategory(t, err, core.CategoryRollbackRecordInvalid)
			if applier.calls != 0 {
				t.Fatalf("rollback applied an invalid record: %d calls", applier.calls)
			}
		})
	}
}

// An explicit project name that conflicts with the record is a missing-input
// error (exit 2) and changes nothing.
func TestRunExplicitProjectNameConflictFails(t *testing.T) {
	target := newTestTarget(t)
	store := state.New(t.TempDir())
	saveRecord(t, store, target, testRecord(target))
	applier := &fakeApplier{}
	u := &fakeUpdater{model: testModel(), images: map[string]string{testImage: testImage, testTag: testImage}}
	c := newController(u, applier, store)

	in := invocationFor(target)
	in.Target.ProjectName = "other-project"
	_, err := c.Run(context.Background(), in, Options{Timeout: time.Second})
	requireCategory(t, err, core.CategoryInvalidOption)
	if applier.calls != 0 {
		t.Fatalf("rollback applied with a conflicting explicit project name: %d calls", applier.calls)
	}
}

// The same per-stack lock as update is taken; a concurrent operation is
// reported as stack_busy and nothing is applied.
func TestRunStackBusyFailsBeforeApply(t *testing.T) {
	target := newTestTarget(t)
	store := state.New(t.TempDir())
	saveRecord(t, store, target, testRecord(target))
	lock, err := store.AcquireStackLock(context.Background(), target, state.LockHolder{Action: "update"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()

	applier := &fakeApplier{}
	u := &fakeUpdater{model: testModel(), images: map[string]string{testImage: testImage, testTag: testImage}}
	c := newController(u, applier, store)
	_, err = c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
	requireCategory(t, err, core.CategoryStackBusy)
	if applier.calls != 0 {
		t.Fatalf("rollback applied while the stack was busy: %d calls", applier.calls)
	}
}

// The recorded execution configuration wins over the current discovery and a
// recorded service missing from that configuration aborts.
func TestRunUsesRecordedExecutionConfig(t *testing.T) {
	target := newTestTarget(t)
	store := state.New(t.TempDir())
	rec := testRecord(target)
	extraFile := filepath.Join(target.ProjectDir, "compose.extra.yaml")
	if err := os.WriteFile(extraFile, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec.Target.Files = []string{target.Files[0], extraFile}
	rec.Target.EnvFiles = []string{filepath.Join(target.ProjectDir, ".env.recorded")}
	if err := os.WriteFile(rec.Target.EnvFiles[0], []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec.Target.Profiles = []string{"prod"}
	saveRecord(t, store, target, rec)

	applier := &fakeApplier{}
	u := &fakeUpdater{
		model:      testModel(),
		images:     map[string]string{testImage: testImage, testTag: testImage},
		containers: []stack.Container{healthyContainer("web", testImage, true, "healthy")},
	}
	c := newController(u, applier, store)
	in := invocationFor(target)
	in.Target.ProjectName = "project" // matches the record
	payload, err := c.Run(context.Background(), in, Options{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	applied := applier.targets[0]
	if strings.Join(applied.Files, ",") != strings.Join(rec.Target.Files, ",") {
		t.Fatalf("files = %v, want %v", applied.Files, rec.Target.Files)
	}
	if strings.Join(applied.EnvFiles, ",") != strings.Join(rec.Target.EnvFiles, ",") {
		t.Fatalf("env files = %v", applied.EnvFiles)
	}
	if strings.Join(applied.Profiles, ",") != "prod" {
		t.Fatalf("profiles = %v", applied.Profiles)
	}
	if payload.Rollback.ProjectName != "project" {
		t.Fatalf("project name = %q", payload.Rollback.ProjectName)
	}

	// A recorded service that is no longer in the configuration is refused.
	u.model = stack.UpdateModel{ProjectName: "project", Services: []stack.ServiceDefinition{{Name: "other"}}}
	applier.calls = 0
	_, err = c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
	requireCategory(t, err, core.CategoryRollbackRecordInvalid)
	if applier.calls != 0 {
		t.Fatal("rollback applied a service missing from the recorded configuration")
	}
}

// Services that are not in the record stay untouched and are reported.
func TestRunLeavesExtraServicesAlone(t *testing.T) {
	target := newTestTarget(t)
	store := state.New(t.TempDir())
	saveRecord(t, store, target, testRecord(target))
	model := testModel()
	model.Services = append(model.Services, stack.ServiceDefinition{Name: "extra"})
	applier := &fakeApplier{}
	u := &fakeUpdater{
		model:      model,
		images:     map[string]string{testImage: testImage, testTag: testImage},
		containers: []stack.Container{healthyContainer("web", testImage, true, "healthy")},
	}
	c := newController(u, applier, store)
	payload, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := applier.services[0]; len(got) != 1 || got[0] != "web" {
		t.Fatalf("applied services = %v, want only the recorded service", got)
	}
	if !strings.Contains(strings.Join(payload.Rollback.Notices, "\n"), "extra") {
		t.Fatalf("notices do not mention the untouched service: %v", payload.Rollback.Notices)
	}
}

// The read-only preview states the recorded target, images and honesty limits
// and changes nothing.
func TestPreviewDescribesRecordedRollback(t *testing.T) {
	target := newTestTarget(t)
	store := state.New(t.TempDir())
	saveRecord(t, store, target, testRecord(target))
	applier := &fakeApplier{}
	u := &fakeUpdater{model: testModel(), images: map[string]string{testImage: testImage, testTag: testImage}}
	c := newController(u, applier, store)

	p, err := c.Preview(context.Background(), invocationFor(target))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, want := range []string{
		"이전 정상 버전 복원 계획", testImage, testTag, "previous_version",
		"되돌리지 않는 것", "DB·볼륨 데이터", "훅은 다시 실행하지 않습니다", "짧은 중단",
		"더 오래된 버전은 없습니다",
	} {
		if !strings.Contains(p.Text, want) {
			t.Fatalf("plan misses %q:\n%s", want, p.Text)
		}
	}
	if applier.calls != 0 {
		t.Fatalf("preview applied changes: %d calls", applier.calls)
	}
}

// Records are separated per environment: a rollback in another environment
// does not see this environment's record.
func TestRunKeepsRecordsSeparatedByEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dev := core.Target{ProjectDir: dir, Files: []string{filepath.Join(dir, "compose.yaml")}, ProjectName: "project", Environment: "dev"}
	prod := dev
	prod.Environment = "prod"
	store := state.New(t.TempDir())
	rec := testRecord(dev)
	rec.Target.Environment = "dev"
	saveRecord(t, store, dev, rec)

	applier := &fakeApplier{}
	u := &fakeUpdater{model: testModel(), images: map[string]string{testImage: testImage, testTag: testImage}}
	c := newController(u, applier, store)

	_, err := c.Run(context.Background(), invocationFor(prod), Options{Timeout: time.Second})
	requireCategory(t, err, core.CategoryRollbackUnavailable)
	if applier.calls != 0 {
		t.Fatal("rollback in another environment reached the stack")
	}

	u.containers = []stack.Container{healthyContainer("web", testImage, true, "healthy")}
	if _, err := c.Run(context.Background(), invocationFor(dev), Options{Timeout: 30 * time.Second}); err != nil {
		t.Fatalf("rollback in the recorded environment: %v", err)
	}
	if applier.calls != 1 {
		t.Fatalf("apply calls = %d, want 1", applier.calls)
	}
}

// Recorded build-phase references are availability-checked like runtime ones;
// rollback never rebuilds, but a missing source still aborts before a change.
func TestRunChecksBuildPhaseSecretSources(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"build source missing", "", true},
		{"build source present", "build-secret", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := newTestTarget(t)
			store := state.New(t.TempDir())
			rec := testRecord(target)
			rec.Target.SecretRefs = []state.SecretReference{{
				Service: "web", Phase: "build", Delivery: "environment", Source: "env", Name: "BUILD_TOKEN",
			}}
			saveRecord(t, store, target, rec)
			applier := &fakeApplier{}
			u := &fakeUpdater{
				model:      testModel(),
				images:     map[string]string{testImage: testImage, testTag: testImage},
				containers: []stack.Container{healthyContainer("web", testImage, true, "healthy")},
			}
			c := newControllerWithEnv(u, applier, store, func(name string) string {
				if name == "BUILD_TOKEN" {
					return tc.value
				}
				return ""
			})
			_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: 30 * time.Second})
			if tc.wantErr {
				requireCategory(t, err, core.CategoryRollbackSecretUnavailable)
				if applier.calls != 0 {
					t.Fatalf("rollback applied with a missing build secret source: %d calls", applier.calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if applier.calls != 1 {
				t.Fatalf("apply calls = %d, want 1", applier.calls)
			}
		})
	}
}

// A recorded reference with an unknown phase is refused rather than ignored.
func TestRunRejectsUnknownSecretPhase(t *testing.T) {
	target := newTestTarget(t)
	store := state.New(t.TempDir())
	rec := testRecord(target)
	rec.Target.SecretRefs = []state.SecretReference{{
		Service: "web", Phase: "deploy", Delivery: "environment", Source: "env", Name: "TOKEN",
	}}
	saveRecord(t, store, target, rec)
	applier := &fakeApplier{}
	u := &fakeUpdater{model: testModel(), images: map[string]string{testImage: testImage, testTag: testImage}}
	c := newControllerWithEnv(u, applier, store, func(string) string { return "value" })
	_, err := c.Run(context.Background(), invocationFor(target), Options{Timeout: time.Second})
	requireCategory(t, err, core.CategoryRollbackSecretUnavailable)
	if applier.calls != 0 {
		t.Fatal("rollback applied with an unknown secret phase")
	}
}
