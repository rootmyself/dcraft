package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	update "github.com/rootmyself/dcraft/internal/features/update"
	behavior "github.com/rootmyself/dcraft/internal/update"
)

// Ticket 07 registration: the only glue between the shared assembly and the
// explicit update operation.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(update.New(behavior.New(d.Compose, d.Compose, d.State, d.Clock)))
	})
}
