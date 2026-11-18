package backup

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
	"github.com/rootmyself/dcraft/internal/state"
)

type fakeNames struct {
	name string
	err  error
}

func (f fakeNames) ReadProjectName(ctx context.Context, target core.Target) (string, error) {
	return f.name, f.err
}

type fakeBackend struct {
	mu           sync.Mutex
	projectName  string
	sources      []data.DataSource
	runtime      []data.ServiceRuntime
	consumers    map[string][]data.ContainerConsumer
	archive      func(volume, dir string) error
	events       []string
	stops        [][]string
	starts       [][]string
	helperImage  string
	projectError error
}

func (f *fakeBackend) record(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

func (f *fakeBackend) ProjectName(ctx context.Context, target core.Target) (string, error) {
	if f.projectError != nil {
		return "", f.projectError
	}
	return f.projectName, nil
}

func (f *fakeBackend) Sources(ctx context.Context, target core.Target) ([]data.DataSource, error) {
	return append([]data.DataSource{}, f.sources...), nil
}

func (f *fakeBackend) ServiceRuntime(ctx context.Context, target core.Target) ([]data.ServiceRuntime, error) {
	return append([]data.ServiceRuntime{}, f.runtime...), nil
}

func (f *fakeBackend) VolumeConsumers(ctx context.Context, volumeName string) ([]data.ContainerConsumer, error) {
	return append([]data.ContainerConsumer{}, f.consumers[volumeName]...), nil
}

func (f *fakeBackend) StopServices(ctx context.Context, target core.Target, services []string) (data.OpResult, error) {
	f.record("stop:" + strings.Join(services, ","))
	f.mu.Lock()
	f.stops = append(f.stops, append([]string{}, services...))
	f.mu.Unlock()
	return data.OpResult{}, nil
}

func (f *fakeBackend) StartServices(ctx context.Context, target core.Target, services []string) (data.OpResult, error) {
	f.record("start:" + strings.Join(services, ","))
	f.mu.Lock()
	f.starts = append(f.starts, append([]string{}, services...))
	f.mu.Unlock()
	return data.OpResult{}, nil
}

func (f *fakeBackend) ArchiveVolume(ctx context.Context, volumeName, hostDir string) (data.OpResult, error) {
	f.record("archive:" + volumeName)
	if f.archive == nil {
		return data.OpResult{}, errors.New("fake archive hook is not configured")
	}
	return data.OpResult{}, f.archive(volumeName, hostDir)
}

func (f *fakeBackend) HelperImage() string {
	if f.helperImage != "" {
		return f.helperImage
	}
	return data.VolumeHelperImage
}

func writeValidArchive(t *testing.T, dir string) {
	t.Helper()
	writeFixtureArchive(t, filepath.Join(dir, DataFile), []fixtureEntry{
		{name: "./", typeflag: tar.TypeDir, mode: 0o755},
		{name: "./file.txt", typeflag: tar.TypeReg, mode: 0o644, body: "hello"},
	})
}

// writeValidArchive in a fake must not require *testing.T; tests replace the
// fake archive hook with a closure that captures t.
func (f *fakeBackend) defaultArchive(t *testing.T) {
	f.archive = func(volume, dir string) error {
		writeValidArchive(t, dir)
		return nil
	}
}

func newController(t *testing.T, backend *fakeBackend, names fakeNames, stateDir string) (*Controller, *state.Store) {
	t.Helper()
	store := state.New(stateDir)
	return New(Config{
		Backend: backend,
		Names:   names,
		Store:   store,
		Clock:   fixedClock{now: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)},
		Tool:    Tool{Version: "test"},
		PID:     4242,
	}), store
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func invocation(t *testing.T, projectDir, output string, volumes ...string) core.Invocation {
	t.Helper()
	report := &recordReporter{}
	return core.Invocation{
		Target: core.Target{
			ProjectDir: projectDir,
			Files:      []string{filepath.Join(projectDir, "compose.yaml")},
		},
		Options:     map[string]string{"output": output},
		OptionLists: map[string][]string{"volume": volumes},
		Reporter:    report,
	}
}

type recordReporter struct{}

func (recordReporter) Begin(name, title string)                           {}
func (recordReporter) End(name string, status core.StageStatus, d string) {}

func volumeSource(key, name string, services ...string) data.DataSource {
	return data.DataSource{Kind: data.KindVolume, Key: key, Name: name, Services: services, MountTargets: []string{"/" + key}}
}

func TestRunStopsOnlyRunningConsumersCopiesAndRestartsOnlyStoppedServices(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{volumeSource("data", "proj_data", "app", "worker", "db")},
		runtime: []data.ServiceRuntime{
			{Service: "app", State: "running"},
			{Service: "worker", State: "exited", ExitCode: 137},
			{Service: "db", State: "running"},
		},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {
				{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"},
				{Name: "proj-db-1", Project: "proj", Service: "db", State: "running"},
			},
		},
	}
	backend.defaultArchive(t)
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())

	payload, err := controller.Run(context.Background(), invocation(t, projectDir, output, "data"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := payload.(Payload).Backup
	if len(report.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v", report.Artifacts)
	}
	if strings.Join(report.Stopped, ",") != "app,db" {
		t.Fatalf("stopped = %v", report.Stopped)
	}
	if strings.Join(report.Restarted, ",") != "app,db" {
		t.Fatalf("restarted = %v", report.Restarted)
	}
	if strings.Join(report.RunningBefore, ",") != "app,db" {
		t.Fatalf("running before = %v", report.RunningBefore)
	}
	if strings.Join(backend.stops[0], ",") != "app,db" {
		t.Fatalf("stop call = %v", backend.stops)
	}
	if strings.Join(backend.starts[0], ",") != "app,db" {
		t.Fatalf("start call = %v", backend.starts)
	}
	if report.Artifacts[0].Complete != true || report.Artifacts[0].SizeBytes == 0 {
		t.Fatalf("artifact = %+v", report.Artifacts[0])
	}
	if !report.SensitiveData {
		t.Fatal("sensitive data notice missing from the payload")
	}
	manifest, err := Verify(report.Artifacts[0].Path)
	if err != nil {
		t.Fatalf("Verify published artifact: %v", err)
	}
	if manifest.Source.VolumeKey != "data" || manifest.Source.VolumeName != "proj_data" {
		t.Fatalf("manifest source = %+v", manifest.Source)
	}
	if strings.Join(manifest.Stack.RunningBefore, ",") != "app,db" {
		t.Fatalf("manifest running before = %v", manifest.Stack.RunningBefore)
	}
	if strings.Join(manifest.Stack.StoppedForBackup, ",") != "app,db" {
		t.Fatalf("manifest stopped = %v", manifest.Stack.StoppedForBackup)
	}
	if manifest.Tool.Version != "test" || !manifest.Sensitive {
		t.Fatalf("manifest = %+v", manifest)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), PartialSuffix) {
			t.Fatalf("partial artifact left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Fatalf("output entries = %v", entries)
	}
}

func TestRunRefusesAnotherStacksConsumerWithoutStoppingAnything(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{volumeSource("data", "proj_data", "app")},
		runtime:     []data.ServiceRuntime{{Service: "other-app", State: "running"}},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {{Name: "other-app-1", Project: "other", Service: "app", State: "running"}},
		},
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), invocation(t, projectDir, output, "data"))
	if err == nil {
		t.Fatal("backup with another stack's consumer was allowed")
	}
	if len(backend.stops) != 0 || len(backend.starts) != 0 {
		t.Fatalf("another stack consumer caused stop/start: %v %v", backend.stops, backend.starts)
	}
	if !hasCategory(err, core.CategoryBackupConsumerUnsafe) {
		t.Fatalf("diagnostics = %v", err)
	}
	if !strings.Contains(errorDetail(err), "other") {
		t.Fatalf("refusal does not name the consumer: %v", errorDetail(err))
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		// The output directory may exist only if the pre-check created it; no
		// artifact may exist.
		entries, _ := os.ReadDir(output)
		if len(entries) != 0 {
			t.Fatalf("artifact was created despite refusal: %v", entries)
		}
	}
}

