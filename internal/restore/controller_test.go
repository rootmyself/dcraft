package restore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
	"github.com/rootmyself/dcraft/internal/state"
)

type fakeNames struct {
	name string
	err  error
}

func (f fakeNames) ReadProjectName(context.Context, core.Target) (string, error) {
	return f.name, f.err
}

type fakeBackend struct {
	mu              sync.Mutex
	projectName     string
	sources         []data.DataSource
	runtime         []data.ServiceRuntime
	consumers       map[string][]data.ContainerConsumer
	bindConsumers   map[string][]data.ContainerConsumer
	events          []string
	stops           [][]string
	starts          [][]string
	exists          map[string]bool
	usage           data.VolumeUsage
	spaceErr        error
	archiveErr      error
	emptyErr        error
	extractErr      error
	verifyErr       error
	cancelOnExtract bool
	archiveBody     []fixtureEntry
	redact          func(string) string
}

func (f *fakeBackend) record(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

func (f *fakeBackend) ProjectName(context.Context, core.Target) (string, error) {
	return f.projectName, nil
}

func (f *fakeBackend) Sources(context.Context, core.Target) ([]data.DataSource, error) {
	return append([]data.DataSource{}, f.sources...), nil
}

func (f *fakeBackend) ServiceRuntime(context.Context, core.Target) ([]data.ServiceRuntime, error) {
	return append([]data.ServiceRuntime{}, f.runtime...), nil
}

func (f *fakeBackend) VolumeConsumers(_ context.Context, _ core.Target, volume string) ([]data.ContainerConsumer, error) {
	return append([]data.ContainerConsumer{}, f.consumers[volume]...), nil
}

// BindConsumers keeps this fake compatible with the published data.Backend
// when bind-folder backup (#17) extends it; bind restore (#19) queries it.
func (f *fakeBackend) BindConsumers(_ context.Context, _ core.Target, hostPath string) ([]data.ContainerConsumer, error) {
	return append([]data.ContainerConsumer{}, f.bindConsumers[hostPath]...), nil
}

func (f *fakeBackend) StopServices(_ context.Context, _ core.Target, services []string) (data.OpResult, error) {
	f.record("stop:" + strings.Join(services, ","))
	f.stops = append(f.stops, append([]string{}, services...))
	return data.OpResult{}, nil
}

func (f *fakeBackend) StartServices(_ context.Context, _ core.Target, services []string) (data.OpResult, error) {
	f.record("start:" + strings.Join(services, ","))
	f.starts = append(f.starts, append([]string{}, services...))
	return data.OpResult{}, nil
}

func (f *fakeBackend) ArchiveVolume(_ context.Context, _ core.Target, volume, hostDir string) (data.OpResult, error) {
	f.record("archive:" + volume)
	if f.archiveErr != nil {
		return data.OpResult{Output: "tar: can't open '/data/secret': Permission denied"}, f.archiveErr
	}
	raw, err := tarGzBytes(f.archiveBody)
	if err != nil {
		return data.OpResult{}, err
	}
	if err := os.WriteFile(filepath.Join(hostDir, backup.DataFile), raw, 0o600); err != nil {
		return data.OpResult{}, err
	}
	return data.OpResult{}, nil
}

func (f *fakeBackend) HelperImage() string { return "alpine:test" }

func (f *fakeBackend) Redact(_ context.Context, _ core.Target, text string) string {
	if f.redact == nil {
		return text
	}
	return f.redact(text)
}

func (f *fakeBackend) VolumeExists(_ context.Context, volume string) (bool, error) {
	if f.exists == nil {
		return true, nil
	}
	return f.exists[volume], nil
}

func (f *fakeBackend) VolumeUsage(context.Context, string) (data.VolumeUsage, error) {
	return f.usage, nil
}

func (f *fakeBackend) EmptyVolume(context.Context, string) (data.OpResult, error) {
	f.record("empty")
	if f.emptyErr != nil {
		return data.OpResult{Output: "find: read-only file system"}, f.emptyErr
	}
	return data.OpResult{}, nil
}

func (f *fakeBackend) ExtractVolume(ctx context.Context, _, artifactDir string) (data.OpResult, error) {
	f.record("extract:" + artifactDir)
	if f.cancelOnExtract {
		return data.OpResult{}, context.Canceled
	}
	if f.extractErr != nil {
		return data.OpResult{Output: "tar: corrupted data"}, f.extractErr
	}
	return data.OpResult{}, nil
}

func (f *fakeBackend) VerifyVolume(context.Context, string, string) (data.OpResult, error) {
	f.record("verify")
	if f.verifyErr != nil {
		return data.OpResult{Output: "dcraft-restore-verify: sha256: /data/file.txt"}, f.verifyErr
	}
	return data.OpResult{}, nil
}

type fakeSpace struct {
	available int64
	err       error
	checked   []string
}

func (f *fakeSpace) AvailableBytes(path string) (int64, error) {
	f.checked = append(f.checked, path)
	return f.available, f.err
}

type stageReporter struct {
	mu     sync.Mutex
	stages []string
}

func (r *stageReporter) Begin(name, title string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stages = append(r.stages, name)
}
func (r *stageReporter) End(string, core.StageStatus, string) {}

func newRestoreController(t *testing.T, backend *fakeBackend, name string, space SpaceChecker) (*Controller, *stageReporter, string) {
	t.Helper()
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("chmod state dir: %v", err)
	}
	controller := New(Config{
		Backend: backend,
		Names:   fakeNames{name: name},
		Locks:   state.New(stateDir),
		Clock:   fixedClock{now: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)},
		Tool:    backup.Tool{Version: "test"},
		PID:     4242,
		Space:   space,
	})
	return controller, &stageReporter{}, stateDir
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func restoreInvocation(t *testing.T, projectDir, artifact string, extras map[string]string, volumes ...string) core.Invocation {
	t.Helper()
	options := map[string]string{"backup": artifact}
	for k, v := range extras {
		options[k] = v
	}
	in := core.Invocation{
		Target: core.Target{
			ProjectDir: projectDir,
			Files:      []string{filepath.Join(projectDir, "compose.yaml")},
		},
		Options:     options,
		OptionLists: map[string][]string{},
		Reporter:    &stageReporter{},
	}
	if len(volumes) > 0 {
		in.OptionLists["volume"] = volumes
	}
	return in
}

