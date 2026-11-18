package update

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

// ---- fakes -------------------------------------------------------------

type fakeUpdater struct {
	model        stack.UpdateModel
	containersFn func() ([]stack.Container, error)
	imageIDs     map[string]string
	tagErr       error
	applyErr     error
	buildErr     error
	pullErr      error
	applyHook    func()

	builds       [][]string
	buildOpts    []stack.BuildOptions
	buildNotices []string
	pulls        [][]string
	applies      [][]string
	applyOpts    []stack.ApplyOptions
	tags         []string
	pruned       bool
	containers   []stack.Container
}

func (f *fakeUpdater) UpdateModel(context.Context, core.Target) (stack.UpdateModel, error) {
	return f.model, nil
}

func (f *fakeUpdater) Containers(context.Context, core.Target) ([]stack.Container, error) {
	if f.containersFn != nil {
		return f.containersFn()
	}
	return f.containers, nil
}

func (f *fakeUpdater) Build(_ context.Context, _ core.Target, services []string, opts stack.BuildOptions) (stack.OpResult, error) {
	f.builds = append(f.builds, append([]string{}, services...))
	f.buildOpts = append(f.buildOpts, opts)
	return stack.OpResult{Output: "built " + strings.Join(services, " "), Notices: append([]string{}, f.buildNotices...)}, f.buildErr
}

func (f *fakeUpdater) Pull(_ context.Context, _ core.Target, services []string) (stack.OpResult, error) {
	f.pulls = append(f.pulls, append([]string{}, services...))
	return stack.OpResult{Output: "pulled " + strings.Join(services, " ")}, f.pullErr
}

func (f *fakeUpdater) Apply(_ context.Context, _ core.Target, services []string, opts stack.ApplyOptions) (stack.OpResult, error) {
	f.applies = append(f.applies, append([]string{}, services...))
	f.applyOpts = append(f.applyOpts, opts)
	if f.applyHook != nil {
		f.applyHook()
	}
	return stack.OpResult{Output: "applied"}, f.applyErr
}

func (f *fakeUpdater) ImageID(_ context.Context, ref string) (string, error) {
	if id, ok := f.imageIDs[ref]; ok {
		return id, nil
	}
	return "", &stack.ImageMissingError{Ref: ref}
}

func (f *fakeUpdater) TagImage(_ context.Context, imageID, tag string) error {
	if f.tagErr != nil {
		return f.tagErr
	}
	if f.imageIDs == nil {
		f.imageIDs = map[string]string{}
	}
	f.imageIDs[tag] = imageID
	f.tags = append(f.tags, tag)
	return nil
}

type fakeIdentity struct {
	cfg      stack.ConfigInfo
	derived  string
	projects []stack.ProjectInfo
}

func (f *fakeIdentity) ConfigInfo(context.Context, core.Target) (stack.ConfigInfo, error) {
	return f.cfg, nil
}
func (f *fakeIdentity) DerivedProjectName(context.Context, core.Target) (string, error) {
	return f.derived, nil
}
func (f *fakeIdentity) Projects(context.Context) ([]stack.ProjectInfo, error) {
	return f.projects, nil
}

type stageLog struct {
	begun []string
	ended []string
}

func (r *stageLog) Begin(name, _ string) { r.begun = append(r.begun, name) }
func (r *stageLog) End(name string, status core.StageStatus, _ string) {
	r.ended = append(r.ended, name+":"+string(status))
}

// ---- helpers -----------------------------------------------------------

func healthyContainer(service, imageID string, hasHealth bool, health string) stack.Container {
	return stack.Container{
		ID: "c-" + service, Name: "app-" + service + "-1", Service: service,
		ImageID: imageID, State: "running", Running: true,
		Health: health, HasHealth: hasHealth,
	}
}

func newTestController(t *testing.T, u stack.Updater, id stack.Identity) (*Controller, *state.Store, *stageLog) {
	t.Helper()
	store := state.New(t.TempDir())
	c := New(u, id, store, core.SystemClock{})
	log := &stageLog{}
	configureTestClock(c)
	return c, store, log
}

func configureTestClock(c *Controller) {
	c.poll = time.Second
	c.settle = 2 * time.Second
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	c.sleep = func(d time.Duration) { now = now.Add(d) }
}

