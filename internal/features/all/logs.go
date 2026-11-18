package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	"github.com/rootmyself/dcraft/internal/features/logs"
)

// Ticket 05 registration: the only glue between the shared assembly and the
// logs feature. The Compose adapter supplies both read-only inspection and the
// live-output boundary.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(logs.New(d.Compose, d.Compose))
	})
}