func defaultBackend() *fakeBackend {
	return &fakeBackend{
		projectName: "proj",
		sources: []data.DataSource{{
			Kind: data.KindVolume, Key: "data", Name: "proj_data",
			MountTargets: []string{"/data"}, Services: []string{"app", "worker"},
		}},
		runtime: []data.ServiceRuntime{
			{Service: "app", State: "running"},
			{Service: "worker", State: "exited", ExitCode: 137},
		},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
		exists:      map[string]bool{"proj_data": true},
		usage:       data.VolumeUsage{Bytes: 2048, Entries: 4},
		archiveBody: referenceEntries(),
	}
}

func TestRunReplacesTheVolumeWithASafetyBackupAndRestoresOnlyRunningServices(t *testing.T) {
	artifactDir, _ := writeArtifact(t, referenceEntries(), nil)
	backend := defaultBackend()
	space := &fakeSpace{available: 1 << 30}
	controller, stages, _ := newRestoreController(t, backend, "proj", space)
	projectDir := t.TempDir()
	in := restoreInvocation(t, projectDir, artifactDir, nil)
	in.Reporter = stages

	payload, err := controller.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := payload.(Payload).Restore
	if report.DataState != DataReplaced {
		t.Fatalf("data state = %s", report.DataState)
	}
	if report.VolumeKey != "data" || report.VolumeName != "proj_data" {
		t.Fatalf("target = %+v", report)
	}
	if strings.Join(report.Stopped, ",") != "app" || strings.Join(report.Restarted, ",") != "app" {
		t.Fatalf("services = stopped %v restarted %v", report.Stopped, report.Restarted)
	}
	if strings.Join(backend.stops[0], ",") != "app" || strings.Join(backend.starts[0], ",") != "app" {
		t.Fatalf("backend service calls = %v / %v", backend.stops, backend.starts)
	}
	events := strings.Join(backend.events, ",")
	resolvedArtifact, err := filepath.EvalSymlinks(artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	want := "stop:app,archive:proj_data,empty,extract:" + resolvedArtifact + ",verify,start:app"
	if events != want {
		t.Fatalf("event order = %s, want %s", events, want)
	}
	if report.SafetyBackup == "" {
		t.Fatal("safety backup path missing")
	}
	if _, err := backup.Verify(report.SafetyBackup); err != nil {
		t.Fatalf("safety backup did not verify: %v", err)
	}
	manifest, err := backup.ReadManifest(report.SafetyBackup)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Source.VolumeName != "proj_data" || manifest.Stack.ProjectName != "proj" {
		t.Fatalf("safety manifest = %+v", manifest)
	}
	if report.SafetyManifestPath != filepath.Join(report.SafetyBackup, backup.ManifestFile) {
		t.Fatalf("safety manifest path = %s", report.SafetyManifestPath)
	}
	if !strings.Contains(strings.Join(stages.stages, ","), "validate_artifact") {
		t.Fatalf("stages = %v", stages.stages)
	}
}

func TestRunRefusesCorruptArtifactsBeforeStoppingAnything(t *testing.T) {
	artifactDir, _ := writeArtifact(t, referenceEntries(), nil)
	path := filepath.Join(artifactDir, backup.DataFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw[:len(raw)-8], 0o600); err != nil {
		t.Fatal(err)
	}
	backend := defaultBackend()
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err = controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifactDir, nil))
	if err == nil {
		t.Fatal("corrupt artifact was accepted")
	}
	if !hasCategory(err, core.CategoryRestoreIntegrityFailed) {
		t.Fatalf("error = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("corrupt artifact touched the backend: %v", backend.events)
	}
}

