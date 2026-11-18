package all

import (
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	"github.com/rootmyself/dcraft/internal/features/environments"
)

// Environment-alias registration file: the only glue between the shared
// assembly and the environments feature. It adds no central branch.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(environments.New())
	})
}
