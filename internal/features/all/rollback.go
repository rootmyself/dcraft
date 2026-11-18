package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	rollback "github.com/rootmyself/dcraft/internal/features/rollback"
	behavior "github.com/rootmyself/dcraft/internal/rollback"
)

// Ticket 11 registration: the only glue between the shared assembly and the
// previous-good rollback. Update, the record and the secret runtime are
// consumed through their published boundaries and never modified here.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(rollback.New(behavior.New(d.Compose, d.Compose, d.Compose, d.Getenv, d.State, d.Clock)))
	})
}