func invocation(t *testing.T, options map[string]string, services ...string) core.Invocation {
	t.Helper()
	return core.Invocation{
		Target: core.Target{
			ProjectDir: "/work/app", Files: []string{"/work/app/compose.yaml"},
			EnvFiles: []string{"/work/app/.env.dev"}, Profiles: []string{"dev"},
			DefaultEnvFile: "/work/app/.env",
		},
		Services: services,
		Options:  options,
		Clock:    core.SystemClock{},
		Reporter: &stageLog{},
	}
}

func oneServiceModel(build bool, healthcheck bool) stack.UpdateModel {
	image := "nginx:1.27"
	if build {
		image = ""
	}
	return stack.UpdateModel{
		ProjectName: "app",
		Services: []stack.ServiceDefinition{{
			Name: "web", Image: image, Build: build, Healthcheck: healthcheck,
		}},
	}
}

func wantCategory(t *testing.T, err error, category core.Category) {
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

func TestRunHealthyImageUpdateRecordsGoodVersion(t *testing.T) {
	u := &fakeUpdater{
		model: oneServiceModel(false, true),
		containers: []stack.Container{
			healthyContainer("web", "sha256:new", true, "healthy"),
		},
		imageIDs: map[string]string{"nginx:1.27": "sha256:new"},
	}
	id := &fakeIdentity{cfg: stack.ConfigInfo{EffectiveName: "app", Services: []string{"web"}}, derived: "app"}
	c, store, _ := newTestController(t, u, id)

	payload, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !payload.Update.Verified {
		t.Fatalf("verified = false: %+v", payload.Update.Readiness)
	}
	if len(u.pulls) != 1 || strings.Join(u.pulls[0], ",") != "web" {
		t.Fatalf("pulls = %v", u.pulls)
	}
	if len(u.applies) != 1 || u.applyOpts[0].ForceRecreate || u.applyOpts[0].PullPolicy != "missing" {
		t.Fatalf("apply = %v %+v", u.applies, u.applyOpts)
	}
	if payload.Update.GoodVersion == nil || len(payload.Update.GoodVersion.Services) != 1 {
		t.Fatalf("good version = %+v", payload.Update.GoodVersion)
	}
	rec := payload.Update.GoodVersion.Services[0]
	if rec.ImageID != "sha256:new" {
		t.Fatalf("recorded image = %q", rec.ImageID)
	}
	if rec.RetentionTag != "dcraft-good/app-web:"+shortID("sha256:new") {
		t.Fatalf("retention tag = %q", rec.RetentionTag)
	}
	saved, ok, err := store.LoadGoodVersion(core.Target{ProjectDir: "/work/app"})
	if err != nil || !ok {
		t.Fatalf("load record: ok=%v err=%v", ok, err)
	}
	if saved.Target.ProjectName != "app" || len(saved.Target.Files) != 1 || len(saved.Target.EnvFiles) != 1 ||
		len(saved.Target.Profiles) != 1 || saved.Target.Environment != "" {
		t.Fatalf("recorded target = %+v", saved.Target)
	}
	if saved.Services[0].ImageID != "sha256:new" || saved.Services[0].RetentionTag == "" {
		t.Fatalf("recorded service = %+v", saved.Services[0])
	}
}

func TestNoHealthcheckIsRunningUnverifiedNotHealthy(t *testing.T) {
	u := &fakeUpdater{
		model:      oneServiceModel(false, false),
		containers: []stack.Container{healthyContainer("web", "sha256:new", false, "")},
		imageIDs:   map[string]string{"nginx:1.27": "sha256:new"},
	}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})

	payload, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if payload.Update.Verified {
		t.Fatal("a service without a healthcheck was reported as verified")
	}
	r := payload.Update.Readiness[0]
	if r.Status != stack.ReadinessRunningUnverified || r.AppVerified || r.HealthcheckConfigured {
		t.Fatalf("readiness = %+v", r)
	}
	if !strings.Contains(renderSuccess(payload, 80), "앱 확인 안 됨") {
		t.Fatalf("human output does not distinguish unverified running:\n%s", renderSuccess(payload, 80))
	}
}

