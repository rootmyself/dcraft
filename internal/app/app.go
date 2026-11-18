// Package app wires the object graph once for both interfaces: command runner,
// state, Compose adapter, feature registry, engine and autocomplete.
package app

import (
	"os"

	"github.com/rootmyself/dcraft/internal/artifacts"
	"github.com/rootmyself/dcraft/internal/compose"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/features/all"
	"github.com/rootmyself/dcraft/internal/hooks"
	"github.com/rootmyself/dcraft/internal/identity"
	"github.com/rootmyself/dcraft/internal/lifecycle"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/state"
	"github.com/rootmyself/dcraft/internal/target"
)

// Options are the process-level dependencies. Tests substitute runners,
// environment lookups, clocks and directories.
type Options struct {
	Getenv     func(string) string
	Getwd      func() (string, error)
	Runner     execx.Runner
	Clock      core.Clock
	Downloader artifacts.Downloader
	Installer  artifacts.Installer
}

// App is the built object graph.
type App struct {
	Options  Options
	Registry *core.Registry
	Engine   *core.Engine
	Deps     *deps.Deps
}

// New builds the application. State is created lazily on first history write;
// no Docker command runs here.
func New(opts Options) (*App, error) {
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Getwd == nil {
		opts.Getwd = os.Getwd
	}
	if opts.Runner == nil {
		opts.Runner = execx.NewOSRunner()
	}
	if opts.Clock == nil {
		opts.Clock = core.SystemClock{}
	}
	if opts.Downloader == nil {
		opts.Downloader = artifacts.HTTPDownloader{}
	}
	if opts.Installer == nil {
		opts.Installer = artifacts.OSInstaller{}
	}

	stateRoot, err := state.Root(opts.Getenv)
	if err != nil {
		return nil, err
	}
	store := state.New(stateRoot)
	secretRuntime := secretref.NewRuntime(store, opts.Getenv)
	hookService := hooks.New(store, opts.Getenv)
	composeAdapter := compose.New(opts.Runner, opts.Getenv)
	composeAdapter.SetSecretRuntime(secretRuntime)
	controller := lifecycle.New(composeAdapter, composeAdapter, composeAdapter, store, opts.Clock)
	d := &deps.Deps{
		Runner:     opts.Runner,
		Getenv:     opts.Getenv,
		Clock:      opts.Clock,
		State:      store,
		Compose:    composeAdapter,
		Lifecycle:  controller,
		Secrets:    secretRuntime,
		Hooks:      hookService,
		Downloader: opts.Downloader,
		Installer:  opts.Installer,
	}

	registry := core.NewRegistry()
	all.Register(registry, d)

	engine := core.NewEngine(registry, target.New(), composeAdapter, store, opts.Clock)
	engine.Identity = identity.New(composeAdapter, store)
	engine.Hooks = hookService
	engine.CWD = opts.Getwd

	return &App{Options: opts, Registry: registry, Engine: engine, Deps: d}, nil
}
