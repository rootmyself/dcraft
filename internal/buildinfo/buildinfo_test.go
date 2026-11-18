package buildinfo_test

import (
	"runtime"
	"testing"

	"github.com/rootmyself/dcraft/internal/buildinfo"
)

// The source defaults are the development identification used when no
// ldflags are supplied. These literals are the contract, not a copy of the
// implementation.
func TestDefaultsAreDevelopmentValues(t *testing.T) {
	info := buildinfo.Current()
	if info.Version != "dev" {
		t.Fatalf("Version = %q, want %q", info.Version, "dev")
	}
	if info.Commit != "none" {
		t.Fatalf("Commit = %q, want %q", info.Commit, "none")
	}
	if info.BuildDate != "unknown" {
		t.Fatalf("BuildDate = %q, want %q", info.BuildDate, "unknown")
	}
	if info.GoVersion != runtime.Version() {
		t.Fatalf("GoVersion = %q, want %q", info.GoVersion, runtime.Version())
	}
	if info.GOOS != runtime.GOOS || info.GOARCH != runtime.GOARCH {
		t.Fatalf("platform = %s/%s, want %s/%s", info.GOOS, info.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
}

func TestStringIsTheSingleIdentificationLine(t *testing.T) {
	info := buildinfo.Info{
		Version:   "1.2.3",
		Commit:    "abc1234",
		BuildDate: "2026-11-18T00:00:00Z",
		GoVersion: "go1.27.4",
		GOOS:      "linux",
		GOARCH:    "amd64",
	}
	want := "dcraft 1.2.3 (commit abc1234, built 2026-11-18T00:00:00Z, linux/amd64, go1.27.4)"
	if got := info.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