func TestNotCreatedServiceFailsReadiness(t *testing.T) {
	u := &fakeUpdater{model: oneServiceModel(false, false), containers: []stack.Container{}}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	_, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second})
	wantCategory(t, err, core.CategoryReadinessFailed)
}

func TestNotStartedServiceFailsReadiness(t *testing.T) {
	created := stack.Container{ID: "c-web", Service: "web", ImageID: "sha256:new", State: "created"}
	u := &fakeUpdater{model: oneServiceModel(false, false), containers: []stack.Container{created}}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	_, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second})
	wantCategory(t, err, core.CategoryReadinessFailed)
	var ce *core.Error
	_ = errors.As(err, &ce)
	if !strings.Contains(ce.Diagnostics[0].Message, "not_started") {
		t.Fatalf("message = %q", ce.Diagnostics[0].Message)
	}
}

func TestVerifyStageFailsAndPlanRunsBeforeApply(t *testing.T) {
	u := &fakeUpdater{
		model:      oneServiceModel(false, true),
		containers: []stack.Container{healthyContainer("web", "sha256:new", true, "unhealthy")},
		imageIDs:   map[string]string{"nginx:1.27": "sha256:new"},
	}
	c, _, st := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	in := invocation(t, nil)
	in.Reporter = st
	_, _ = c.Run(context.Background(), in, Options{Pull: true, Timeout: 30 * time.Second})
	want := "resolve_project:allocate_name:coordinate:plan:build:pull:apply:verify"
	if got := strings.Join(st.begun, ":"); got != want {
		t.Fatalf("stage order = %q, want %q", got, want)
	}
	if got := strings.Join(st.ended, ","); !strings.Contains(got, "verify:failed") {
		t.Fatalf("verify was not marked failed: %s", got)
	}
}

func TestSuccessfulUpdateReplacesPreviousGoodRecord(t *testing.T) {
	u := &fakeUpdater{
		model:      oneServiceModel(false, false),
		containers: []stack.Container{healthyContainer("web", "sha256:new", false, "")},
		imageIDs:   map[string]string{"nginx:1.27": "sha256:new"},
	}
	c, store, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	target := core.Target{ProjectDir: "/work/app"}
	old := state.GoodVersion{Version: state.GoodVersionVersion, OperationID: "op_old"}
	if err := store.SaveGoodVersion(target, old); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	saved, ok, err := store.LoadGoodVersion(target)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if saved.OperationID == "op_old" || len(saved.Services) != 1 || saved.Services[0].ImageID != "sha256:new" {
		t.Fatalf("successful update did not replace the record: %+v", saved)
	}
}

func TestGoodVersionRecordsSecretReferencesWithoutValues(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".dcraft.yaml"), []byte(`secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: DB_PASSWORD
  - service: web
    phase: runtime
    delivery: file
    source: aws
    ref: prod/db
    field: database.password
    region: ap-northeast-2
    profile: ops-admin
    target: db_password
  - service: web
    phase: build
    delivery: file
    source: file
    ref: secrets/build-token.txt
    target: build_token
`), 0o644); err != nil {
		t.Fatal(err)
	}
	u := &fakeUpdater{
		model:      oneServiceModel(false, false),
		containers: []stack.Container{healthyContainer("web", "sha256:new", false, "")},
		imageIDs:   map[string]string{"nginx:1.27": "sha256:new"},
	}
	c, store, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	in := invocation(t, nil)
	in.Target.ProjectDir = project
	in.Target.Files = []string{filepath.Join(project, "compose.yaml")}
	if _, err := c.Run(context.Background(), in, Options{Pull: true, Timeout: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	saved, ok, err := store.LoadGoodVersion(in.Target)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	byKey := map[string]state.SecretReference{}
	for _, ref := range saved.Target.SecretRefs {
		byKey[ref.Phase+"/"+ref.Delivery+"/"+ref.Name] = ref
	}
	build, ok := byKey["build/file/secrets/build-token.txt"]
	if !ok {
		t.Fatalf("build reference missing from the good-version record: %+v", saved.Target.SecretRefs)
	}
	if build.Service != "web" || build.Source != "file" || build.Name != "secrets/build-token.txt" {
		t.Fatalf("build reference = %+v", build)
	}
	runtimeEnv, ok := byKey["runtime/environment/DB_PASSWORD"]
	if !ok || runtimeEnv.Service != "web" {
		t.Fatalf("runtime environment reference = %+v", runtimeEnv)
	}
	aws, ok := byKey["runtime/file/prod/db"]
	if !ok {
		t.Fatalf("AWS reference missing from the good-version record: %+v", saved.Target.SecretRefs)
	}
	if aws.Source != "aws" || aws.Field != "database.password" || aws.Region != "ap-northeast-2" || aws.Profile != "ops-admin" {
		t.Fatalf("AWS reference = %+v", aws)
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "value") || strings.Contains(string(encoded), "secret-value") {
		t.Fatalf("good-version record contains a value field: %s", encoded)
	}
}

func TestBuildNoticesAreReported(t *testing.T) {
	u := &fakeUpdater{
		model:        oneServiceModel(true, false),
		containers:   []stack.Container{healthyContainer("web", "sha256:new", false, "")},
		imageIDs:     map[string]string{"app-web": "sha256:new"},
		buildNotices: []string{"빌드 시점 비밀값 캐시 안내"},
	}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	payload, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: false, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(payload.Update.Notices, "\n"), "빌드 시점 비밀값 캐시 안내") {
		t.Fatalf("build notices were dropped: %v", payload.Update.Notices)
	}
}