func TestRunRefusesWrongStackAndWrongVolumeWithoutStopping(t *testing.T) {
	artifactDir, _ := writeArtifact(t, referenceEntries(), nil)
	for _, tc := range []struct {
		name    string
		backend *fakeBackend
		extras  map[string]string
		want    core.Category
	}{
		{
			name: "wrong project",
			backend: func() *fakeBackend {
				b := defaultBackend()
				b.projectName = "other"
				return b
			}(),
			want: core.CategoryRestoreTargetMismatch,
		},
		{
			name:    "wrong volume",
			backend: defaultBackend(),
			extras:  map[string]string{"volume": "cache"},
			want:    core.CategoryRestoreTargetMismatch,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.extras != nil {
				tc.backend.sources = append(tc.backend.sources, data.DataSource{
					Kind: data.KindVolume, Key: "cache", Name: "proj_cache", Services: []string{"app"},
				})
				tc.backend.exists["proj_cache"] = true
			}
			controller, _, _ := newRestoreController(t, tc.backend, tc.backend.projectName, &fakeSpace{available: 1 << 30})
			_, err := controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifactDir, tc.extras))
			if err == nil || !hasCategory(err, tc.want) {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
			if len(tc.backend.events) != 0 {
				t.Fatalf("wrong mapping touched the backend: %v", tc.backend.events)
			}
		})
	}
}

func TestRunRefusesABindArtifactWithoutADeclaredTarget(t *testing.T) {
	artifactDir, _ := writeArtifact(t, referenceEntries(), func(src *backup.Source) {
		src.Kind = data.KindBind
		src.HostPath = "/host/folder"
		src.VolumeKey = ""
		src.VolumeName = ""
	})
	backend := defaultBackend()
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifactDir, nil))
	if err == nil || !hasCategory(err, core.CategoryRestoreTargetMismatch) {
		t.Fatalf("bind artifact without a declared target err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("bind artifact touched the backend: %v", backend.events)
	}
}

func TestRunUnknownVolumeIsAUsageErrorAndSpaceShortageStopsNothing(t *testing.T) {
	artifactDir, _ := writeArtifact(t, referenceEntries(), nil)
	backend := defaultBackend()
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifactDir, map[string]string{"volume": "nope"}))
	if err == nil || !hasCategory(err, core.CategoryRestoreVolumeUnknown) {
		t.Fatalf("unknown volume err = %v", err)
	}

	backend = defaultBackend()
	controller, _, _ = newRestoreController(t, backend, "proj", &fakeSpace{available: 128})
	_, err = controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifactDir, nil))
	if err == nil || !hasCategory(err, core.CategoryRestoreSafetyFailed) {
		t.Fatalf("space err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("short space touched the backend: %v", backend.events)
	}
}

func TestRunSafetyBackupFailureAbortsBeforeReplacementAndRestartsServices(t *testing.T) {
	artifactDir, _ := writeArtifact(t, referenceEntries(), nil)
	backend := defaultBackend()
	backend.archiveErr = errors.New("helper failed")
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifactDir, nil))
	if err == nil || !hasCategory(err, core.CategoryRestoreSafetyFailed) {
		t.Fatalf("err = %v", err)
	}
	events := strings.Join(backend.events, ",")
	if strings.Contains(events, "empty") || strings.Contains(events, "extract") {
		t.Fatalf("safety failure replaced data: %s", events)
	}
	if !strings.Contains(events, "start:app") {
		t.Fatalf("services were not restored: %s", events)
	}
	rep := controller.LastReport()
	if rep.DataState != DataUntouched {
		t.Fatalf("data state = %s", rep.DataState)
	}
	if rep.SafetyBackup == "" {
		t.Fatal("safety attempt location missing")
	}
}

