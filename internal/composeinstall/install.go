package composeinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rootmyself/dcraft/internal/artifacts"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
)

const (
	featureName = "install-compose"
	pluginMode  = 0o755
)

// Config is the feature's narrowly scoped dependency set. Tests replace the
// downloader, installer, runner and platform; production wires the real ones.
type Config struct {
	Runner     execx.Runner
	Getenv     func(string) string
	Downloader artifacts.Downloader
	Installer  artifacts.Installer
	// ReleaseAPIURL overrides the official latest-release endpoint. Empty uses
	// the official GitHub API. The app sets it only from the documented test
	// override.
	ReleaseAPIURL string
	GOOS          string
	GOARCH        string
}

// Feature installs a missing Docker Compose CLI plugin after approval.
type Feature struct {
	runner     execx.Runner
	getenv     func(string) string
	downloader artifacts.Downloader
	installer  artifacts.Installer
	releases   ReleaseSource
	platform   Platform
}

// New builds the feature.
func New(cfg Config) *Feature {
	if cfg.Getenv == nil {
		cfg.Getenv = func(string) string { return "" }
	}
	if cfg.Downloader == nil {
		cfg.Downloader = artifacts.HTTPDownloader{}
	}
	if cfg.Installer == nil {
		cfg.Installer = artifacts.OSInstaller{}
	}
	goos := cfg.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := cfg.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return &Feature{
		runner:     cfg.Runner,
		getenv:     cfg.Getenv,
		downloader: cfg.Downloader,
		installer:  cfg.Installer,
		releases: ReleaseSource{
			Downloader: cfg.Downloader,
			APIURL:     cfg.ReleaseAPIURL,
		},
		platform: Platform{GOOS: goos, GOARCH: goarch},
	}
}

// Info is the machine payload of a successful install result. It contains no
// secret values and separates what changed from where it came from.
type Info struct {
	Changed          bool   `json:"changed"`
	AlreadyAvailable bool   `json:"already_available"`
	Version          string `json:"version,omitempty"`
	Source           string `json:"source,omitempty"`
	Destination      string `json:"destination,omitempty"`
	ChecksumVerified bool   `json:"checksum_verified"`
	SizeBytes        int64  `json:"size_bytes,omitempty"`
	RecheckOutput    string `json:"recheck_output,omitempty"`
}

// Payload is the data of a successful result.
type Payload struct {
	Install Info `json:"install"`
}

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:    featureName,
		Title:   msgTitle,
		Summary: msgSummary,
		Usage:   msgUsage,
		// Installing Compose is a separate risk from running a stack.
		Approval: core.ApprovalInstall,
		// It exists precisely because Compose is missing, so it needs only the
		// Docker CLI and must work in a folder without Compose files.
		Requires:       core.Requirements{core.RequirementDockerCLI},
		TargetOptional: true,
	}
}

// Plan implements core.Planner: it shows the concrete source, version,
// destination, permission need and effect without downloading or installing.
func (f *Feature) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	return core.RunStageValue(in.Reporter, "plan", msgStagePlan, func() (core.Plan, error) {
		p, err := f.prepare(ctx)
		if err != nil {
			return core.Plan{}, err
		}
		return core.Plan{Text: p.text}, nil
	})
}

// prepared is the read-only install decision shared by Plan and Run.
type prepared struct {
	dest             string
	release          Release
	alreadyAvailable bool
	composeOutput    string
	text             string
}