func TestPreviewExplainsBuildSecretCacheLimitation(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".dcraft.yaml"), []byte(`secrets:
  - service: web
    phase: build
    delivery: file
    source: file
    ref: secrets/build-token.txt
`), 0o644); err != nil {
		t.Fatal(err)
	}
	u := &fakeUpdater{
		model:      oneServiceModel(true, false),
		containers: []stack.Container{healthyContainer("web", "sha256:old", false, "")},
	}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	in := invocation(t, nil)
	in.Target.ProjectDir = project
	in.Target.Files = []string{filepath.Join(project, "compose.yaml")}
	plan, err := c.Preview(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"빌드 시점 비밀값", "--no-cache", "저장하거나 비교하지 않습니다"} {
		if !strings.Contains(plan.Text, want) {
			t.Fatalf("plan missing %q:\n%s", want, plan.Text)
		}
	}
}

func TestMultipleServicesGetDistinctRetentionTags(t *testing.T) {
	u := &fakeUpdater{
		model: stack.UpdateModel{ProjectName: "app", Services: []stack.ServiceDefinition{
			{Name: "api", Image: "api:1"},
			{Name: "db", Image: "postgres:16"},
		}},
		containers: []stack.Container{
			healthyContainer("api", "sha256:aaaa", false, ""),
			healthyContainer("db", "sha256:bbbb", false, ""),
		},
		imageIDs: map[string]string{"api:1": "sha256:aaaa", "postgres:16": "sha256:bbbb"},
	}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"api", "db"}}, derived: "app"})
	payload, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.tags) != 2 || u.tags[0] == u.tags[1] {
		t.Fatalf("retention tags = %v", u.tags)
	}
	if payload.Update.GoodVersion == nil || len(payload.Update.GoodVersion.Services) != 2 {
		t.Fatalf("record = %+v", payload.Update.GoodVersion)
	}
}

func TestUnhealthyUpdateFailsAndKeepsPreviousGoodRecord(t *testing.T) {
	u := &fakeUpdater{
		model:      oneServiceModel(false, true),
		containers: []stack.Container{healthyContainer("web", "sha256:new", true, "unhealthy")},
		imageIDs:   map[string]string{"nginx:1.27": "sha256:new"},
	}
	c, store, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	target := core.Target{ProjectDir: "/work/app"}
	old := state.GoodVersion{Version: state.GoodVersionVersion, OperationID: "op_old"}
	if err := store.SaveGoodVersion(target, old); err != nil {
		t.Fatal(err)
	}

	_, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second})
	wantCategory(t, err, core.CategoryReadinessFailed)
	saved, ok, loadErr := store.LoadGoodVersion(target)
	if loadErr != nil || !ok || saved.OperationID != "op_old" {
		t.Fatalf("failed update replaced the escape route: ok=%v err=%v rec=%+v", ok, loadErr, saved)
	}
	if len(u.tags) != 0 {
		t.Fatalf("a failed update created retention tags: %v", u.tags)
	}
}

