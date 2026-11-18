package all

import (
	"os"
	"runtime"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/buildinfo"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
	"github.com/rootmyself/dcraft/internal/deps"
	restorefeature "github.com/rootmyself/dcraft/internal/features/restore"
	restoreimpl "github.com/rootmyself/dcraft/internal/restore"
)

// Ticket 18 registration: the only glue between the shared assembly and the
// safe data replacement feature. The backup format comes from internal/backup;
// the published safe-replace contract lives in internal/restore.
func init() {
	register(func(reg *core.Registry, d *deps.Deps) {
		// The Compose adapter implements the published restore capability.
		// A differently wired backend registers the feature but it reports a
		// clear internal diagnostic instead of panicking at startup.
		backend, _ := d.Data.(data.VolumeRestoreBackend)
		reg.MustRegister(restorefeature.New(restoreimpl.New(restoreimpl.Config{
			Backend: backend,
			Names:   d.Identity,
			Locks:   d.Locks,
			Clock:   d.Clock,
			Tool: backup.Tool{
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
