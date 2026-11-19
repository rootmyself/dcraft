package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	"github.com/rootmyself/dcraft/internal/features/exec"
)

// Ticket 05 registration: the only glue between the shared assembly and the
// exec feature. The Compose adapter supplies both read-only inspection and the
// live terminal boundary.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(exec.New(d.Compose, d.Compose))
	})
}
