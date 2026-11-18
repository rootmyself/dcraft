// Package composeinstall installs an official Docker Compose CLI plugin into a
// user-writable Docker CLI plugin directory. It never installs Docker itself,
// never uses sudo, never replaces an existing Compose, and never falls back to
// the standalone docker-compose binary.
package composeinstall

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/rootmyself/dcraft/internal/artifacts"
	"github.com/rootmyself/dcraft/internal/core"
)

// DefaultReleaseAPI is the official GitHub API endpoint for the latest stable
// Docker Compose release. Releases marked draft or prerelease are ignored.
const DefaultReleaseAPI = "https://api.github.com/repos/docker/compose/releases/latest"

// Platform identifies the operating system and architecture dcraft runs on.
// WSL2 reports linux, which is one of the supported targets.
type Platform struct {
	GOOS   string
	GOARCH string
}

// Release is one official Compose artifact plus the integrity metadata
// published with it.
type Release struct {
	// Version is the published tag (for example "v5.8.0").
	Version string
	// AssetName is the official artifact file name.
	AssetName string
	// AssetURL is the official download URL from the release metadata.
	AssetURL string
	// AssetSize is the size published with the asset, used to detect a
	// truncated download.
	AssetSize int64
	// Checksum is the SHA-256 hex digest published for AssetName.
	Checksum string
	// ChecksumsURL is the published checksum file the digest came from.
	ChecksumsURL string
}

// ReleaseSource resolves the latest official release at install time. It never
// pins a version: the caller learns the current tag from this call.
type ReleaseSource struct {
	Downloader artifacts.Downloader
	// APIURL overrides the release API. Empty uses DefaultReleaseAPI. The
	// production app only sets it from a documented test/enterprise override.
	APIURL string
	// PluginDir is the display/install directory used in manual guidance.
	// Empty means the default user plugin directory.
	PluginDir string
}

func (s ReleaseSource) pluginDir() string {
	if dir := strings.TrimSpace(s.PluginDir); dir != "" {
		return dir
	}
	return defaultPluginDir()
}

