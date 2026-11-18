// Package deps supplies the shared services a feature registration may use.
// Adding a new shared service is a contract change handled by the integration
// owner; a feature that only needs these services adds files, never edits the
// shared assembly.
package deps

import (
	"github.com/rootmyself/dcraft/internal/compose"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/lifecycle"
	"github.com/rootmyself/dcraft/internal/secretref"
)

// Deps is built once by the application and handed to every registration.
type Deps struct {
	Runner  execx.Runner
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
}
