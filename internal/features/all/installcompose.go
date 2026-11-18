package all

import (
	"github.com/rootmyself/dcraft/internal/composeinstall"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
)

// Ticket 06 registration. This file is the only glue between the shared
// assembly and the Compose install feature.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		getenv := d.Getenv
		if getenv == nil {
			getenv = func(string) string { return "" }
		}
		reg.MustRegister(composeinstall.New(composeinstall.Config{
			Runner:        d.Runner,
			Getenv:        getenv,
			Downloader:    d.Downloader,
			Installer:     d.Installer,
			ReleaseAPIURL: getenv("DCRAFT_COMPOSE_RELEASE_API"),
		}))
	})
}