type releaseResponse struct {
	TagName    string         `json:"tag_name"`
	Prerelease bool           `json:"prerelease"`
	Draft      bool           `json:"draft"`
	Assets     []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

var versionRe = regexp.MustCompile(`^v?([0-9]+)(\.[0-9]+)*`)

// Resolve fetches the latest stable official release and its published
// checksum. It refuses when the source does not publish verifiable integrity
// metadata for the platform asset.
func (s ReleaseSource) Resolve(ctx context.Context, p Platform) (Release, error) {
	assetName, err := platformAsset(p, s.pluginDir())
	if err != nil {
		return Release{}, err
	}
	if s.Downloader == nil {
		return Release{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryComposeReleaseUnavailable,
			Severity:    core.SeverityError,
			Message:     msgReleaseUnavailable,
			NextActions: manualInstallActions(s.pluginDir()),
		})
	}
	apiURL := strings.TrimSpace(s.APIURL)
	if apiURL == "" {
		apiURL = DefaultReleaseAPI
	}
	data, getErr := s.Downloader.Get(ctx, apiURL)
	if getErr != nil {
		if ctx.Err() != nil {
			return Release{}, ctx.Err()
		}
		return Release{}, core.FailCause(getErr, core.Diagnostic{
			Category:    core.CategoryComposeReleaseUnavailable,
			Severity:    core.SeverityError,
			Message:     msgReleaseUnavailable,
			Detail:      getErr.Error(),
			NextActions: manualInstallActions(s.pluginDir()),
		})
	}
	var release releaseResponse
	if err := json.Unmarshal(data, &release); err != nil {
		return Release{}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryComposeReleaseUnavailable,
			Severity:    core.SeverityError,
			Message:     msgReleaseUnavailable,
			Detail:      "release metadata is not valid JSON",
			NextActions: manualInstallActions(s.pluginDir()),
		})
	}
	if release.Draft || release.Prerelease {
		return Release{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryComposeReleaseUnavailable,
			Severity:    core.SeverityError,
			Message:     msgReleaseUnavailable,
			Detail:      "latest release is a draft or prerelease",
			NextActions: manualInstallActions(s.pluginDir()),
		})
	}
	if major, ok := releaseMajor(release.TagName); !ok || major < 2 {
		return Release{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryComposeReleaseUnavailable,
			Severity:    core.SeverityError,
			Message:     msgReleaseUnavailable,
			Detail:      "unexpected release tag " + release.TagName,
			NextActions: manualInstallActions(s.pluginDir()),
		})
	}

	var asset, checksums *releaseAsset
	for i := range release.Assets {
		switch release.Assets[i].Name {
		case assetName:
			asset = &release.Assets[i]
		case "checksums.txt":
			checksums = &release.Assets[i]
		}
	}
	if asset == nil || strings.TrimSpace(asset.BrowserDownloadURL) == "" {
		return Release{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryComposeInstallManual,
			Severity:    core.SeverityError,
			Message:     msgReleaseAssetMissing,
			Detail:      assetName,
			NextActions: manualInstallActions(s.pluginDir()),
		})
	}
	if checksums == nil || strings.TrimSpace(checksums.BrowserDownloadURL) == "" {
		return Release{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryComposeIntegrityUnavail,
			Severity:    core.SeverityError,
			Message:     msgIntegrityUnavailable,
			Detail:      "release does not publish checksums.txt",
			NextActions: manualInstallActions(s.pluginDir()),
		})
	}
	checksumData, sumErr := s.Downloader.Get(ctx, checksums.BrowserDownloadURL)
	if sumErr != nil {
		if ctx.Err() != nil {
			return Release{}, ctx.Err()
		}
		return Release{}, core.FailCause(sumErr, core.Diagnostic{
			Category:    core.CategoryComposeIntegrityUnavail,
			Severity:    core.SeverityError,
			Message:     msgIntegrityUnavailable,
			Detail:      sumErr.Error(),
			NextActions: manualInstallActions(s.pluginDir()),
		})
	}
	sums := parseChecksums(checksumData)
	expected, ok := sums[assetName]
	if !ok {
		return Release{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryComposeIntegrityUnavail,
			Severity:    core.SeverityError,
			Message:     msgIntegrityUnavailable,
			Detail:      "checksums.txt has no entry for " + assetName,
			NextActions: manualInstallActions(s.pluginDir()),
		})
	}
	// GitHub also publishes a per-asset digest. When both values exist they
	// must agree; conflicting metadata is not a safe install.
	if digest := strings.TrimSpace(asset.Digest); strings.HasPrefix(digest, "sha256:") {
		if !strings.EqualFold(strings.TrimPrefix(digest, "sha256:"), expected) {
			return Release{}, core.Fail(core.Diagnostic{
				Category:    core.CategoryComposeIntegrityUnavail,
				Severity:    core.SeverityError,
				Message:     msgIntegrityConflicting,
				Detail:      assetName,
				NextActions: manualInstallActions(s.pluginDir()),
			})
		}
	}
	return Release{
		Version:      release.TagName,
		AssetName:    assetName,
		AssetURL:     asset.BrowserDownloadURL,
		AssetSize:    asset.Size,
		Checksum:     expected,
		ChecksumsURL: checksums.BrowserDownloadURL,
	}, nil
}

// platformAsset maps a Go platform to Docker's published asset naming. Only
// platforms dcraft can verify end to end are automated; everything else gets
// exact manual steps instead of a risky attempt.
func platformAsset(p Platform, pluginDir string) (string, error) {
	arch := ""
	switch p.GOARCH {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	switch p.GOOS {
	case "darwin", "linux":
	default:
		arch = ""
	}
	if arch == "" {
		return "", core.Fail(core.Diagnostic{
			Category:    core.CategoryComposeInstallManual,
			Severity:    core.SeverityError,
			Message:     msgPlatformUnsupported,
			Detail:      p.GOOS + "/" + p.GOARCH,
			NextActions: manualInstallActions(pluginDir),
		})
	}
	return "docker-compose-" + p.GOOS + "-" + arch, nil
}

func releaseMajor(tag string) (int, bool) {
	m := versionRe.FindStringSubmatch(strings.TrimSpace(tag))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseChecksums accepts the published format `<sha256> *<name>` and the
// common `<sha256>  <name>` variant. Malformed lines are ignored; the caller
// refuses when the required asset has no valid entry.
func parseChecksums(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		sum := strings.ToLower(fields[0])
		if len(sum) != 64 || !isHex(sum) {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == "" || strings.ContainsAny(name, `/\`) {
			continue
		}
		out[name] = sum
	}
	return out
}

func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
