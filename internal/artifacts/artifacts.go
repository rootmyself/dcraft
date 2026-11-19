// Package artifacts is dcraft's narrow boundary for fetching and installing
// external release artifacts. Business rules (release resolution, integrity
// verification, platform validation) live in the owning feature; this package
// only performs network reads and atomic file placement, so both can be
// replaced by deterministic fakes in tests.
package artifacts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrExists is returned when the destination already exists. dcraft never
// replaces an existing tool installation.
var ErrExists = errors.New("destination already exists")

// Downloader fetches HTTP(S) content. Get is for small metadata; Download
// streams an asset to a local file.
type Downloader interface {
	Get(ctx context.Context, url string) ([]byte, error)
	Download(ctx context.Context, url, dest string) error
}

// Installer places a verified artifact at its destination.
type Installer interface {
	// Exists reports whether path exists, including a symlink.
	Exists(path string) (bool, error)
	// InstallAtomic copies src to dest through a temporary file in the same
	// directory, applies mode, and makes the file visible with one atomic
	// operation. It returns ErrExists instead of replacing dest.
	InstallAtomic(src, dest string, mode fs.FileMode) error
}

const (
	userAgent      = "dcraft"
	maxMetadata    = 4 << 20
	downloadPerm   = 0o600
	pluginDirPerm  = 0o755
	tempFilePrefix = ".dcraft-install-"
	httpTimeout    = 120 * time.Second
)

// HTTPDownloader is the production Downloader.
type HTTPDownloader struct {
	// Client overrides the default client in tests. A nil Client uses a
	// client with a bounded timeout; the context still governs cancellation.
	Client *http.Client
}

func (d HTTPDownloader) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{Timeout: httpTimeout}
}

func (d HTTPDownloader) request(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := d.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}
	return resp, nil
}

func (d HTTPDownloader) Get(ctx context.Context, url string) ([]byte, error) {
	resp, err := d.request(ctx, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadata+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxMetadata {
		return nil, fmt.Errorf("GET %s: metadata larger than %d bytes", url, maxMetadata)
	}
	return body, nil
}

func (d HTTPDownloader) Download(ctx context.Context, url, dest string) error {
	resp, err := d.request(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, downloadPerm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return closeErr
	}
	return nil
}

// OSInstaller is the production Installer.
type OSInstaller struct{}

func (OSInstaller) Exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (OSInstaller) InstallAtomic(src, dest string, mode fs.FileMode) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, pluginDirPerm); err != nil {
		return err
	}
	if exists, err := (OSInstaller{}).Exists(dest); err != nil {
		return err
	} else if exists {
		return ErrExists
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	tmp, err := os.CreateTemp(dir, tempFilePrefix+"*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}

	// A hard link makes the finished file visible atomically and fails if the
	// destination appeared in the meantime, so an existing Compose (or a
	// concurrent dcraft) is never replaced. Filesystems without hard links
	// fail safely here instead of falling back to an overwriting rename.
	if err := os.Link(tmpName, dest); err != nil {
		cleanup()
		if errors.Is(err, fs.ErrExist) {
			return ErrExists
		}
		return fmt.Errorf("publish %s: %w", filepath.Base(dest), err)
	}
	cleanup()
	return nil
}

// IsPermissionError reports whether an install error is a filesystem
// permission problem. The install feature maps it to its own Korean
// diagnostic while other causes stay install failures.
func IsPermissionError(err error) bool {
	return errors.Is(err, fs.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "permission denied")
}
