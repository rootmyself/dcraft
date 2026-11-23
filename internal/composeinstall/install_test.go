package composeinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
)

type fakeInstaller struct {
	exists     map[string]bool
	existsErr  error
	installErr error
	installs   []string
	written    map[string][]byte
}

func (f *fakeInstaller) Exists(path string) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	return f.exists[path], nil
}

func (f *fakeInstaller) InstallAtomic(src, dest string, mode fs.FileMode) error {
	f.installs = append(f.installs, dest)
	if f.installErr != nil {
		return f.installErr
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if f.written == nil {
		f.written = map[string][]byte{}
	}
	f.written[dest] = data
	return nil
}

type fakeInstallDownloader struct {
	release   []byte
	checksums []byte
	asset     []byte
	assetErr  error
	gets      []string
	downloads []string
}

func (f *fakeInstallDownloader) Get(ctx context.Context, url string) ([]byte, error) {
	f.gets = append(f.gets, url)
	switch {
	case strings.HasSuffix(url, "/releases/latest"):
		return f.release, nil
	case strings.HasSuffix(url, "/checksums.txt"):
		return f.checksums, nil
	default:
		return nil, errors.New("unexpected metadata URL " + url)
	}
}

func (f *fakeInstallDownloader) Download(ctx context.Context, url, dest string) error {
	f.downloads = append(f.downloads, url)
	if f.assetErr != nil {
		return f.assetErr
	}
	return os.WriteFile(dest, f.asset, 0o600)
}

type probeRunner struct {
	results []execx.Result
	calls   int
	lines   []string
}

func (r *probeRunner) LookPath(file string) (string, error) { return file, nil }

func (r *probeRunner) Run(ctx context.Context, cmd execx.Command) (execx.Result, error) {
	r.calls++
	r.lines = append(r.lines, strings.Join(append([]string{cmd.Name}, cmd.Args...), " "))
	if len(r.results) == 0 {
		return execx.Result{ExitCode: 127}, nil
	}
	res := r.results[0]
	if len(r.results) > 1 {
		r.results = r.results[1:]
	}
	return res, nil
}

type stageRecorder struct{ stages []string }

func (r *stageRecorder) Begin(name, title string)                           { r.stages = append(r.stages, name) }
func (r *stageRecorder) End(name string, status core.StageStatus, d string) {}

func missingCompose() execx.Result {
	return execx.Result{ExitCode: 1, Stderr: []byte("docker: unknown command: docker compose")}
}

func workingCompose(version string) execx.Result {
	return execx.Result{Stdout: []byte(version + "\n")}
}

func newInstallFeature(t *testing.T, dl *fakeInstallDownloader, inst *fakeInstaller, runner execx.Runner, platform Platform) (*Feature, string) {
	t.Helper()
	dir := t.TempDir()
	return New(Config{
		Runner:     runner,
		Getenv:     envGetter(map[string]string{"DCRAFT_COMPOSE_PLUGIN_DIR": dir}),
		Downloader: dl,
		Installer:  inst,
		GOOS:       platform.GOOS,
		GOARCH:     platform.GOARCH,
	}), filepath.Join(dir, "docker-compose")
}

func TestPlanShowsSourceVersionDestinationPermissionAndEffect(t *testing.T) {
	dl := &fakeInstallDownloader{release: releaseFixture(t), checksums: testdata(t, "checksums.txt")}
	inst := &fakeInstaller{}
	runner := &probeRunner{results: []execx.Result{missingCompose()}}
	feature, dest := newInstallFeature(t, dl, inst, runner, Platform{"darwin", "arm64"})

	plan, err := feature.Plan(context.Background(), core.Invocation{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, want := range []string{
		"출처", "https://github.com/docker/compose/releases/download/v5.8.0/docker-compose-darwin-aarch64",
		"버전: v5.8.0", "대상 경로: " + dest, "관리자 권한(sudo)은 필요하지 않음",
		"변경 내용", "Docker CLI·Engine·Desktop과 WSL 통합은 변경하지 않음",
		"SHA-256",
	} {
		if !strings.Contains(plan.Text, want) {
			t.Fatalf("plan text missing %q:\n%s", want, plan.Text)
		}
	}
	if len(dl.downloads) != 0 || len(inst.installs) != 0 {
		t.Fatalf("plan changed something: downloads=%v installs=%v", dl.downloads, inst.installs)
	}
}

func TestRunDownloadsVerifiesInstallsAndRechecks(t *testing.T) {
	asset := machoHeader(machoCpuArm64)
	release, checksums := customRelease(t, "docker-compose-darwin-aarch64", asset)
	dl := &fakeInstallDownloader{release: release, checksums: checksums, asset: asset}
	inst := &fakeInstaller{}
	runner := &probeRunner{results: []execx.Result{missingCompose(), workingCompose("Docker Compose version v5.8.0")}}
	feature, dest := newInstallFeature(t, dl, inst, runner, Platform{"darwin", "arm64"})
	rec := &stageRecorder{}

	payload, err := feature.Run(context.Background(), core.Invocation{Reporter: rec})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, ok := payloadFrom(payload)
	if !ok {
		t.Fatalf("payload = %#v", payload)
	}
	if !got.Install.Changed || got.Install.AlreadyAvailable {
		t.Fatalf("install info = %+v", got.Install)
	}
	if got.Install.Version != "v5.8.0" || got.Install.Destination != dest {
		t.Fatalf("install info = %+v", got.Install)
	}
	if !got.Install.ChecksumVerified || got.Install.RecheckOutput != "Docker Compose version v5.8.0" {
		t.Fatalf("install info = %+v", got.Install)
	}
	if len(inst.installs) != 1 || inst.installs[0] != dest {
		t.Fatalf("installs = %v", inst.installs)
	}
	if string(inst.written[dest]) != string(asset) {
		t.Fatalf("installed bytes differ")
	}
	if strings.Join(rec.stages, ",") != "plan,download,verify,install,recheck" {
		t.Fatalf("stages = %v", rec.stages)
	}
}

func TestRunRefusesToOverwriteAnExistingPlugin(t *testing.T) {
	dl := &fakeInstallDownloader{release: releaseFixture(t), checksums: testdata(t, "checksums.txt")}
	runner := &probeRunner{results: []execx.Result{missingCompose()}}
	inst := &fakeInstaller{}
	feature, dest := newInstallFeature(t, dl, inst, runner, Platform{"darwin", "arm64"})
	inst.exists = map[string]bool{dest: true}

	_, err := feature.Run(context.Background(), core.Invocation{})
	ce := asCoreError(t, err)
	if ce.Diagnostics[0].Category != core.CategoryComposeInstallExists {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
	if len(dl.downloads) != 0 || len(inst.installs) != 0 {
		t.Fatalf("existing install was touched: downloads=%v installs=%v", dl.downloads, inst.installs)
	}
}

func TestRunChangesNothingWhenComposeAlreadyWorks(t *testing.T) {
	dl := &fakeInstallDownloader{}
	runner := &probeRunner{results: []execx.Result{workingCompose("Docker Compose version v5.7.0")}}
	inst := &fakeInstaller{}
	feature, dest := newInstallFeature(t, dl, inst, runner, Platform{"darwin", "arm64"})

	payload, err := feature.Run(context.Background(), core.Invocation{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := payloadFrom(payload)
	if !got.Install.AlreadyAvailable || got.Install.Changed {
		t.Fatalf("install info = %+v", got.Install)
	}
	if got.Install.Destination != dest {
		t.Fatalf("destination = %q", got.Install.Destination)
	}
	if len(dl.gets) != 0 || len(dl.downloads) != 0 || len(inst.installs) != 0 {
		t.Fatalf("already available still changed something: gets=%v downloads=%v installs=%v", dl.gets, dl.downloads, inst.installs)
	}
}

func TestChecksumMismatchFailsBeforeInstall(t *testing.T) {
	asset := machoHeader(machoCpuArm64)
	release, _ := customRelease(t, "docker-compose-darwin-aarch64", asset)
	sum := sha256.Sum256([]byte("something else"))
	checksums := []byte(hex.EncodeToString(sum[:]) + " *docker-compose-darwin-aarch64\n")
	dl := &fakeInstallDownloader{release: release, checksums: checksums, asset: asset}
	inst := &fakeInstaller{}
	runner := &probeRunner{results: []execx.Result{missingCompose()}}
	feature, _ := newInstallFeature(t, dl, inst, runner, Platform{"darwin", "arm64"})

	_, err := feature.Run(context.Background(), core.Invocation{})
	ce := asCoreError(t, err)
	if ce.Diagnostics[0].Category != core.CategoryComposeIntegrityMismatch {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
	if len(inst.installs) != 0 {
		t.Fatalf("installed after checksum mismatch: %v", inst.installs)
	}
}

func TestTruncatedDownloadIsDistinctFromIntegrityFailure(t *testing.T) {
	asset := machoHeader(machoCpuArm64)
	release, checksums := customRelease(t, "docker-compose-darwin-aarch64", append(asset, make([]byte, 10)...))
	dl := &fakeInstallDownloader{release: release, checksums: checksums, asset: asset}
	inst := &fakeInstaller{}
	runner := &probeRunner{results: []execx.Result{missingCompose()}}
	feature, _ := newInstallFeature(t, dl, inst, runner, Platform{"darwin", "arm64"})

	_, err := feature.Run(context.Background(), core.Invocation{})
	ce := asCoreError(t, err)
	if ce.Diagnostics[0].Category != core.CategoryComposeDownloadFailed {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
	if len(inst.installs) != 0 {
		t.Fatalf("installed a truncated download: %v", inst.installs)
	}
}

func TestWrongArchitectureIsDistinctFromInvalidArtifact(t *testing.T) {
	asset := machoHeader(machoCpuX86_64)
	release, checksums := customRelease(t, "docker-compose-darwin-aarch64", asset)
	dl := &fakeInstallDownloader{release: release, checksums: checksums, asset: asset}
	inst := &fakeInstaller{}
	runner := &probeRunner{results: []execx.Result{missingCompose()}}
	feature, _ := newInstallFeature(t, dl, inst, runner, Platform{"darwin", "arm64"})

	_, err := feature.Run(context.Background(), core.Invocation{})
	ce := asCoreError(t, err)
	if ce.Diagnostics[0].Category != core.CategoryComposeArchMismatch {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
}

func TestNonExecutableArtifactIsRejected(t *testing.T) {
	asset := []byte("<html>releases are temporarily unavailable</html>")
	release, checksums := customRelease(t, "docker-compose-darwin-aarch64", asset)
	dl := &fakeInstallDownloader{release: release, checksums: checksums, asset: asset}
	inst := &fakeInstaller{}
	runner := &probeRunner{results: []execx.Result{missingCompose()}}
	feature, _ := newInstallFeature(t, dl, inst, runner, Platform{"darwin", "arm64"})

	_, err := feature.Run(context.Background(), core.Invocation{})
	ce := asCoreError(t, err)
	if ce.Diagnostics[0].Category != core.CategoryComposeArtifactInvalid {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
	if len(inst.installs) != 0 {
		t.Fatalf("installed a non-executable artifact: %v", inst.installs)
	}
}

func TestPermissionFailureIsActionableAndChangesNothing(t *testing.T) {
	asset := machoHeader(machoCpuArm64)
	release, checksums := customRelease(t, "docker-compose-darwin-aarch64", asset)
	dl := &fakeInstallDownloader{release: release, checksums: checksums, asset: asset}
	inst := &fakeInstaller{installErr: fs.ErrPermission}
	runner := &probeRunner{results: []execx.Result{missingCompose()}}
	feature, _ := newInstallFeature(t, dl, inst, runner, Platform{"darwin", "arm64"})

	_, err := feature.Run(context.Background(), core.Invocation{})
	ce := asCoreError(t, err)
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryComposeInstallPermission {
		t.Fatalf("category = %q", d.Category)
	}
	if len(d.NextActions) == 0 || !strings.Contains(strings.Join(d.NextActions, " "), "sudo") {
		t.Fatalf("permission actions = %v", d.NextActions)
	}
	if len(inst.written) != 0 {
		t.Fatalf("wrote a partial install: %v", inst.written)
	}
}

func TestCancellationDuringDownloadLeavesNoInstall(t *testing.T) {
	asset := machoHeader(machoCpuArm64)
	release, checksums := customRelease(t, "docker-compose-darwin-aarch64", asset)
	dl := &fakeInstallDownloader{release: release, checksums: checksums}
	ctx, cancel := context.WithCancel(context.Background())
	dl.assetErr = context.Canceled
	cancel()
	inst := &fakeInstaller{}
	runner := &probeRunner{results: []execx.Result{missingCompose()}}
	feature, dest := newInstallFeature(t, dl, inst, runner, Platform{"darwin", "arm64"})

	_, err := feature.Run(ctx, core.Invocation{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	ce := asCoreError(t, err)
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryCanceled {
		t.Fatalf("category = %q", d.Category)
	}
	if !strings.Contains(d.Message, "완전한 파일") {
		t.Fatalf("cancellation message = %q", d.Message)
	}
	if len(inst.installs) != 0 {
		t.Fatalf("install ran after cancellation: %v", inst.installs)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("partial plugin exists: %v", statErr)
	}
}

func TestUnsupportedPlatformExplainsManualStepsWithoutNetwork(t *testing.T) {
	dl := &fakeInstallDownloader{}
	inst := &fakeInstaller{}
	runner := &probeRunner{results: []execx.Result{missingCompose()}}
	feature, _ := newInstallFeature(t, dl, inst, runner, Platform{"windows", "amd64"})

	_, err := feature.Plan(context.Background(), core.Invocation{})
	ce := asCoreError(t, err)
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryComposeInstallManual {
		t.Fatalf("category = %q", d.Category)
	}
	joined := strings.Join(d.NextActions, " ")
	if !strings.Contains(joined, "docker-compose") || !strings.Contains(joined, "docker compose version") {
		t.Fatalf("manual steps = %v", d.NextActions)
	}
	if len(dl.gets) != 0 || len(dl.downloads) != 0 {
		t.Fatalf("unsupported platform contacted the network: %v %v", dl.gets, dl.downloads)
	}
}

func TestRecheckFailureReportsInstalledFileButFails(t *testing.T) {
	asset := machoHeader(machoCpuArm64)
	release, checksums := customRelease(t, "docker-compose-darwin-aarch64", asset)
	dl := &fakeInstallDownloader{release: release, checksums: checksums, asset: asset}
	inst := &fakeInstaller{}
	runner := &probeRunner{results: []execx.Result{missingCompose(), missingCompose()}}
	feature, dest := newInstallFeature(t, dl, inst, runner, Platform{"darwin", "arm64"})

	_, err := feature.Run(context.Background(), core.Invocation{})
	ce := asCoreError(t, err)
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryComposeRecheckFailed {
		t.Fatalf("category = %q", d.Category)
	}
	if !strings.Contains(d.Message, "설치되었지만") {
		t.Fatalf("message does not state the file may remain: %q", d.Message)
	}
	if len(inst.written[dest]) == 0 {
		t.Fatal("recheck failure did not install the verified file")
	}
}

// The TUI document declares the install target as a path and the source as a
// URL; the CLI text is unchanged.
func TestComposeInstallDocumentTagsURLAndPath(t *testing.T) {
	feature := &Feature{}
	res := core.Result{Outcome: core.OutcomeSuccess, Data: Payload{Install: Info{
		Changed: true, Version: "v2.39.1",
		Source:      "https://github.com/docker/compose/releases/download/v2.39.1/docker-compose",
		Destination: "/home/me/.docker/cli-plugins/docker-compose",
	}}}
	doc := feature.Document(res)
	if len(doc.Blocks) != 1 || doc.Blocks[0].Kind != core.BlockValueLines {
		t.Fatalf("document = %+v", doc)
	}
	kinds := map[core.ValueKind]int{}
	var text []string
	for _, line := range doc.Blocks[0].Lines {
		text = append(text, line.Text())
		for _, run := range line {
			kinds[run.Kind]++
		}
	}
	if kinds[core.ValueURL] != 1 || kinds[core.ValuePath] != 1 {
		t.Fatalf("kinds = %v, want one url and one path", kinds)
	}
	if got, want := strings.Join(text, "\n"), feature.Present(res, 0); got != want {
		t.Fatalf("document text = %q, want presenter text %q", got, want)
	}
}

func customRelease(t *testing.T, assetName string, asset []byte) ([]byte, []byte) {
	t.Helper()
	var rel releaseResponse
	if err := json.Unmarshal(releaseFixture(t), &rel); err != nil {
		t.Fatal(err)
	}
	for i := range rel.Assets {
		if rel.Assets[i].Name == assetName {
			rel.Assets[i].Size = int64(len(asset))
			rel.Assets[i].Digest = ""
		}
	}
	release, err := json.Marshal(rel)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(asset)
	return release, []byte(hex.EncodeToString(sum[:]) + " *" + assetName + "\n")
}
