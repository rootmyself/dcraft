package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	"github.com/rootmyself/dcraft/internal/features/secrets"
)

// Ticket 09 registration: the only glue between the shared assembly and the
// secrets feature. CLI and TUI derive the command from the same descriptor.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(secrets.New(d.Compose, d.Secrets, d.Compose))
	})
}