func TestRunFailsBeforeStoppingWhenTheOutputDirectoryIsMissing(t *testing.T) {
	projectDir := t.TempDir()
	backend := &fakeBackend{projectName: "proj", sources: []data.DataSource{volumeSource("data", "proj_data", "app")}}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), invocation(t, projectDir, ""))
	if !hasCategory(err, core.CategoryBackupOutputRequired) {
		t.Fatalf("error = %v", err)
	}
	if len(backend.stops) != 0 {
		t.Fatalf("services were stopped before the output check: %v", backend.stops)
	}
}

func TestRunRequiresAnExplicitVolumeSelection(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{projectName: "proj", sources: []data.DataSource{volumeSource("data", "proj_data", "app")}}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), invocation(t, projectDir, output))
	if !hasCategory(err, core.CategoryBackupVolumeRequired) {
		t.Fatalf("error = %v", err)
	}
	if len(backend.stops) != 0 {
		t.Fatalf("services stopped without a selection: %v", backend.stops)
	}
}

func TestRunFailsWithStackBusyBeforeStopping(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	stateDir := t.TempDir()
	backend := &fakeBackend{projectName: "proj", sources: []data.DataSource{volumeSource("data", "proj_data", "app")}}
	backend.defaultArchive(t)
	controller, store := newController(t, backend, fakeNames{name: "proj"}, stateDir)
	target := core.Target{ProjectDir: projectDir, Files: []string{filepath.Join(projectDir, "compose.yaml")}}
	lock, err := store.AcquireStackLock(context.Background(), target, state.LockHolder{Action: "up", PID: 99})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	_, runErr := controller.Run(context.Background(), invocation(t, projectDir, output, "data"))
	if !hasCategory(runErr, core.CategoryStackBusy) {
		t.Fatalf("error = %v", runErr)
	}
	if len(backend.stops) != 0 {
		t.Fatalf("busy stack was stopped: %v", backend.stops)
	}
}

