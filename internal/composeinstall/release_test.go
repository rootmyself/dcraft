package composeinstall

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type fakeDownloader struct {
	get      func(ctx context.Context, url string) ([]byte, error)
	download func(ctx context.Context, url, dest string) error
	gets     []string
	dls      []string
}

func (f *fakeDownloader) Get(ctx context.Context, url string) ([]byte, error) {
	f.gets = append(f.gets, url)
	return f.get(ctx, url)
}

func (f *fakeDownloader) Download(ctx context.Context, url, dest string) error {
	f.dls = append(f.dls, url)
	if f.download == nil {
		return errors.New("no download handler")
	}
	return f.download(ctx, url, dest)
}

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func releaseFixture(t *testing.T) []byte {
	return testdata(t, "release-latest.json")
}

func fakeReleaseDownloader(t *testing.T, release []byte, checksums []byte) *fakeDownloader {
	t.Helper()
	return &fakeDownloader{get: func(ctx context.Context, url string) ([]byte, error) {
		switch {
		case strings.HasSuffix(url, "/releases/latest"):
			return release, nil
		case strings.HasSuffix(url, "/checksums.txt"):
			return checksums, nil
		default:
			return nil, errors.New("unexpected metadata URL " + url)
		}
	}}
}

func TestResolveUsesPublishedLatestReleaseForEachSupportedPlatform(t *testing.T) {
	cases := []struct {
		goos, goarch string
		wantAsset    string
	}{
		{"darwin", "arm64", "docker-compose-darwin-aarch64"},
		{"darwin", "amd64", "docker-compose-darwin-x86_64"},
		{"linux", "arm64", "docker-compose-linux-aarch64"},
		{"linux", "amd64", "docker-compose-linux-x86_64"},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.goarch, func(t *testing.T) {
			downloader := fakeReleaseDownloader(t, releaseFixture(t), testdata(t, "checksums.txt"))
			src := ReleaseSource{Downloader: downloader, APIURL: "https://api.example.test/repos/docker/compose/releases/latest"}
			rel, err := src.Resolve(context.Background(), Platform{GOOS: tc.goos, GOARCH: tc.goarch})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if rel.Version != "v5.8.0" {
				t.Fatalf("version = %q", rel.Version)
			}
			if rel.AssetName != tc.wantAsset {
				t.Fatalf("asset = %q, want %q", rel.AssetName, tc.wantAsset)
			}
			if rel.AssetURL != "https://github.com/docker/compose/releases/download/v5.8.0/"+tc.wantAsset {
				t.Fatalf("asset URL = %q", rel.AssetURL)
			}
			if rel.AssetSize <= 0 {
				t.Fatalf("asset size = %d", rel.AssetSize)
			}
			if len(rel.Checksum) != 64 {
				t.Fatalf("checksum = %q", rel.Checksum)
			}
			if rel.ChecksumsURL != "https://github.com/docker/compose/releases/download/v5.8.0/checksums.txt" {
				t.Fatalf("checksums URL = %q", rel.ChecksumsURL)
			}
		})
	}
}

func TestResolveRefusesUnsupportedPlatformWithoutNetwork(t *testing.T) {
	for _, p := range []Platform{
		{GOOS: "windows", GOARCH: "amd64"},
		{GOOS: "linux", GOARCH: "arm"},
		{GOOS: "darwin", GOARCH: "386"},
		{GOOS: "freebsd", GOARCH: "amd64"},
	} {
		downloader := fakeReleaseDownloader(t, releaseFixture(t), testdata(t, "checksums.txt"))
		src := ReleaseSource{Downloader: downloader, APIURL: "https://api.example.test/releases/latest"}
		_, err := src.Resolve(context.Background(), p)
		ce := asCoreError(t, err)
		if ce.Diagnostics[0].Category != core.CategoryComposeInstallManual {
			t.Fatalf("%v category = %q", p, ce.Diagnostics[0].Category)
		}
		if len(ce.Diagnostics[0].NextActions) == 0 || !strings.Contains(strings.Join(ce.Diagnostics[0].NextActions, " "), "cli-plugins") {
			t.Fatalf("%v manual steps = %v", p, ce.Diagnostics[0].NextActions)
		}
		if len(downloader.gets) != 0 {
			t.Fatalf("%v contacted the network: %v", p, downloader.gets)
		}
	}
}

