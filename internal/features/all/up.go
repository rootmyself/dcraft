package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	"github.com/rootmyself/dcraft/internal/features/up"
)

// Ticket 03 registration: the only glue between the shared assembly and up.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(up.New(d.Lifecycle))
	})
}
