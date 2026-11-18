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
	mu            sync.Mutex
	projectName   string
	sources       []data.DataSource
	runtime       []data.ServiceRuntime
	consumers     map[string][]data.ContainerConsumer
	bindConsumers map[string][]data.ContainerConsumer
	archive       func(volume, dir string) error
	events        []string
	stops         [][]string
	starts        [][]string
	helperImage   string
	projectError  error
	redact        func(string) string
	sourcesSeq    [][]data.DataSource
	sourcesCalls  int
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sourcesSeq) > 0 {
		index := f.sourcesCalls
		if index >= len(f.sourcesSeq) {
			index = len(f.sourcesSeq) - 1
		}
		f.sourcesCalls++
		return append([]data.DataSource{}, f.sourcesSeq[index]...), nil
	}
	f.sourcesCalls++
	return append([]data.DataSource{}, f.sources...), nil
}

func (f *fakeBackend) ServiceRuntime(ctx context.Context, target core.Target) ([]data.ServiceRuntime, error) {
	return append([]data.ServiceRuntime{}, f.runtime...), nil
}

func (f *fakeBackend) VolumeConsumers(ctx context.Context, target core.Target, volumeName string) ([]data.ContainerConsumer, error) {
	return append([]data.ContainerConsumer{}, f.consumers[volumeName]...), nil
}

func (f *fakeBackend) BindConsumers(ctx context.Context, target core.Target, hostPath string) ([]data.ContainerConsumer, error) {
	return append([]data.ContainerConsumer{}, f.bindConsumers[hostPath]...), nil
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

func (f *fakeBackend) Redact(ctx context.Context, target core.Target, text string) string {
	if f.redact == nil {
		return text
	}
	return f.redact(text)
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
		Locks:   store,
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

func TestToolOutputIsRedactedBeforeItBecomesADiagnostic(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	const sentinel = "SENTINEL-SECRET-VALUE"
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{volumeSource("data", "proj_data", "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "running"}},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
		redact: func(text string) string {
			return strings.ReplaceAll(text, sentinel, "[redacted]")
		},
	}
	backend.archive = func(volume, dir string) error {
		return errors.New("helper failed for /var/lib/docker/" + sentinel)
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), invocation(t, projectDir, output, "data"))
	if err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(err.Error(), sentinel) || strings.Contains(errorDetail(err), sentinel) {
		t.Fatalf("diagnostic leaked the sentinel: %v / %s", err, errorDetail(err))
	}
	if !strings.Contains(errorDetail(err), "[redacted]") {
		t.Fatalf("redaction was not applied: %s", errorDetail(err))
	}
}

func TestRunReResolvesTheSelectionBeforeStopping(t *testing.T) {
	projectDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sourcesSeq: [][]data.DataSource{
			{volumeSource("data", "proj_data", "app")},
			{volumeSource("data", "renamed_data", "app")},
		},
		runtime: []data.ServiceRuntime{{Service: "app", State: "running"}},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
	}
	backend.defaultArchive(t)
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), invocation(t, projectDir, output, "data"))
	if !hasCategory(err, core.CategoryBackupSelectionChanged) {
		t.Fatalf("error = %v", err)
	}
	if len(backend.stops) != 0 {
		t.Fatalf("changed selection was stopped: %v", backend.stops)
	}
	if entries, _ := os.ReadDir(output); len(entries) != 0 {
		t.Fatalf("changed selection was archived: %v", entries)
	}
}