func TestResolveRefusesWhenChecksumsAreNotPublished(t *testing.T) {
	release := strings.Replace(string(releaseFixture(t)), `"checksums.txt"`, `"something-else.txt"`, 1)
	downloader := fakeReleaseDownloader(t, []byte(release), testdata(t, "checksums.txt"))
	src := ReleaseSource{Downloader: downloader, APIURL: "https://api.example.test/releases/latest"}
	_, err := src.Resolve(context.Background(), Platform{GOOS: "darwin", GOARCH: "arm64"})
	ce := asCoreError(t, err)
	if ce.Diagnostics[0].Category != core.CategoryComposeIntegrityUnavail {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
	if len(downloader.gets) != 1 {
		t.Fatalf("checksums were fetched anyway: %v", downloader.gets)
	}
}

func TestResolveRefusesWhenChecksumEntryIsMissing(t *testing.T) {
	downloader := fakeReleaseDownloader(t, releaseFixture(t), []byte("deadbeef\n"))
	src := ReleaseSource{Downloader: downloader, APIURL: "https://api.example.test/releases/latest"}
	_, err := src.Resolve(context.Background(), Platform{GOOS: "darwin", GOARCH: "arm64"})
	ce := asCoreError(t, err)
	if ce.Diagnostics[0].Category != core.CategoryComposeIntegrityUnavail {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
}

func TestResolveRefusesConflictingIntegrityMetadata(t *testing.T) {
	release := strings.Replace(string(releaseFixture(t)),
		`"sha256:008b8fcf6c6d60340f5796626a0f0d51c042085793a9b1653343011727140b71"`,
		`"sha256:1111111111111111111111111111111111111111111111111111111111111111"`, 1)
	downloader := fakeReleaseDownloader(t, []byte(release), testdata(t, "checksums.txt"))
	src := ReleaseSource{Downloader: downloader, APIURL: "https://api.example.test/releases/latest"}
	_, err := src.Resolve(context.Background(), Platform{GOOS: "darwin", GOARCH: "arm64"})
	ce := asCoreError(t, err)
	if ce.Diagnostics[0].Category != core.CategoryComposeIntegrityUnavail {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
}

func TestResolveFailsCleanlyForBrokenAPIResponses(t *testing.T) {
	cases := map[string]string{
		"invalid json": `{`,
		"prerelease":   `{"tag_name":"v9.0.0-rc.1","prerelease":true,"draft":false,"assets":[]}`,
		"draft":        `{"tag_name":"v9.0.0","prerelease":false,"draft":true,"assets":[]}`,
		"old major":    `{"tag_name":"v1.29.2","prerelease":false,"draft":false,"assets":[]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			downloader := &fakeDownloader{get: func(ctx context.Context, url string) ([]byte, error) {
				return []byte(body), nil
			}}
			src := ReleaseSource{Downloader: downloader, APIURL: "https://api.example.test/releases/latest"}
			_, err := src.Resolve(context.Background(), Platform{GOOS: "darwin", GOARCH: "arm64"})
			ce := asCoreError(t, err)
			if ce.Diagnostics[0].Category != core.CategoryComposeReleaseUnavailable {
				t.Fatalf("category = %q", ce.Diagnostics[0].Category)
			}
		})
	}
}

func TestResolveReportsMetadataNetworkFailure(t *testing.T) {
	downloader := &fakeDownloader{get: func(ctx context.Context, url string) ([]byte, error) {
		return nil, errors.New("dial tcp: network is unreachable")
	}}
	src := ReleaseSource{Downloader: downloader, APIURL: "https://api.example.test/releases/latest"}
	_, err := src.Resolve(context.Background(), Platform{GOOS: "darwin", GOARCH: "arm64"})
	ce := asCoreError(t, err)
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryComposeReleaseUnavailable {
		t.Fatalf("category = %q", d.Category)
	}
	if len(d.NextActions) == 0 {
		t.Fatal("no next action for a network failure")
	}
}

func TestParseChecksumsAcceptsPublishedFormats(t *testing.T) {
	data := []byte(strings.Join([]string{
		"008b8fcf6c6d60340f5796626a0f0d51c042085793a9b1653343011727140b71 *docker-compose-darwin-aarch64",
		"5c41cd573c549dc66abb03c5b29d6d4dfaae206b2e79848d1c32482bb2a3b416  docker-compose-darwin-x86_64",
		"not-a-checksum *broken",
		"",
	}, "\n"))
	sums := parseChecksums(data)
	if sums["docker-compose-darwin-aarch64"] != "008b8fcf6c6d60340f5796626a0f0d51c042085793a9b1653343011727140b71" {
		t.Fatalf("star format not parsed: %v", sums)
	}
	if sums["docker-compose-darwin-x86_64"] != "5c41cd573c549dc66abb03c5b29d6d4dfaae206b2e79848d1c32482bb2a3b416" {
		t.Fatalf("space format not parsed: %v", sums)
	}
	if _, ok := sums["broken"]; ok {
		t.Fatalf("invalid checksum was accepted: %v", sums)
	}
}

func asCoreError(t *testing.T, err error) *core.Error {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not *core.Error", err)
	}
	if len(ce.Diagnostics) == 0 {
		t.Fatalf("error %v has no diagnostics", err)
	}
	return ce
}