func TestCrashLoopIsNotSuccess(t *testing.T) {
	crash := stack.Container{
		ID: "c-web", Service: "web", ImageID: "sha256:new", State: "restarting",
		Restarting: true, ExitCode: 1, RestartCount: 3,
	}
	u := &fakeUpdater{model: oneServiceModel(false, false), containers: []stack.Container{crash}}
	c, store, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})

	_, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second})
	wantCategory(t, err, core.CategoryReadinessFailed)
	if _, ok, _ := store.LoadGoodVersion(core.Target{ProjectDir: "/work/app"}); ok {
		t.Fatal("crash-looping update was recorded as a good version")
	}
}

func TestEarlyExitAfterFirstObservationFails(t *testing.T) {
	reads := 0
	u := &fakeUpdater{model: oneServiceModel(false, false)}
	u.containersFn = func() ([]stack.Container, error) {
		reads++
		if reads == 1 {
			return []stack.Container{healthyContainer("web", "sha256:new", false, "")}, nil
		}
		return []stack.Container{{
			ID: "c-web", Service: "web", ImageID: "sha256:new", State: "exited", ExitCode: 0,
		}}, nil
	}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})

	_, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second})
	wantCategory(t, err, core.CategoryReadinessFailed)
	var ce *core.Error
	_ = errors.As(err, &ce)
	if !strings.Contains(ce.Diagnostics[0].Message, "exited") {
		t.Fatalf("message does not report the observed exit: %q", ce.Diagnostics[0].Message)
	}
}

func TestRestartAfterHealthyFails(t *testing.T) {
	reads := 0
	u := &fakeUpdater{model: oneServiceModel(false, false)}
	u.containersFn = func() ([]stack.Container, error) {
		reads++
		c := healthyContainer("web", "sha256:new", false, "")
		if reads > 1 {
			c.RestartCount = 1
		}
		return []stack.Container{c}, nil
	}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	_, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second})
	wantCategory(t, err, core.CategoryReadinessFailed)
}

func TestHealthcheckTimeoutIsReportedAsTimeout(t *testing.T) {
	starting := healthyContainer("web", "sha256:new", true, "starting")
	u := &fakeUpdater{model: oneServiceModel(false, true), containers: []stack.Container{starting}}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})

	_, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 5 * time.Second})
	wantCategory(t, err, core.CategoryReadinessFailed)
	var ce *core.Error
	_ = errors.As(err, &ce)
	if !strings.Contains(ce.Diagnostics[0].Message, "timeout") {
		t.Fatalf("message = %q, want the timeout classification", ce.Diagnostics[0].Message)
	}
	if !strings.Contains(ce.Diagnostics[0].Message, "5초") {
		t.Fatalf("message does not state the bound: %q", ce.Diagnostics[0].Message)
	}
}

func TestBuildServicesUseCacheUnlessExplicitlyChanged(t *testing.T) {
	u := &fakeUpdater{
		model:      oneServiceModel(true, false),
		containers: []stack.Container{healthyContainer("web", "sha256:built", false, "")},
		imageIDs:   map[string]string{},
	}
	id := &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"}
	c, _, _ := newTestController(t, u, id)

	if _, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if len(u.builds) != 1 || len(u.pulls) != 0 {
		t.Fatalf("builds=%v pulls=%v", u.builds, u.pulls)
	}
	if opts := u.buildOpts[0]; opts.NoCache || opts.Pull {
		t.Fatalf("default build options = %+v, want normal cache", opts)
	}

	u2 := &fakeUpdater{
		model:      oneServiceModel(true, false),
		containers: []stack.Container{healthyContainer("web", "sha256:built", false, "")},
	}
	c2, _, _ := newTestController(t, u2, id)
	if _, err := c2.Run(context.Background(), invocation(t, map[string]string{"no-cache": "true", "build-pull": "true"}), Options{
		NoCache: true, BuildPull: true, Pull: true, Timeout: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	if opts := u2.buildOpts[0]; !opts.NoCache || !opts.Pull {
		t.Fatalf("explicit build options = %+v", opts)
	}
}

func TestNoPullSkipsRefreshAndFailsBeforeChangingWhenImageIsMissing(t *testing.T) {
	u := &fakeUpdater{model: oneServiceModel(false, false)}
	u.containers = []stack.Container{healthyContainer("web", "sha256:old", false, "")}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})

	_, err := c.Run(context.Background(), invocation(t, map[string]string{"no-pull": "true"}), Options{
		Pull: false, Timeout: 30 * time.Second,
	})
	wantCategory(t, err, core.CategoryImageMissing)
	if len(u.pulls) != 0 || len(u.applies) != 0 {
		t.Fatalf("no-pull changed the stack before the image check: pulls=%v applies=%v", u.pulls, u.applies)
	}
}