func TestWizardStepsListsVolumesAndBindFoldersAndPreviewShowsTheMixedEffect(t *testing.T) {
	projectDir := t.TempDir()
	bindDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{
		projectName: "proj",
		sources: []data.DataSource{
			volumeSource("data", "proj_data", "app", "worker"),
			volumeSource("logs", "proj_logs", "app"),
			{Kind: data.KindVolume, Anonymous: true, Services: []string{"app"}},
			bindSource(bindDir, "app", "worker"),
		},
		runtime: []data.ServiceRuntime{
			{Service: "app", State: "running"},
			{Service: "worker", State: "exited"},
		},
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	in := invocation(t, projectDir, "")
	in.Options["wizard"] = "steps"
	spec, err := controller.WizardSteps(context.Background(), in)
	if err != nil {
		t.Fatalf("WizardSteps: %v", err)
	}
	if len(spec.Steps) != 2 || spec.Steps[0].Name != "sources" || spec.Steps[1].Name != "output" {
		t.Fatalf("steps = %+v", spec.Steps)
	}
	if !spec.Steps[0].Multi {
		t.Fatal("the mixed source step is not a multi selection")
	}
	if len(spec.Steps[0].Options) != 3 {
		t.Fatalf("source options = %+v (anonymous must not be offered)", spec.Steps[0].Options)
	}
	labels := ""
	for _, option := range spec.Steps[0].Options {
		labels += option.Label + "\n"
	}
	for _, want := range []string{"proj_data", "폴더 " + bindDir, "서비스 app", "다른 스택 공유 없음"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("option labels missing %q:\n%s", want, labels)
		}
	}
	if spec.Steps[1].Default == "" || !spec.Steps[1].AllowCustom {
		t.Fatalf("output step = %+v", spec.Steps[1])
	}
	preview, err := controller.wizardPreview(map[string]string{
		"sources": "volume:data\nbind:" + bindDir,
		"output":  "./out",
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, want := range []string{"proj_data", "폴더 " + bindDir, "app, worker", "실행 중", "./out", "민감", "다른 스택 공유"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q:\n%s", want, preview)
		}
	}
	if strings.Contains(preview, "worker, app") {
		t.Fatalf("preview order is unstable:\n%s", preview)
	}
}

func TestDiskFullFromAFakeWriterFailsCleanlyAndRestoresServices(t *testing.T) {
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
		if err := os.WriteFile(filepath.Join(dir, DataFile), make([]byte, 4096), 0o600); err != nil {
			t.Fatal(err)
		}
		return errors.New("gzip: write error: No space left on device")
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), invocation(t, projectDir, output, "data"))
	if !hasCategory(err, core.CategoryBackupFailed) {
		t.Fatalf("error = %v", err)
	}
	var message string
	var ce *core.Error
	if errors.As(err, &ce) && len(ce.Diagnostics) > 0 {
		message = ce.Diagnostics[0].Message
	}
	if !strings.Contains(message, "공간") {
		t.Fatalf("disk-full guidance missing: %v", err)
	}
	if strings.Join(backend.starts[0], ",") != "app" {
		t.Fatalf("services were not restarted: %v", backend.starts)
	}
	artifacts, listErr := List(output)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(artifacts) != 0 {
		t.Fatalf("disk-full partial was listed as valid: %+v", artifacts)
	}
}

func TestAnEarlierPublishedBackupIsNamedWhenALaterVolumeFails(t *testing.T) {
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
	backend.archive = func(volume, dir string) error {
		if volume == "proj_logs" {
			return errors.New("helper failed")
		}
		writeValidArchive(t, dir)
		return nil
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), invocation(t, projectDir, output, "data", "logs"))
	if err == nil {
		t.Fatal("expected a failure")
	}
	detail := errorDetail(err)
	if !strings.Contains(detail, "dcraft-proj-volume-data") || !strings.Contains(detail, "이미 완료된 백업") {
		t.Fatalf("published artifact not named: %s", detail)
	}
}

func bindInvocation(t *testing.T, projectDir, output string, binds ...string) core.Invocation {
	t.Helper()
	in := invocation(t, projectDir, output)
	in.OptionLists = map[string][]string{"bind": binds}
	return in
}

