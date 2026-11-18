package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	"github.com/rootmyself/dcraft/internal/features/restart"
)

// Ticket 03 registration: the only glue between the shared assembly and restart.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(restart.New(d.Lifecycle))
	})
}