func TestRunVerificationFailureDoesNotRestartAndReportsHonestState(t *testing.T) {
	artifactDir, _ := writeArtifact(t, referenceEntries(), nil)
	backend := defaultBackend()
	backend.verifyErr = errors.New("mismatch SECRET_VALUE")
	backend.redact = func(s string) string { return strings.ReplaceAll(s, "SECRET_VALUE", "[redacted]") }
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifactDir, nil))
	if err == nil || !hasCategory(err, core.CategoryRestoreVerifyFailed) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(errorDetail(err), "SECRET_VALUE") {
		t.Fatalf("secret value leaked into the failure detail: %s", errorDetail(err))
	}
	if strings.Contains(strings.Join(backend.events, ","), "start:") {
		t.Fatalf("verification failure restarted services: %v", backend.events)
	}
	rep := controller.LastReport()
	if rep.DataState != DataUncertain || rep.Stage != "verify" {
		t.Fatalf("report = %+v", rep)
	}
	if !strings.Contains(errorDetail(err), "verify") || !strings.Contains(errorDetail(err), rep.SafetyBackup) {
		t.Fatalf("failure detail missing stage/safety: %s", errorDetail(err))
	}
}

func TestRunCancellationDuringReplaceReportsTheStageAndSafetyPathWithoutRestart(t *testing.T) {
	artifactDir, _ := writeArtifact(t, referenceEntries(), nil)
	backend := defaultBackend()
	backend.cancelOnExtract = true
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifactDir, nil))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	if strings.Contains(strings.Join(backend.events, ","), "start:") {
		t.Fatalf("canceled replace restarted services: %v", backend.events)
	}
	rep := controller.LastReport()
	if rep.DataState != DataUncertain || rep.Stage != "replace" {
		t.Fatalf("report = %+v", rep)
	}
	diag := controller.CanceledDiagnostic()
	for _, want := range []string{"replace", string(DataUncertain), rep.SafetyBackup} {
		if !strings.Contains(diag.Message, want) {
			t.Fatalf("canceled diagnostic missing %q: %s", want, diag.Message)
		}
	}
}

func TestRunRefusesAnotherStacksConsumer(t *testing.T) {
	artifactDir, _ := writeArtifact(t, referenceEntries(), nil)
	backend := defaultBackend()
	backend.consumers["proj_data"] = append(backend.consumers["proj_data"], data.ContainerConsumer{
		Name: "other-app-1", Project: "other", Service: "app", State: "running",
	})
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifactDir, nil))
	if err == nil || !hasCategory(err, core.CategoryRestoreConsumerUnsafe) {
		t.Fatalf("err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("other-stack consumer refusal touched data: %v", backend.events)
	}
}

func TestPlanIsReadOnlyAndShowsTheTargetSafetyAndOverwriteImpact(t *testing.T) {
	artifactDir, _ := writeArtifact(t, referenceEntries(), nil)
	backend := defaultBackend()
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	in := restoreInvocation(t, t.TempDir(), artifactDir, nil)
	plan, err := controller.Plan(context.Background(), in)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, want := range []string{"복원 계획", "data → proj_data", "app", "안전 백업 위치", "병합이 아닙니다", "잠시 중지"} {
		if !strings.Contains(plan.Text, want) {
			t.Fatalf("plan missing %q:\n%s", want, plan.Text)
		}
	}
	if len(backend.events) != 0 {
		t.Fatalf("plan changed the backend: %v", backend.events)
	}
}

func TestRunMissingTargetAndExplicitMapping(t *testing.T) {
	artifactDir, _ := writeArtifact(t, referenceEntries(), nil)
	backend := defaultBackend()
	backend.exists["proj_data"] = false
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifactDir, nil))
	if err == nil || !hasCategory(err, core.CategoryRestoreTargetInvalid) {
		t.Fatalf("missing target err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("missing target touched data: %v", backend.events)
	}

	// Explicit mapping is allowed but must be reported as a warning.
	backend = defaultBackend()
	backend.projectName = "renamed"
	backend.sources[0].Name = "renamed_data"
	backend.consumers["renamed_data"] = []data.ContainerConsumer{
		{Name: "renamed-app-1", Project: "renamed", Service: "app", State: "running"},
	}
	backend.exists["renamed_data"] = true
	controller, _, _ = newRestoreController(t, backend, "renamed", &fakeSpace{available: 1 << 30})
	payload, err := controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifactDir,
		map[string]string{"allow-source-mismatch": "true"}))
	if err != nil {
		t.Fatalf("explicit mapping: %v", err)
	}
	report := payload.(Payload).Restore
	if !report.ExplicitMapping || len(report.Warnings) == 0 {
		t.Fatalf("explicit mapping was not reported: %+v", report)
	}
}

func hasCategory(err error, category core.Category) bool {
	var ce *core.Error
	if !errors.As(err, &ce) {
		return false
	}
	for _, d := range ce.AllDiagnostics() {
		if d.Category == category {
			return true
		}
	}
	return false
}

func errorDetail(err error) string {
	var ce *core.Error
	if !errors.As(err, &ce) {
		return err.Error()
	}
	var b strings.Builder
	for _, d := range ce.AllDiagnostics() {
		b.WriteString(d.Detail)
		b.WriteString("\n")
	}
	return b.String()
}
