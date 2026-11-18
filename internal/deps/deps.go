// Package deps supplies the shared services a feature registration may use.
// Adding a new shared service is a contract change handled by the integration
// owner; a feature that only needs these services adds files, never edits the
// shared assembly.
package deps

import (
	"github.com/rootmyself/dcraft/internal/artifacts"
	"github.com/rootmyself/dcraft/internal/compose"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/identity"
	"github.com/rootmyself/dcraft/internal/lifecycle"
	"github.com/rootmyself/dcraft/internal/state"
)

// Deps is built once by the application and handed to every registration.
type Deps struct {
	Runner  execx.Runner
	Getenv  func(string) string
	Clock   core.Clock
	State   core.StateStore
	Compose *compose.Compose
	// Lifecycle executes and coordinates the four stack lifecycle commands.
	// It is published by ticket 03 and is the only write-capable stack
	// service in this version.
	Lifecycle *lifecycle.Controller
	// Downloader and Installer are the narrow external-artifact boundary
	// used by the Compose install feature. Tests replace them with fakes.
	Downloader artifacts.Downloader
	Installer  artifacts.Installer
	// Data is the narrow volume/bind data-plane boundary published by ticket
	// #16. Identity is the read-only effective-name lookup; Store is the
	// published state implementation used for the per-stack lock.
	Data     data.Backend
	Identity *identity.Reader
	Store    *state.Store
}
