package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	"github.com/rootmyself/dcraft/internal/features/status"
)

// Ticket 01 registration. This file is the only glue between the shared
// assembly and the status feature.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(status.New(d.Compose))
	})
}