func TestRunBacksUpADeclaredBindFolderWithTheV1Format(t *testing.T) {
	projectDir := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".hidden"), []byte("dot"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.txt", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{bindSource(source, "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "running"}},
		bindConsumers: map[string][]data.ContainerConsumer{
			source: {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	payload, err := controller.Run(context.Background(), bindInvocation(t, projectDir, output, source))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := payload.(Payload).Backup
	if len(report.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v", report.Artifacts)
	}
	if strings.Join(report.Stopped, ",") != "app" || strings.Join(report.Restarted, ",") != "app" {
		t.Fatalf("stopped/restarted = %v/%v", report.Stopped, report.Restarted)
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Verify(report.Artifacts[0].Path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if manifest.Source.Kind != data.KindBind || manifest.Source.HostPath != resolved || manifest.Source.VolumeName != "" {
		t.Fatalf("manifest source = %+v", manifest.Source)
	}
	if !manifest.Sensitive || !strings.Contains(manifest.OwnershipNote, "xattr") {
		t.Fatalf("manifest notes = %+v", manifest)
	}
	byPath := map[string]Entry{}
	for _, entry := range manifest.Entries {
		byPath[entry.Path] = entry
	}
	for _, want := range []string{"./.hidden", "./file.txt", "./link"} {
		if _, ok := byPath[want]; !ok {
			t.Fatalf("manifest missing %q: %+v", want, manifest.Entries)
		}
	}
	if byPath["./link"].Type != EntrySymlink {
		t.Fatalf("symlink entry = %+v", byPath["./link"])
	}
	if !containsNotice(report.Notices, "호스트에서 직접 tar") {
		t.Fatalf("host archive notice missing: %+v", report.Notices)
	}
	if containsNotice(report.Notices, "Docker 컨테이너(tar)") {
		t.Fatalf("volume helper notice shown for a bind-only backup: %+v", report.Notices)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), PartialSuffix) {
			t.Fatalf("partial artifact left behind: %s", entry.Name())
		}
	}
}

func TestRunMixesVolumeAndBindSelectionIntoTwoV1Artifacts(t *testing.T) {
	projectDir := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "bind.txt"), []byte("bind"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources: []data.DataSource{
			volumeSource("data", "proj_data", "app"),
			bindSource(source, "app", "worker"),
		},
		runtime: []data.ServiceRuntime{
			{Service: "app", State: "running"},
			{Service: "worker", State: "running"},
		},
		consumers: map[string][]data.ContainerConsumer{
			"proj_data": {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
		bindConsumers: map[string][]data.ContainerConsumer{
			source: {{Name: "proj-worker-1", Project: "proj", Service: "worker", State: "running"}},
		},
	}
	backend.defaultArchive(t)
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	in := invocation(t, projectDir, output, "data")
	in.OptionLists["bind"] = []string{source}
	payload, err := controller.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := payload.(Payload).Backup
	if len(report.Artifacts) != 2 {
		t.Fatalf("artifacts = %+v", report.Artifacts)
	}
	kinds := map[data.Kind]int{}
	for _, artifact := range report.Artifacts {
		kinds[artifact.Source.Kind]++
	}
	if kinds[data.KindVolume] != 1 || kinds[data.KindBind] != 1 {
		t.Fatalf("artifact kinds = %v", kinds)
	}
	if strings.Join(report.Stopped, ",") != "app,worker" {
		t.Fatalf("stopped = %v", report.Stopped)
	}
}

func TestRunRefusesAnOutputInsideTheBindSourceBeforeStopping(t *testing.T) {
	projectDir := t.TempDir()
	source := t.TempDir()
	output := filepath.Join(source, "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{bindSource(source, "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "running"}},
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), bindInvocation(t, projectDir, output, source))
	if !hasCategory(err, core.CategoryBackupBindUnsafe) {
		t.Fatalf("error = %v", err)
	}
	if len(backend.stops) != 0 {
		t.Fatalf("services were stopped before the containment check: %v", backend.stops)
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("dcraft created the output directory inside the source")
	}
}

func TestRunRefusesAnotherStacksBindConsumerWithoutStoppingAnything(t *testing.T) {
	projectDir := t.TempDir()
	source := t.TempDir()
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{bindSource(source, "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "running"}},
		bindConsumers: map[string][]data.ContainerConsumer{
			source: {{Name: "other-app-1", Project: "other", Service: "app", State: "running"}},
		},
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), bindInvocation(t, projectDir, output, source))
	if !hasCategory(err, core.CategoryBackupConsumerUnsafe) {
		t.Fatalf("error = %v", err)
	}
	if len(backend.stops) != 0 || len(backend.starts) != 0 {
		t.Fatalf("another stack's consumer caused stop/start: %v %v", backend.stops, backend.starts)
	}
	if entries, _ := os.ReadDir(output); len(entries) != 0 {
		t.Fatalf("artifact was created despite refusal: %v", entries)
	}
}