// Run implements core.Feature. It installs only after the engine has accepted
// the install approval, and re-checks the Compose function afterwards.
func (f *Feature) Run(ctx context.Context, in core.Invocation) (any, error) {
	p, err := core.RunStageValue(in.Reporter, "plan", msgStagePlan, func() (prepared, error) {
		return f.prepare(ctx)
	})
	if err != nil {
		return nil, err
	}
	if p.alreadyAvailable {
		return Payload{Install: Info{
			AlreadyAvailable: true,
			Destination:      p.dest,
			RecheckOutput:    p.composeOutput,
		}}, nil
	}

	tmpDir, err := os.MkdirTemp("", "dcraft-compose-install-*")
	if err != nil {
		return nil, installFailure(err, "")
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	assetPath := filepath.Join(tmpDir, "docker-compose")

	err = core.RunStage(in.Reporter, "download", msgStageDownload, func() error {
		if f.downloader == nil {
			return core.Fail(core.Diagnostic{
				Category:    core.CategoryComposeDownloadFailed,
				Severity:    core.SeverityError,
				Message:     msgDownloadFailed,
				Detail:      "downloader is not configured",
				NextActions: downloadActions(),
			})
		}
		if err := f.downloader.Download(ctx, p.release.AssetURL, assetPath); err != nil {
			return downloadFailure(ctx, err)
		}
		info, statErr := os.Stat(assetPath)
		if statErr != nil {
			return downloadFailure(ctx, statErr)
		}
		if p.release.AssetSize > 0 && info.Size() != p.release.AssetSize {
			return downloadFailure(ctx, fmt.Errorf("downloaded %d bytes, published size is %d", info.Size(), p.release.AssetSize))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = core.RunStage(in.Reporter, "verify", msgStageVerify, func() error {
		if ctx.Err() != nil {
			return canceledInstall(ctx)
		}
		sum, sumErr := sha256File(assetPath)
		if sumErr != nil {
			return checksumFailure(sumErr, "")
		}
		if !strings.EqualFold(sum, p.release.Checksum) {
			return checksumFailure(nil, "sha256 "+sum+" does not match published "+p.release.Checksum)
		}
		return validateExecutable(assetPath, f.platform)
	})
	if err != nil {
		return nil, err
	}

	err = core.RunStage(in.Reporter, "install", msgStageInstall, func() error {
		if ctx.Err() != nil {
			return canceledInstall(ctx)
		}
		exists, existErr := f.installer.Exists(p.dest)
		if existErr != nil {
			return installFailure(existErr, filepath.Dir(p.dest))
		}
		if exists {
			return existsFailure(p.dest)
		}
		if installErr := f.installer.InstallAtomic(assetPath, p.dest, pluginMode); installErr != nil {
			if errors.Is(installErr, artifacts.ErrExists) {
				return existsFailure(p.dest)
			}
			return installFailure(installErr, filepath.Dir(p.dest))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var recheck string
	err = core.RunStage(in.Reporter, "recheck", msgStageRecheck, func() error {
		output, ok, probeErr := f.probeCompose(ctx)
		if probeErr != nil {
			if ctx.Err() != nil {
				return canceledInstall(ctx)
			}
			return recheckFailure(probeErr.Error())
		}
		if !ok {
			return recheckFailure(output)
		}
		recheck = output
		return nil
	})
	if err != nil {
		return nil, err
	}

	return Payload{Install: Info{
		Changed:          true,
		Version:          p.release.Version,
		Source:           p.release.AssetURL,
		Destination:      p.dest,
		ChecksumVerified: true,
		SizeBytes:        p.release.AssetSize,
		RecheckOutput:    recheck,
	}}, nil
}

// prepare resolves the destination, checks the current Compose state and
// resolves the official release. It changes nothing.
func (f *Feature) prepare(ctx context.Context) (prepared, error) {
	dest, err := pluginDestination(f.getenv)
	if err != nil {
		return prepared{}, err
	}

	output, available, probeErr := f.probeCompose(ctx)
	if probeErr != nil {
		if ctx.Err() != nil {
			return prepared{}, canceledInstall(ctx)
		}
		return prepared{}, core.FailCause(probeErr, core.Diagnostic{
			Category: core.CategoryDockerMissing,
			Severity: core.SeverityError,
			Message:  msgNoDockerCLI,
			Detail:   probeErr.Error(),
			NextActions: []string{
				"docker --version이 성공하는지 확인하고 새 터미널에서 다시 실행하세요.",
				"dcraft는 Docker 자체를 설치하거나 교체하지 않습니다.",
			},
		})
	}
	if available {
		text := strings.Join([]string{
			msgPlanTitle,
			"- " + msgPlanVersion + ": " + output,
			"- " + msgPlanTarget + ": " + dest,
			"- " + msgPlanNoChange,
		}, "\n")
		return prepared{
			dest:             dest,
			alreadyAvailable: true,
			composeOutput:    output,
			text:             text,
		}, nil
	}

	exists, existErr := f.installer.Exists(dest)
	if existErr != nil {
		return prepared{}, installFailure(existErr, filepath.Dir(dest))
	}
	if exists {
		return prepared{}, existsFailure(dest)
	}

	releases := f.releases
	releases.PluginDir = filepath.Dir(dest)
	rel, resolveErr := releases.Resolve(ctx, f.platform)
	if resolveErr != nil {
		if ctx.Err() != nil {
			return prepared{}, canceledInstall(ctx)
		}
		return prepared{}, resolveErr
	}
	return prepared{
		dest:    dest,
		release: rel,
		text:    installPlan(rel, dest),
	}, nil
}

// probeCompose reports whether docker compose runs and its full version text.
// A start failure is returned as an error; an execution failure is simply
// "not available" because the install itself never needs the engine.
func (f *Feature) probeCompose(ctx context.Context) (string, bool, error) {
	if f.runner == nil {
		return "", false, errors.New("docker runner is not configured")
	}
	res, err := f.runner.Run(ctx, execx.Command{Name: "docker", Args: []string{"compose", "version"}})
	if err != nil {
		return "", false, err
	}
	if !res.Succeeded() {
		return res.Combined(), false, nil
	}
	output := strings.TrimSpace(string(res.Stdout))
	if output == "" {
		output = strings.TrimSpace(res.Combined())
	}
	return output, true, nil
}

func installPlan(rel Release, dest string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", msgPlanTitle)
	fmt.Fprintf(&b, "- %s: %s\n", msgPlanSourceLabel, rel.AssetURL)
	fmt.Fprintf(&b, "- %s: %s\n", msgPlanVersion, rel.Version)
	fmt.Fprintf(&b, "- %s: %s\n", msgPlanTarget, dest)
	fmt.Fprintf(&b, "- %s: %s\n", msgPlanPermission, msgPermissionUserArea)
	fmt.Fprintf(&b, "- %s: %s\n", msgPlanEffect, msgEffectInstall)
	fmt.Fprintf(&b, "- %s: %s", msgPlanIntegrity, msgIntegritySHA256)
	return b.String()
}

// Present implements core.Presenter for a successful install.
func (f *Feature) Present(res core.Result, width int) string {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return msgPlanTitle
	}
	info := payload.Install
	var b strings.Builder
	if info.AlreadyAvailable {
		fmt.Fprintf(&b, "%s\n", msgAlreadyAvailable)
		fmt.Fprintf(&b, "- %s: %s\n", msgPlanTarget, info.Destination)
		if info.RecheckOutput != "" {
			fmt.Fprintf(&b, "- %s: %s\n", msgPlanVersion, info.RecheckOutput)
		}
	} else {
		fmt.Fprintf(&b, "%s\n", msgStatusInstalled)
		fmt.Fprintf(&b, "- %s: %s\n", msgPlanVersion, info.Version)
		fmt.Fprintf(&b, "- %s: %s\n", msgPlanTarget, info.Destination)
		fmt.Fprintf(&b, "- %s: %s\n", msgPlanSourceLabel, info.Source)
		if info.ChecksumVerified {
			fmt.Fprintf(&b, "- 무결성: SHA-256 확인됨\n")
		}
		if info.RecheckOutput != "" {
			fmt.Fprintf(&b, "- 재확인: %s\n", info.RecheckOutput)
		}
	}
	b.WriteString("\n")
	b.WriteString(msgGuidanceOne)
	b.WriteString("\n")
	b.WriteString(msgGuidanceTwo)
	return strings.TrimRight(b.String(), "\n")
}

func payloadFrom(data any) (Payload, bool) {
	switch v := data.(type) {
	case Payload:
		return v, true
	case *Payload:
		if v != nil {
			return *v, true
		}
	}
	return Payload{}, false
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func downloadActions() []string {
	return []string{
		"네트워크 연결과 GitHub 접근 가능 여부를 확인한 뒤 다시 시도하세요.",
		"실패한 다운로드의 임시 파일은 정리되며 Compose 플러그인은 설치되지 않습니다.",
		"계속 실패하면 공식 릴리스 페이지에서 직접 내려받아 수동으로 설치하세요.",
	}
}

func downloadFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return canceledInstall(ctx)
	}
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryComposeDownloadFailed,
		Severity:    core.SeverityError,
		Message:     msgDownloadFailed,
		Detail:      err.Error(),
		NextActions: downloadActions(),
	})
}

func checksumFailure(cause error, detail string) error {
	return core.FailCause(cause, core.Diagnostic{
		Category: core.CategoryComposeIntegrityMismatch,
		Severity: core.SeverityError,
		Message:  msgChecksumMismatch,
		Detail:   detail,
		NextActions: []string{
			"다시 시도하고, 같은 오류가 반복되면 네트워크 프록시나 공식 릴리스 페이지를 확인하세요.",
			"dcraft는 체크섬이 맞지 않는 파일을 설치하지 않았습니다.",
			"임시 다운로드 파일은 정리되었습니다.",
		},
	})
}

// canceledInstall reports exactly what the atomic install does and does not
// leave behind. It is used instead of the generic cancellation text so the
// user can tell a completed install from one that never reached the target.
func canceledInstall(ctx context.Context) error {
	return core.FailCause(ctx.Err(), core.Diagnostic{
		Category: core.CategoryCanceled,
		Severity: core.SeverityWarning,
		Message:  msgInstallCanceled,
		NextActions: []string{
			msgInstallCanceledActionOne,
			msgInstallCanceledActionTwo,
			"dcraft status로 Compose 사용 가능 여부를 확인하세요.",
		},
	})
}

func existsFailure(dest string) error {
	return core.Fail(core.Diagnostic{
		Category: core.CategoryComposeInstallExists,
		Severity: core.SeverityError,
		Message:  msgInstallExists,
		Detail:   dest,
		NextActions: []string{
			"docker compose version으로 현재 설치를 확인하세요.",
			"기존 Compose를 직접 업데이트하거나 제거한 뒤 다시 실행하세요. dcraft는 기존 설치를 덮어쓰지 않습니다.",
			"dcraft는 과거 독립 실행형 docker-compose로 대체하지 않습니다.",
		},
	})
}

func installFailure(err error, dir string) error {
	if err == nil {
		err = errors.New("install failed")
	}
	if dir == "" {
		dir = defaultPluginDir()
	}
	if errors.Is(err, fs.ErrPermission) || artifacts.IsPermissionError(err) {
		return core.FailCause(err, core.Diagnostic{
			Category: core.CategoryComposeInstallPermission,
			Severity: core.SeverityError,
			Message:  msgInstallPermission,
			Detail:   err.Error(),
			NextActions: []string{
				"설치 대상 폴더의 쓰기 권한을 확인하세요: " + dir,
				"dcraft는 관리자 권한(sudo)을 요청하지 않습니다. 권한을 직접 조정한 뒤 다시 실행하세요.",
				"변경된 내용은 없습니다. 임시 파일은 정리됩니다.",
			},
		})
	}
	return core.FailCause(err, core.Diagnostic{
		Category: core.CategoryComposeInstallFailed,
		Severity: core.SeverityError,
		Message:  msgInstallFailed,
		Detail:   err.Error(),
		NextActions: []string{
			"대상 폴더의 상태와 여유 공간을 확인한 뒤 다시 시도하세요.",
			"dcraft는 부분 설치 파일을 남기지 않도록 원자적으로 설치합니다.",
			"계속 실패하면 공식 릴리스 페이지의 수동 설치 안내를 따르세요.",
		},
	})
}

func recheckFailure(detail string) error {
	return core.Fail(core.Diagnostic{
		Category: core.CategoryComposeRecheckFailed,
		Severity: core.SeverityError,
		Message:  msgRecheckFailed,
		Detail:   detail,
		NextActions: []string{
			"docker compose version을 직접 실행해 출력과 원인을 확인하세요.",
			"DOCKER_CONFIG와 cli-plugins 경로가 일치하는지 확인하세요.",
			"플러그인 파일은 설치되어 있을 수 있습니다. 임의로 삭제하기 전에 경로와 버전을 확인하세요.",
		},
	})
}
