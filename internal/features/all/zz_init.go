package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	initfeature "github.com/rootmyself/dcraft/internal/features/initconfig"
	initimpl "github.com/rootmyself/dcraft/internal/initconfig"
)

// Ticket 05 registration: the only glue between the shared assembly and the
// first-run configuration feature. The filename deliberately sorts after the
// existing feature registrations so the published CLI/TUI menu order of older
// features stays stable; init is added, not wedged into their sequence.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(initfeature.New(initimpl.New(
			initimpl.ComposeValidator{Runner: d.Runner},
			initimpl.FileSaver{},
		)))
	})
}
