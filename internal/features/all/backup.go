package all

import (
	"os"
	"runtime"

	backupimpl "github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/buildinfo"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	backupfeature "github.com/rootmyself/dcraft/internal/features/backup"
)

// Ticket 16 registration: the only glue between the shared assembly and the
// backup feature. The archive format and the data-source contract live in
// internal/backup and internal/data.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		reg.MustRegister(backupfeature.New(backupimpl.New(backupimpl.Config{
			Backend: d.Data,
			Names:   d.Identity,
			Locks:   d.Locks,
			Clock:   d.Clock,
			Tool: backupimpl.Tool{
				Version:   buildinfo.Version,
				Commit:    buildinfo.Commit,
				GoVersion: runtime.Version(),
				OS:        runtime.GOOS,
				Arch:      runtime.GOARCH,
			},
			PID: os.Getpid(),
		})))
	})
}
