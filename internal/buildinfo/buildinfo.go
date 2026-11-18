// Package buildinfo carries the release identification embedded in a dcraft
// binary. Version, Commit and BuildDate are set with -ldflags -X at build
// time; the source defaults identify a development build. Platform and Go
// toolchain are compiled in by the runtime, so every artifact reports the
// target it was actually built for.
package buildinfo

import (
	"fmt"
	"runtime"
)

// Injected by scripts/build.sh:
//
//	-X github.com/rootmyself/dcraft/internal/buildinfo.Version=...
//	-X github.com/rootmyself/dcraft/internal/buildinfo.Commit=...
//	-X github.com/rootmyself/dcraft/internal/buildinfo.BuildDate=...
//
// Keep the defaults stable: a plain `go build` must stay identifiable as a
// development build rather than claiming a release version.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// Info is the identification reported by `dcraft --version` and written to
// the distribution metadata file.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

// Current returns the identification of the running binary.
func Current() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}
}

// String renders one line suitable for `--version` output. Machine-readable
// distribution metadata is the build-info.json file written by the build
// script, not this human line.
func (i Info) String() string {
	return fmt.Sprintf(
		"dcraft %s (commit %s, built %s, %s/%s, %s)",
		i.Version, i.Commit, i.BuildDate, i.GOOS, i.GOARCH, i.GoVersion,
	)
}