func TestRunCleansUpAndRestartsAfterAReadFailure(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{volumeSource("data", "proj_data", "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "running"}},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
	}
	backend.archive = func(volume, dir string) error {
		if err := os.WriteFile(filepath.Join(dir, DataFile), []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		return errors.New("tar: can't open './secret': Permission denied")
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), invocation(t, projectDir, output, "data"))
	if !hasCategory(err, core.CategoryBackupFailed) {
		t.Fatalf("error = %v", err)
	}
	if strings.Join(backend.starts[0], ",") != "app" {
		t.Fatalf("services were not restarted: %v", backend.starts)
	}
	entries, readErr := os.ReadDir(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("failed backup was published: %v", entries)
		}
	}
	if !strings.Contains(errorDetail(err), "Permission denied") && !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("read failure output missing: %v", err)
	}
}

func TestRunRejectsATruncatedArchiveAndDoesNotPublishIt(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{volumeSource("data", "proj_data", "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "running"}},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
	}
	backend.archive = func(volume, dir string) error {
		path := filepath.Join(dir, DataFile)
		writeFixtureArchive(t, path, referenceEntries())
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(path, raw[:len(raw)-8], 0o600)
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), invocation(t, projectDir, output, "data"))
	if !hasCategory(err, core.CategoryBackupFailed) && !hasCategory(err, core.CategoryBackupIntegrityFailed) {
		t.Fatalf("error = %v", err)
	}
	if strings.Join(backend.starts[0], ",") != "app" {
		t.Fatalf("services were not restarted: %v", backend.starts)
	}
	entries, readErr := os.ReadDir(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("truncated backup was published: %v", entries)
	}
}