func TestRunFailsClearlyOnAnUnreadableBindFolderWithoutStopping(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-000 folder")
	}
	projectDir := t.TempDir()
	source := t.TempDir()
	if err := os.Chmod(source, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(source, 0o700) })
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{bindSource(source, "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "running"}},
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), bindInvocation(t, projectDir, output, source))
	if !hasCategory(err, core.CategoryBackupBindInvalid) {
		t.Fatalf("error = %v", err)
	}
	if len(backend.stops) != 0 {
		t.Fatalf("unreadable folder stopped services: %v", backend.stops)
	}
	artifacts, listErr := List(output)
	if listErr != nil {
		if !errors.Is(listErr, os.ErrNotExist) {
			t.Fatal(listErr)
		}
		artifacts = nil
	}
	if len(artifacts) != 0 {
		t.Fatalf("unreadable folder published an artifact: %+v", artifacts)
	}
}

func TestRunCancellationAfterStopRestoresBindConsumersAndPublishesNothing(t *testing.T) {
	projectDir := t.TempDir()
	source := t.TempDir()
	big, err := os.Create(filepath.Join(source, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	if err := big.Close(); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{bindSource(source, "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "running"}},
		bindConsumers: map[string][]data.ContainerConsumer{
			source: {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := controller.Run(ctx, bindInvocation(t, projectDir, output, source))
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		backend.mu.Lock()
		stopped := len(backend.stops) > 0
		backend.mu.Unlock()
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("backup never stopped the bind consumer")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	runErr := <-done
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", runErr)
	}
	backend.mu.Lock()
	starts := append([][]string{}, backend.starts...)
	backend.mu.Unlock()
	if len(starts) != 1 || strings.Join(starts[0], ",") != "app" {
		t.Fatalf("canceled backup did not restore services: %v", starts)
	}
	artifacts, listErr := List(output)
	if listErr != nil && !errors.Is(listErr, os.ErrNotExist) {
		t.Fatal(listErr)
	}
	if len(artifacts) != 0 {
		t.Fatalf("canceled backup was listed as valid: %+v", artifacts)
	}
}

func TestRunRestoresServicesAfterAnArchiveReadFailureInsideABindFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-000 folder")
	}
	projectDir := t.TempDir()
	source := t.TempDir()
	locked := filepath.Join(source, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	output := filepath.Join(t.TempDir(), "backups")
	backend := &fakeBackend{
		projectName: "proj",
		sources:     []data.DataSource{bindSource(source, "app")},
		runtime:     []data.ServiceRuntime{{Service: "app", State: "running"}},
		bindConsumers: map[string][]data.ContainerConsumer{
			source: {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
		},
	}
	controller, _ := newController(t, backend, fakeNames{name: "proj"}, t.TempDir())
	_, err := controller.Run(context.Background(), bindInvocation(t, projectDir, output, source))
	if !hasCategory(err, core.CategoryBackupBindInvalid) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(errorDetail(err), "locked") {
		t.Fatalf("unreadable path not named: %s", errorDetail(err))
	}
	if len(backend.starts) != 1 || strings.Join(backend.starts[0], ",") != "app" {
		t.Fatalf("services were not restored after the archive failure: %v", backend.starts)
	}
	artifacts, listErr := List(output)
	if listErr != nil && !errors.Is(listErr, os.ErrNotExist) {
		t.Fatal(listErr)
	}
	if len(artifacts) != 0 {
		t.Fatalf("failed bind backup was listed as valid: %+v", artifacts)
	}
}

func containsNotice(notices []string, substring string) bool {
	for _, notice := range notices {
		if strings.Contains(notice, substring) {
			return true
		}
	}
	return false
}
