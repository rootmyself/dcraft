package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	gitgenfeature "github.com/rootmyself/dcraft/internal/features/gitgen"
)

// Ticket 13 registration: the guided Git host-script generator. The hooks
// command and the engine own registration and script trust; this file only
// exposes the generator command.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(gitgenfeature.New())
	})
}
