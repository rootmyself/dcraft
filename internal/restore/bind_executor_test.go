package restore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
	"github.com/rootmyself/dcraft/internal/state"
)

// faultBindDestination is a controllable bind destination used to exercise the
// shared Executor's failure and cancellation reporting without a second
// replacement pipeline.
type faultBindDestination struct {
	root        string
	archiveErr  error
	extractErr  error
	verifyErr   error
	emptyCalled bool
}

func (d *faultBindDestination) Kind() data.Kind        { return data.KindBind }
func (d *faultBindDestination) Key() string            { return d.root }
func (d *faultBindDestination) Name() string           { return d.root }
func (d *faultBindDestination) Services() []string     { return []string{"app"} }
func (d *faultBindDestination) MountTargets() []string { return []string{"/data"} }

func (d *faultBindDestination) ManifestSource() backup.Source {
	return bindSourceFixture(d.root)
}

func (d *faultBindDestination) Match(backup.Manifest, bool) ([]string, error) { return nil, nil }

func (d *faultBindDestination) Outside(string) (bool, error) { return true, nil }

func (d *faultBindDestination) Prepare(context.Context) error { return nil }

func (d *faultBindDestination) UnsafeConsumers(context.Context) ([]data.ContainerConsumer, error) {
	return nil, nil
}

func (d *faultBindDestination) Usage(context.Context) (ContentSummary, error) {
	return ContentSummary{Bytes: 1024, Entries: 3}, nil
}

func (d *faultBindDestination) SafetyBackup(ctx context.Context, hostDir string) (data.OpResult, error) {
	if d.archiveErr != nil {
		return data.OpResult{Output: "tar: read error"}, d.archiveErr
	}
	if err := backup.ArchiveHostDir(ctx, d.root, filepath.Join(hostDir, backup.DataFile)); err != nil {
		return data.OpResult{}, err
	}
	return data.OpResult{}, nil
}

func (d *faultBindDestination) Empty(context.Context) (data.OpResult, error) {
	d.emptyCalled = true
	return data.OpResult{}, nil
}

func (d *faultBindDestination) Extract(context.Context, string) (data.OpResult, error) {
	return data.OpResult{}, d.extractErr
}

func (d *faultBindDestination) Verify(context.Context, backup.Manifest) (data.OpResult, error) {
	return data.OpResult{}, d.verifyErr
}

func newFaultBindDestination(t *testing.T) *faultBindDestination {
	t.Helper()
	root := t.TempDir()
	seedBindV1(t, root)
	return &faultBindDestination{root: root}
}

func executeBindFault(t *testing.T, dest *faultBindDestination) (ExecutionReport, error) {
	t.Helper()
	artifact, manifest := buildBindArtifact(t, dest.root, dest.root, nil)
	parsed, err := ValidateArtifact(artifact, "")
	if err != nil {
		t.Fatalf("ValidateArtifact: %v", err)
	}
	if parsed.Manifest.Source.Kind != data.KindBind || len(manifest.Entries) == 0 {
		t.Fatalf("artifact = %+v", parsed)
	}
	backend := defaultBackend()
	backend.sources = []data.DataSource{{Kind: data.KindBind, Declared: dest.root, Name: dest.root, Services: []string{"app"}}}
	backend.runtime = []data.ServiceRuntime{{Service: "app", State: "running"}}
	backend.bindConsumers = map[string][]data.ContainerConsumer{
		dest.root: {{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"}},
	}
	exec := &Executor{
		Backend: backend,
		Locks:   state.New(t.TempDir()),
		Clock:   fixedClock{now: time.Unix(1700000000, 0)},
		Tool:    backup.Tool{Version: "test"},
		PID:     4242,
		Space:   &fakeSpace{available: 1 << 30},
	}
	report, err := exec.Execute(context.Background(), core.Invocation{Reporter: &stageReporter{}}, ExecutionRequest{
		Target: core.Target{ProjectDir: t.TempDir()}, ProjectName: "proj",
		Artifact: parsed, Destination: dest, SafetyDir: t.TempDir(),
		Affected: []string{"app"}, Running: []string{"app"},
	})
	return report, err
}

func TestExecutorBindVerificationFailureReportsUncertainStateWithoutRestart(t *testing.T) {
	dest := newFaultBindDestination(t)
	dest.verifyErr = errors.New("mismatch SECRET_VALUE")
	report, err := executeBindFault(t, dest)
	if err == nil || !hasCategory(err, core.CategoryRestoreVerifyFailed) {
		t.Fatalf("err = %v", err)
	}
	if report.DataState != DataUncertain || report.Stage != "verify" {
		t.Fatalf("report = %+v", report)
	}
	if report.SafetyBackup == "" {
		t.Fatal("safety backup path missing")
	}
	if !dest.emptyCalled {
		t.Fatal("verification failure test never reached replacement")
	}
	detail := errorDetail(err)
	for _, want := range []string{"verify", string(DataUncertain), report.SafetyBackup} {
		if !strings.Contains(detail, want) {
			t.Fatalf("failure detail missing %q:\n%s", want, detail)
		}
	}
}

func TestExecutorBindExtractCancellationKeepsServicesStoppedAndReportsSafety(t *testing.T) {
	dest := newFaultBindDestination(t)
	dest.extractErr = context.Canceled
	report, err := executeBindFault(t, dest)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	if report.DataState != DataUncertain || report.Stage != "replace" {
		t.Fatalf("report = %+v", report)
	}
	if report.SafetyBackup == "" {
		t.Fatal("safety backup path missing")
	}
	if len(report.Restarted) != 0 {
		t.Fatalf("interrupted replacement restarted services: %v", report.Restarted)
	}
}

func TestExecutorBindSafetyFailureAbortsUntouchedAndRestartsStoppedServices(t *testing.T) {
	dest := newFaultBindDestination(t)
	dest.archiveErr = errors.New("tar: permission denied")
	report, err := executeBindFault(t, dest)
	if err == nil || !hasCategory(err, core.CategoryRestoreSafetyFailed) {
		t.Fatalf("err = %v", err)
	}
	if report.DataState != DataUntouched {
		t.Fatalf("data state = %s", report.DataState)
	}
	if dest.emptyCalled {
		t.Fatal("safety failure emptied the target")
	}
	if strings.Join(report.Restarted, ",") != "app" {
		t.Fatalf("stopped service was not restored: %v", report.Restarted)
	}
}

func TestExecutorBindSuccessOnlyRestartsPreviouslyRunningServices(t *testing.T) {
	dest := newFaultBindDestination(t)
	report, err := executeBindFault(t, dest)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if report.DataState != DataReplaced || report.TargetKind != data.KindBind || report.HostPath != dest.root {
		t.Fatalf("report = %+v", report)
	}
	if strings.Join(report.Restarted, ",") != "app" {
		t.Fatalf("restarted = %v", report.Restarted)
	}
	if report.VolumeKey != "" || report.VolumeName != "" {
		t.Fatalf("bind report carried volume identity: %+v", report)
	}
	if _, err := os.Stat(report.SafetyBackup); err != nil {
		t.Fatalf("safety backup missing: %v", err)
	}
}