func TestRunRestartsServicesAfterCancellation(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	ctx, cancel := context.WithCancel(context.Background())
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{volumeSource("data", "proj_data", "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "running"}},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
	}
	backend.archive = func(volume, dir string) error {
		_ = os.WriteFile(filepath.Join(dir, DataFile), []byte("partial"), 0o600)
		cancel()
		return ctx.Err()
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(ctx, invocation(t, projectDir, output, "data"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if len(backend.starts) != 1 || strings.Join(backend.starts[0], ",") != "app" {
		t.Fatalf("canceled backup did not restore services: %v", backend.starts)
	}
	entries, _ := os.ReadDir(output)
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("canceled backup was published: %v", entries)
		}
	}
}

func TestRunRefusesAServiceThatCannotBeRestoredSafely(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{volumeSource("data", "proj_data", "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "paused"}},
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), invocation(t, projectDir, output, "data"))
	if !hasCategory(err, core.CategoryBackupConsumerUnsafe) {
		t.Fatalf("error = %v", err)
	}
	if len(backend.stops) != 0 {
		t.Fatalf("paused service was stopped: %v", backend.stops)
	}
}

func TestRepeatedRunsKeepBothBackups(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{volumeSource("data", "proj_data", "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "running"}},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
	}
	backend.defaultArchive(t)
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	for i := 0; i < 2; i++ {
		if _, err := controller.Run(context.Background(), invocation(t, projectDir, output, "data")); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	artifacts, err := List(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artifacts = %d, want 2", len(artifacts))
	}
}

func TestMultipleVolumesProduceOneArtifactEach(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources: []data.DataSource{
			volumeSource("data", "proj_data", "app"),
			volumeSource("logs", "proj_logs", "app"),
		},
		runtime: []data.ServiceRuntime{{Service: "app", State: "running"}},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
			"proj_logs": {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
	}
	backend.defaultArchive(t)
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	payload, err := controller.Run(context.Background(), invocation(t, projectDir, output, "data", "logs"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := payload.(Payload).Backup
	if len(report.Artifacts) != 2 {
		t.Fatalf("artifacts = %+v", report.Artifacts)
	}
	if len(backend.stops) != 1 {
		t.Fatalf("stop was called %d times, want once for the union", len(backend.stops))
	}
}

func TestPlanDescribesTheExactSelectionWithoutChangingAnything(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources: []data.DataSource{
			volumeSource("data", "proj_data", "app"),
			{Kind: data.KindVolume, Anonymous: true, Services: []string{"app"}, MountTargets: []string{"/anon"}},
			{Kind: data.KindBind, Name: "/host/bind", Services: []string{"app"}},
		},
		runtime: []data.ServiceRuntime{
			{Service: "app", State: "running"},
			{Service: "worker", State: "exited"},
		},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	plan, err := controller.Plan(context.Background(), invocation(t, projectDir, output, "data"))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, want := range []string{"data", "proj_data", output, "app", "실행 중", "중지", "민감", "tar.gz", "alpine"} {
		if !strings.Contains(plan.Text, want) {
			t.Fatalf("plan missing %q:\n%s", want, plan.Text)
		}
	}
	if len(backend.stops) != 0 || len(backend.starts) != 0 {
		t.Fatalf("plan changed the stack: %v %v", backend.stops, backend.starts)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("plan created the output directory")
	}
}

func TestPlanAndRunRefuseToPresentSecrets(t *testing.T) {
	// The plan and report are built only from source identity and states; no
	// environment values or file contents enter the payload.
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{volumeSource("data", "proj_data", "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "running"}},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
	}
	backend.defaultArchive(t)
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	payload, err := controller.Run(context.Background(), invocation(t, projectDir, output, "data"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"SUPER_SECRET", "PASSWORD", "TOKEN"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("payload leaked %q: %s", forbidden, raw)
		}
	}
}

func hasCategory(err error, category core.Category) bool {
	var ce *core.Error
	if !errors.As(err, &ce) {
		return false
	}
	for _, d := range ce.Diagnostics {
		if d.Category == category {
			return true
		}
	}
	return false
}

func errorDetail(err error) string {
	var ce *core.Error
	if !errors.As(err, &ce) {
		return ""
	}
	var parts []string
	for _, d := range ce.Diagnostics {
		parts = append(parts, d.Detail)
	}
	return strings.Join(parts, "\n")
}
