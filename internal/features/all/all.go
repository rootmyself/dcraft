// Package all is the composition root for features. Each feature contributes
// its own registration file to this package. Adding a feature creates a new
// feature package plus one new file here; it never edits an existing file.
package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
)

type registerFunc func(*core.Registry, *deps.Deps)

var registrations []registerFunc

func register(f registerFunc) {
	registrations = append(registrations, f)
}

// Register adds every feature to the registry. It is safe to call with a fresh
// registry and a fresh Deps. The cross-feature contract is checked once here,
// after the last descriptor is known.
func Register(reg *core.Registry, d *deps.Deps) {
	for _, f := range registrations {
		f(reg, d)
	}
	if err := reg.Validate(); err != nil {
		panic(err)
	}
}