func TestNoPullAppliesWithNeverPolicyWhenImagesExist(t *testing.T) {
	u := &fakeUpdater{
		model:      oneServiceModel(false, false),
		containers: []stack.Container{healthyContainer("web", "sha256:new", false, "")},
		imageIDs:   map[string]string{"nginx:1.27": "sha256:new"},
	}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})

	if _, err := c.Run(context.Background(), invocation(t, map[string]string{"no-pull": "true"}), Options{Pull: false, Timeout: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if len(u.pulls) != 0 {
		t.Fatalf("no-pull still pulled: %v", u.pulls)
	}
	if u.applyOpts[0].PullPolicy != "never" {
		t.Fatalf("apply pull policy = %q, want never", u.applyOpts[0].PullPolicy)
	}
}

func TestForceRecreateIsSeparate(t *testing.T) {
	u := &fakeUpdater{
		model:      oneServiceModel(false, false),
		containers: []stack.Container{healthyContainer("web", "sha256:new", false, "")},
		imageIDs:   map[string]string{"nginx:1.27": "sha256:new"},
	}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})

	if _, err := c.Run(context.Background(), invocation(t, map[string]string{"force-recreate": "true"}), Options{
		ForceRecreate: true, Pull: true, Timeout: 30 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	if !u.applyOpts[0].ForceRecreate {
		t.Fatal("force-recreate was not passed to apply")
	}
}

func TestSelectedServiceOnlyTouchesSelection(t *testing.T) {
	u := &fakeUpdater{
		model: stack.UpdateModel{ProjectName: "app", Services: []stack.ServiceDefinition{
			{Name: "api", Image: "api:local"},
			{Name: "web", Build: true},
		}},
		containers: []stack.Container{
			healthyContainer("api", "sha256:api", false, ""),
			healthyContainer("web", "sha256:web", false, ""),
		},
		imageIDs: map[string]string{"api:local": "sha256:api"},
	}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"api", "web"}}, derived: "app"})

	payload, err := c.Run(context.Background(), invocation(t, nil, "web"), Options{Pull: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.builds) != 1 || strings.Join(u.builds[0], ",") != "web" {
		t.Fatalf("builds = %v", u.builds)
	}
	if len(u.pulls) != 0 {
		t.Fatalf("selected update pulled unrelated services: %v", u.pulls)
	}
	if len(u.applies) != 1 || strings.Join(u.applies[0], ",") != "web" {
		t.Fatalf("applies = %v", u.applies)
	}
	if payload.Update.Scope != "services" || strings.Join(payload.Update.Services, ",") != "web" {
		t.Fatalf("scope = %+v", payload.Update)
	}
	if len(payload.Update.Readiness) != 1 || payload.Update.Readiness[0].Service != "web" {
		t.Fatalf("readiness = %+v", payload.Update.Readiness)
	}
}

func TestUnknownServiceIsUsageError(t *testing.T) {
	u := &fakeUpdater{model: oneServiceModel(false, false)}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	_, err := c.Run(context.Background(), invocation(t, nil, "nope"), Options{Timeout: 30 * time.Second})
	wantCategory(t, err, core.CategoryServiceUnknown)
	if len(u.applies) != 0 {
		t.Fatal("unknown service reached apply")
	}
}

func TestInvalidTimeoutIsUsageError(t *testing.T) {
	for _, raw := range []string{"0", "-1", "1s", "99999"} {
		_, err := ParseOptions(invocation(t, map[string]string{"timeout": raw}))
		wantCategory(t, err, core.CategoryInvalidOption)
	}
	opts, err := ParseOptions(invocation(t, nil))
	if err != nil || opts.Timeout != defaultTimeout {
		t.Fatalf("default timeout = %v err=%v", opts.Timeout, err)
	}
}

