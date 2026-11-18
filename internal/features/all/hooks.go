package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	hookfeature "github.com/rootmyself/dcraft/internal/features/hooks"
)

// Ticket 12 registration: explicit host-script registration. The engine owns
// the actual pre/post execution seam; this file only exposes the command.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(hookfeature.New(d.Hooks))
	})
}
