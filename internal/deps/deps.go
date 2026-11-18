// Package deps supplies the shared services a feature registration may use.
// Adding a new shared service is a contract change handled by the integration
// owner; a feature that only needs these services adds files, never edits the
// shared assembly.
package deps

import (
	"github.com/rootmyself/dcraft/internal/artifacts"
	"github.com/rootmyself/dcraft/internal/compose"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
)

// Deps is built once by the application and handed to every registration.
type Deps struct {
	Runner  execx.Runner
	Getenv  func(string) string
	Clock   core.Clock
	State   core.StateStore
	Compose *compose.Compose
	// Downloader and Installer are the narrow external-artifact boundary
	// used by the Compose install feature. Tests replace them with fakes.
	Downloader artifacts.Downloader
	Installer  artifacts.Installer
}
