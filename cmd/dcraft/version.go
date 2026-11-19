package main

import (
	"fmt"
	"io"

	"github.com/rootmyself/dcraft/internal/buildinfo"
)

// versionRequested reports whether the process was invoked only for build
// identification. It is handled at process entry, before the application
// object graph exists, so `dcraft --version` works without Docker, Compose, a
// Compose project or a writable state directory.
func versionRequested(args []string) bool {
	if len(args) != 1 {
		return false
	}
	return args[0] == "--version" || args[0] == "-version"
}

func printVersion(w io.Writer) {
	fmt.Fprintln(w, buildinfo.Current().String())
}
