package restore

import (
	"context"
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

func bindRestoreFixture(t *testing.T) (string, string, string, *fakeBackend) {
	t.Helper()
	projectDir := t.TempDir()
	targetDir := t.TempDir()
	seedBindV1(t, targetDir)
	artifact, _ := buildBindArtifact(t, targetDir, targetDir, nil)

	// Version 2: one file changes, one is added and one is removed. A restore
	// must replace instead of merging these leftovers.
	if err := os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("version two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(targetDir, "run.sh")); err != nil {
		t.Fatal(err)
	}

	backend := defaultBackend()
	backend.sources = []data.DataSource{{
		Kind: data.KindBind, Declared: targetDir, Name: targetDir,
		MountTargets: []string{"/data"}, Services: []string{"app", "worker"},
	}}
	backend.runtime = []data.ServiceRuntime{
		{Service: "app", State: "running"},
		{Service: "worker", State: "exited", ExitCode: 137},
	}
	backend.bindConsumers = map[string][]data.ContainerConsumer{
		targetDir: {
			{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"},
			{Name: "proj-worker-1", Project: "proj", Service: "worker", State: "exited"},
		},
	}
	return projectDir, targetDir, artifact, backend
}

func TestRunBindRestoreReplacesTheFolderAndRestoresOnlyRunningServices(t *testing.T) {
	projectDir, targetDir, artifact, backend := bindRestoreFixture(t)
	expectedDir := t.TempDir()
	seedBindV1(t, expectedDir)
	want := bindTree(t, expectedDir)
	controller, stages, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	in := restoreInvocation(t, projectDir, artifact, map[string]string{"bind": targetDir})
	in.Reporter = stages

	payload, err := controller.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := payload.(Payload).Restore
	if report.DataState != DataReplaced {
		t.Fatalf("data state = %s", report.DataState)
	}
	if report.TargetKind != data.KindBind || report.HostPath == "" {
		t.Fatalf("target = %+v", report)
	}
	resolvedTarget, err := filepath.EvalSymlinks(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	if report.HostPath != resolvedTarget {
		t.Fatalf("host path = %q, want %q", report.HostPath, resolvedTarget)
	}
	if report.VolumeKey != "" || report.VolumeName != "" {
		t.Fatalf("bind report carried volume identity: %+v", report)
	}
	if strings.Join(report.Stopped, ",") != "app" || strings.Join(report.Restarted, ",") != "app" {
		t.Fatalf("services = stopped %v restarted %v", report.Stopped, report.Restarted)
	}
	if strings.Join(backend.stops[0], ",") != "app" || strings.Join(backend.starts[0], ",") != "app" {
		t.Fatalf("backend calls = %v / %v", backend.stops, backend.starts)
	}
	if report.SafetyBackup == "" {
		t.Fatal("safety backup path missing")
	}
	safetyManifest, err := backup.Verify(report.SafetyBackup)
	if err != nil {
		t.Fatalf("safety backup did not verify: %v", err)
	}
	if safetyManifest.Source.Kind != data.KindBind || safetyManifest.Source.HostPath != resolvedTarget {
		t.Fatalf("safety source = %+v", safetyManifest.Source)
	}
	if !strings.Contains(safetyManifest.OwnershipNote, "xattr") {
		t.Fatalf("safety ownership note = %q", safetyManifest.OwnershipNote)
	}
	safetyPaths := map[string]bool{}
	for _, entry := range safetyManifest.Entries {
		safetyPaths[entry.Path] = true
	}
	if !safetyPaths["./stale.txt"] {
		t.Fatalf("safety backup does not contain the pre-restore v2 content: %+v", safetyManifest.Entries)
	}
	got := bindTree(t, targetDir)
	if len(got) != len(want) {
		t.Fatalf("restored tree = %v, want %v", got, want)
	}
	for path, fact := range want {
		if got[path] != fact {
			t.Fatalf("%s = %q, want %q", path, got[path], fact)
		}
	}
	if _, ok := got["stale.txt"]; ok {
		t.Fatal("stale v2 file survived the restore")
	}
	if !containsSubstring(report.Notices, "연결 폴더") {
		t.Fatalf("bind notices missing: %v", report.Notices)
	}
	if !containsSubstring(report.Notices, "uid/gid") {
		t.Fatalf("ownership limitation notice missing: %v", report.Notices)
	}
	if containsSubstring(report.Notices, "이 명령의 범위가 아니") {
		t.Fatalf("stale out-of-scope notice is still shown: %v", report.Notices)
	}
}

func TestRunBindRefusesUndeclaredTargetWithoutStopping(t *testing.T) {
	projectDir, targetDir, artifact, backend := bindRestoreFixture(t)
	before := bindTree(t, targetDir)
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, projectDir, artifact,
		map[string]string{"bind": filepath.Join(t.TempDir(), "not-declared")}))
	if err == nil || !hasCategory(err, core.CategoryRestoreBindUnknown) {
		t.Fatalf("err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("undeclared target touched data: %v", backend.events)
	}
	if after := bindTree(t, targetDir); !sameFacts(before, after) {
		t.Fatal("undeclared target changed the declared folder")
	}
}

func TestRunBindPathMismatchRequiresTheExplicitFlagAndWarns(t *testing.T) {
	projectDir, _, artifact, backend := bindRestoreFixture(t)
	expectedDir := t.TempDir()
	seedBindV1(t, expectedDir)
	archived := bindTree(t, expectedDir)
	other := t.TempDir()
	seedBindV1(t, other)
	if err := os.WriteFile(filepath.Join(other, "other.txt"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	otherBefore := bindTree(t, other)
	backend.sources[0] = data.DataSource{
		Kind: data.KindBind, Declared: other, Name: other,
		MountTargets: []string{"/data"}, Services: []string{"app", "worker"},
	}
	backend.bindConsumers = map[string][]data.ContainerConsumer{
		other: {
			{Name: "proj-app-1", Project: "proj", Service: "app", State: "running"},
		},
	}

	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, projectDir, artifact, map[string]string{"bind": other}))
	if err == nil || !hasCategory(err, core.CategoryRestoreTargetMismatch) {
		t.Fatalf("path mismatch err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("path mismatch touched data: %v", backend.events)
	}
	if after := bindTree(t, other); !sameFacts(otherBefore, after) {
		t.Fatal("path mismatch changed the declared folder")
	}

	controller, _, _ = newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	payload, err := controller.Run(context.Background(), restoreInvocation(t, projectDir, artifact, map[string]string{
		"bind": other, "allow-path-mismatch": "true",
	}))
	if err != nil {
		t.Fatalf("explicit path mapping: %v", err)
	}
	report := payload.(Payload).Restore
	if !report.PathMapping {
		t.Fatalf("path mapping was not reported: %+v", report)
	}
	if !containsSubstring(report.Warnings, "경로가 백업과 다름") {
		t.Fatalf("mapping warning missing: %v", report.Warnings)
	}
	if after := bindTree(t, other); !sameFacts(archived, after) {
		t.Fatalf("explicit path mapping did not restore the archived content:\n%v\nwant\n%v", after, archived)
	}
}

func TestRunBindRefusesTheProjectRootAndUnsafeLocations(t *testing.T) {
	projectDir := t.TempDir()
	targetDir := t.TempDir()
	seedBindV1(t, targetDir)
	artifact, _ := buildBindArtifact(t, targetDir, targetDir, nil)
	backend := defaultBackend()
	backend.sources = []data.DataSource{{
		Kind: data.KindBind, Declared: projectDir, Name: projectDir,
		MountTargets: []string{"/work"}, Services: []string{"app"},
	}}
	backend.runtime = []data.ServiceRuntime{{Service: "app", State: "running"}}
	backend.bindConsumers = map[string][]data.ContainerConsumer{}
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, projectDir, artifact,
		map[string]string{"bind": projectDir, "allow-path-mismatch": "true"}))
	if err == nil || !hasCategory(err, core.CategoryRestoreTargetInvalid) {
		t.Fatalf("project root err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("project root touched data: %v", backend.events)
	}

	// The artifact itself inside the target is refused before any stop.
	targetDir = t.TempDir()
	seedBindV1(t, targetDir)
	artifactInside := filepath.Join(targetDir, "dcraft-backups", "artifact")
	if err := os.MkdirAll(artifactInside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := backup.ArchiveHostDir(context.Background(), targetDir, filepath.Join(artifactInside, backup.DataFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.BuildArtifact(artifactInside, bindSourceFixture(targetDir), backup.StackState{}, backup.Tool{}, fixedClock{}.Now()); err != nil {
		t.Fatal(err)
	}
	backend = defaultBackend()
	backend.sources = []data.DataSource{{Kind: data.KindBind, Declared: targetDir, Name: targetDir, Services: []string{"app"}}}
	backend.runtime = []data.ServiceRuntime{{Service: "app", State: "running"}}
	controller, _, _ = newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err = controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifactInside, nil))
	if err == nil || !hasCategory(err, core.CategoryRestoreTargetMismatch) {
		t.Fatalf("artifact inside target err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("artifact-inside refusal touched data: %v", backend.events)
	}

	// A safety folder inside the target is refused before creating it.
	controller, _, _ = newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	safety := filepath.Join(targetDir, "safety")
	_, err = controller.Run(context.Background(), restoreInvocation(t, t.TempDir(), artifact, map[string]string{
		"safety-output": safety, "bind": targetDir, "allow-path-mismatch": "true",
	}))
	if err == nil || !hasCategory(err, core.CategoryRestoreSafetyFailed) {
		t.Fatalf("safety inside target err = %v", err)
	}
	if _, statErr := os.Lstat(safety); !os.IsNotExist(statErr) {
		t.Fatalf("safety folder was created inside the target: %v", statErr)
	}
}

func TestRunBindSafetyFailureAbortsBeforeEmptying(t *testing.T) {
	projectDir, targetDir, artifact, backend := bindRestoreFixture(t)
	before := bindTree(t, targetDir)
	safetyFile := filepath.Join(t.TempDir(), "safety")
	if err := os.WriteFile(safetyFile, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, projectDir, artifact, map[string]string{
		"bind": targetDir, "safety-output": safetyFile,
	}))
	if err == nil || !hasCategory(err, core.CategoryRestoreSafetyFailed) {
		t.Fatalf("safety failure err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("safety failure stopped or replaced data: %v", backend.events)
	}
	if after := bindTree(t, targetDir); !sameFacts(before, after) {
		t.Fatal("safety failure changed the original folder")
	}
}

func TestRunBindRefusesAnotherStacksConsumer(t *testing.T) {
	projectDir, targetDir, artifact, backend := bindRestoreFixture(t)
	backend.bindConsumers[targetDir] = append(backend.bindConsumers[targetDir], data.ContainerConsumer{
		Name: "other-app-1", Project: "other", Service: "app", State: "running",
	})
	before := bindTree(t, targetDir)
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, projectDir, artifact, map[string]string{"bind": targetDir}))
	if err == nil || !hasCategory(err, core.CategoryRestoreConsumerUnsafe) {
		t.Fatalf("other-stack consumer err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("other-stack refusal touched data: %v", backend.events)
	}
	if after := bindTree(t, targetDir); !sameFacts(before, after) {
		t.Fatal("other-stack refusal changed the target")
	}
}

func TestRunBindRefusesWrongFlagForArtifactKind(t *testing.T) {
	projectDir, targetDir, bindArtifact, backend := bindRestoreFixture(t)
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err := controller.Run(context.Background(), restoreInvocation(t, projectDir, bindArtifact, map[string]string{"volume": "data"}))
	if err == nil || !hasCategory(err, core.CategoryRestoreTargetMismatch) {
		t.Fatalf("volume flag with bind artifact err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("wrong flag touched data: %v", backend.events)
	}

	volumeArtifact, _ := writeArtifact(t, referenceEntries(), nil)
	_, err = controller.Run(context.Background(), restoreInvocation(t, projectDir, volumeArtifact, map[string]string{"bind": targetDir}))
	if err == nil || !hasCategory(err, core.CategoryRestoreTargetMismatch) {
		t.Fatalf("bind flag with volume artifact err = %v", err)
	}
}

func TestRunBindRefusesCorruptArtifactsBeforeStopping(t *testing.T) {
	projectDir, targetDir, artifact, backend := bindRestoreFixture(t)
	raw, err := os.ReadFile(filepath.Join(artifact, backup.DataFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifact, backup.DataFile), raw[:len(raw)-8], 0o600); err != nil {
		t.Fatal(err)
	}
	before := bindTree(t, targetDir)
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	_, err = controller.Run(context.Background(), restoreInvocation(t, projectDir, artifact, map[string]string{"bind": targetDir}))
	if err == nil || !hasCategory(err, core.CategoryRestoreIntegrityFailed) {
		t.Fatalf("corrupt bind artifact err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("corrupt artifact touched data: %v", backend.events)
	}
	if after := bindTree(t, targetDir); !sameFacts(before, after) {
		t.Fatal("corrupt artifact changed the target")
	}
}

func TestRunBindTakesThePerStackLockBeforeSelecting(t *testing.T) {
	projectDir, targetDir, artifact, backend := bindRestoreFixture(t)
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("chmod state dir: %v", err)
	}
	locks := state.New(stateDir)
	in := restoreInvocation(t, projectDir, artifact, map[string]string{"bind": targetDir})
	held, err := locks.AcquireStackLock(context.Background(), in.Target, state.LockHolder{
		Action: "up", PID: 1, StartedAt: time.Unix(1700000000, 0),
	})
	if err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	defer func() { _ = held.Release() }()
	controller := New(Config{
		Backend: backend, Names: fakeNames{name: "proj"}, Locks: locks,
		Clock: fixedClock{now: time.Unix(1700000000, 0)}, Tool: backup.Tool{Version: "test"},
		PID: 4242, Space: &fakeSpace{available: 1 << 30},
	})
	before := bindTree(t, targetDir)
	_, err = controller.Run(context.Background(), in)
	if err == nil || !hasCategory(err, core.CategoryStackBusy) {
		t.Fatalf("stack busy err = %v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("busy stack touched data: %v", backend.events)
	}
	if after := bindTree(t, targetDir); !sameFacts(before, after) {
		t.Fatal("busy stack changed the target")
	}
}

func TestPlanBindIsReadOnlyAndShowsTheDeclaredTargetAndLimits(t *testing.T) {
	projectDir, targetDir, artifact, backend := bindRestoreFixture(t)
	controller, _, _ := newRestoreController(t, backend, "proj", &fakeSpace{available: 1 << 30})
	plan, err := controller.Plan(context.Background(), restoreInvocation(t, projectDir, artifact, map[string]string{"bind": targetDir}))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, want := range []string{"복원 계획", "폴더", "안전 백업 위치", "병합이 아님", "xattr"} {
		if !strings.Contains(plan.Text, want) {
			t.Fatalf("bind plan missing %q:\n%s", want, plan.Text)
		}
	}
	if len(backend.events) != 0 {
		t.Fatalf("plan changed the backend: %v", backend.events)
	}
}

func containsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func sameFacts(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func TestParseWizardTargetUsesKindPrefixes(t *testing.T) {
	volumes, binds := ParseWizardTarget("volume:data\nbind:/srv/app\nplain")
	if strings.Join(volumes, ",") != "data,plain" || strings.Join(binds, ",") != "/srv/app" {
		t.Fatalf("targets = %v / %v", volumes, binds)
	}
}