func TestPreviewShowsTargetPlanAndHonestLimits(t *testing.T) {
	u := &fakeUpdater{
		model: stack.UpdateModel{ProjectName: "app", Services: []stack.ServiceDefinition{
			{Name: "api", Build: true, Healthcheck: true},
			{Name: "db", Image: "postgres:16"},
		}},
		containers: []stack.Container{},
	}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{EffectiveName: "app", Services: []string{"api", "db"}}, derived: "app"})

	plan, err := c.Preview(context.Background(), invocation(t, map[string]string{"no-cache": "true", "force-recreate": "true"}))
	if err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(c.store.(*state.Store).Root()); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("preview wrote state: %v", entries)
	}
	for _, want := range []string{
		"/work/app/compose.yaml", ".env.dev", "dev", "app", "api", "db",
		"캐시 사용 안 함", "강제 재생성", "정확한 미래 상태 diff가 아닙니다",
	} {
		if !strings.Contains(plan.Text, want) {
			t.Fatalf("preview misses %q:\n%s", want, plan.Text)
		}
	}
	if len(u.builds)+len(u.pulls)+len(u.applies) != 0 {
		t.Fatalf("preview changed the stack: %v %v %v", u.builds, u.pulls, u.applies)
	}
}

func TestLockIsHeldAndReleasedOnSuccessFailureAndCancel(t *testing.T) {
	identity := &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"}
	storePath := t.TempDir()

	runWith := func(t *testing.T, u *fakeUpdater, ctx context.Context, opts Options) {
		t.Helper()
		c := New(u, identity, state.New(storePath), core.SystemClock{})
		c.poll = time.Second
		c.settle = 2 * time.Second
		now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
		c.now = func() time.Time { return now }
		c.sleep = func(d time.Duration) { now = now.Add(d) }
		_, _ = c.Run(ctx, invocation(t, nil), opts)
		// The lock must be free again: taking it directly proves release.
		store := state.New(storePath)
		lock, err := store.AcquireStackLock(context.Background(), core.Target{ProjectDir: "/work/app"}, state.LockHolder{Action: "probe"})
		if err != nil {
			t.Fatalf("lock was not released: %v", err)
		}
		_ = lock.Release()
	}

	runWith(t, &fakeUpdater{
		model: oneServiceModel(false, false), containers: []stack.Container{healthyContainer("web", "sha256:new", false, "")},
		imageIDs: map[string]string{"nginx:1.27": "sha256:new"},
	}, context.Background(), Options{Pull: true, Timeout: 30 * time.Second})
	runWith(t, &fakeUpdater{
		model: oneServiceModel(false, true), containers: []stack.Container{healthyContainer("web", "sha256:new", true, "unhealthy")},
		imageIDs: map[string]string{"nginx:1.27": "sha256:new"},
	}, context.Background(), Options{Pull: true, Timeout: 30 * time.Second})
	runWith(t, &fakeUpdater{
		model: oneServiceModel(false, false), containers: []stack.Container{healthyContainer("web", "sha256:new", false, "")},
	}, context.Background(), Options{Pull: false, Timeout: 30 * time.Second}) // image_missing failure
}

func TestConcurrentOperationOnSameStackFailsBusy(t *testing.T) {
	store := state.New(t.TempDir())
	identity := &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"}
	u := &fakeUpdater{model: oneServiceModel(false, false)}
	c := New(u, identity, store, core.SystemClock{})

	holder, err := store.AcquireStackLock(context.Background(), core.Target{ProjectDir: "/work/app"}, state.LockHolder{Action: "up", PID: 4321})
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := c.Run(context.Background(), invocation(t, nil), Options{Pull: false, Timeout: 30 * time.Second})
	wantCategory(t, runErr, core.CategoryStackBusy)
	if len(u.applies) != 0 {
		t.Fatal("busy stack received an apply")
	}
	_ = holder.Release()

	other := &fakeUpdater{
		model: oneServiceModel(false, false), containers: []stack.Container{healthyContainer("web", "sha256:new", false, "")},
	}
	c2 := New(other, identity, store, core.SystemClock{})
	configureTestClock(c2)
	otherInv := invocation(t, nil)
	otherInv.Target.ProjectDir = "/work/other"
	if _, err := c2.Run(context.Background(), otherInv, Options{Pull: true, Timeout: 30 * time.Second}); err != nil {
		t.Fatalf("different stack was affected by the lock: %v", err)
	}
}

func TestCancellationDuringApplyIsNotSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	u := &fakeUpdater{
		model:      oneServiceModel(false, false),
		containers: []stack.Container{healthyContainer("web", "sha256:new", false, "")},
	}
	c, store, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	old := state.GoodVersion{Version: state.GoodVersionVersion, OperationID: "op_old"}
	if err := store.SaveGoodVersion(core.Target{ProjectDir: "/work/app"}, old); err != nil {
		t.Fatal(err)
	}

	// Cancel while apply is in flight.
	u.applyHook = cancel
	_, err := c.Run(ctx, invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was not reported as canceled: %v", err)
	}
	saved, ok, loadErr := store.LoadGoodVersion(core.Target{ProjectDir: "/work/app"})
	if loadErr != nil || !ok || saved.OperationID != "op_old" {
		t.Fatalf("cancellation replaced the previous good record: ok=%v err=%v %+v", ok, loadErr, saved)
	}
	if len(u.tags) != 0 {
		t.Fatalf("cancellation created retention tags: %v", u.tags)
	}
	probe, lockErr := store.AcquireStackLock(context.Background(), core.Target{ProjectDir: "/work/app"}, state.LockHolder{Action: "probe"})
	if lockErr != nil {
		t.Fatalf("lock was not released after cancellation: %v", lockErr)
	}
	_ = probe.Release()
}

func TestRecordFailureIsAWarningAndKeepsOldRecord(t *testing.T) {
	u := &fakeUpdater{
		model:      oneServiceModel(false, false),
		containers: []stack.Container{healthyContainer("web", "sha256:new", false, "")},
		imageIDs:   map[string]string{"nginx:1.27": "sha256:new"},
		tagErr:     errors.New("disk full"),
	}
	c, store, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	target := core.Target{ProjectDir: "/work/app"}
	old := state.GoodVersion{Version: state.GoodVersionVersion, OperationID: "op_old"}
	if err := store.SaveGoodVersion(target, old); err != nil {
		t.Fatal(err)
	}

	payload, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("a record failure turned a completed update into a failure: %v", err)
	}
	if payload.Update.GoodVersion != nil {
		t.Fatalf("unrecorded good version was reported: %+v", payload.Update.GoodVersion)
	}
	if payload.Update.Notices == nil || len(payload.Update.Notices) == 0 {
		t.Fatal("record failure was silent")
	}
	saved, ok, _ := store.LoadGoodVersion(target)
	if !ok || saved.OperationID != "op_old" {
		t.Fatalf("old record was lost: %+v", saved)
	}
}

func TestRecordNeverDeletesImagesOrTags(t *testing.T) {
	u := &fakeUpdater{
		model:      oneServiceModel(false, false),
		containers: []stack.Container{healthyContainer("web", "sha256:new", false, "")},
		imageIDs:   map[string]string{"nginx:1.27": "sha256:new"},
	}
	c, _, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	if _, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if len(u.tags) != 1 || u.pruned {
		t.Fatalf("tags=%v pruned=%v", u.tags, u.pruned)
	}
}

func TestPayloadHasNoSecretValues(t *testing.T) {
	const secretValue = "super-secret-value-123"
	t.Setenv("APP_TOKEN", secretValue)
	u := &fakeUpdater{
		model:      oneServiceModel(false, false),
		containers: []stack.Container{healthyContainer("web", "sha256:new", false, "")},
		imageIDs:   map[string]string{"nginx:1.27": "sha256:new"},
	}
	c, store, _ := newTestController(t, u, &fakeIdentity{cfg: stack.ConfigInfo{Services: []string{"web"}}, derived: "app"})
	payload, err := c.Run(context.Background(), invocation(t, nil), Options{Pull: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(payload)
	if strings.Contains(string(data), secretValue) {
		t.Fatalf("payload contains a secret value:\n%s", data)
	}
	path, _ := store.GoodVersionPath(core.Target{ProjectDir: "/work/app"})
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), secretValue) {
		t.Fatalf("record contains a secret value:\n%s", raw)
	}
}
