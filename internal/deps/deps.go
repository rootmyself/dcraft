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
	"github.com/rootmyself/dcraft/internal/hooks"
	"github.com/rootmyself/dcraft/internal/identity"
	"github.com/rootmyself/dcraft/internal/lifecycle"
	"github.com/rootmyself/dcraft/internal/secretref"
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
	// Secrets owns project secret references and runtime materialization. It
	// is published by ticket 09 and shared by the secrets feature and the
	// Compose adapter.
	Secrets *secretref.Runtime
	// Hooks owns explicit host-script registration, the machine-local trust
	// record and execution. It is published by ticket 12 for the engine and
	// the hooks command; generated-script features (#14/#15) consume it.
	Hooks *hooks.Service
	// Downloader and Installer are the narrow external-artifact boundary
	// used by the Compose install feature. Tests replace them with fakes.
	Downloader artifacts.Downloader
	Installer  artifacts.Installer
	// Data is the narrow volume/bind data-plane boundary published by ticket
	// #16. Identity is the read-only effective-name lookup. Locks is the
	// narrow per-stack change lock capability shared by stack-changing
	// features (backup/restore/rollback); no feature type-asserts the
	// concrete store.
	Data     data.Backend
	Identity *identity.Reader
	Locks    state.Locker
}
