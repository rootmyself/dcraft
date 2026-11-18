package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	s3genfeature "github.com/rootmyself/dcraft/internal/features/s3gen"
)

// Ticket 14 registration: the guided S3 host-script generator. The hooks
// command and the engine own registration and script trust; this file only
// exposes the generator command.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(s3genfeature.New())
	})
}
