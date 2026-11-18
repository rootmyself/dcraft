package compose

import (
	"context"
	"os"
	"sort"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/secretref"
)

// prepareSecrets loads the project's saved references, validates the services
// against the resolved Compose configuration and asks the secret runtime for
// only the delivery the operation needs. It never resolves a reference for a
// service outside the selected closure.
func (c *Compose) prepareSecrets(ctx context.Context, t core.Target, services []string, mode secretref.Mode) (secretref.Session, error) {
	cat, err := secretref.Load(t.ProjectDir)
	if err != nil {
		return secretref.Session{}, err
	}
	if !cat.Has() {
		if mode == secretref.ModeCreate && c.secrets != nil {
			// A previous configuration may have left a managed override.
			// Removing it keeps the effective inputs equal to the user's files.
			return c.secrets.Prepare(ctx, t, nil, mode)
		}
		return secretref.Session{}, nil
	}
	if c.secrets == nil {
		return secretref.Session{}, secretref.Unavailable()
	}
	cfg, err := c.ConfigInfo(ctx, t)
	if err != nil {
		return secretref.Session{}, err
	}
	if err := secretref.ValidateServices(cat, cfg.Services); err != nil {
		return secretref.Session{}, err
	}
	selected := services
	if len(services) > 0 && mode == secretref.ModeCreate {
		selected = serviceClosure(services, cfg.Dependencies)
	}
	return c.secrets.Prepare(ctx, t, selected, mode)
}

// serviceClosure returns the selected services and their depends_on closure.
// Compose starts dependencies of an explicitly selected service, so their
// runtime references are needed too.
func serviceClosure(selected []string, dependencies map[string][]string) []string {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		for _, dep := range dependencies[name] {
			visit(dep)
		}
	}
	for _, name := range selected {
		visit(name)
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// composeArgsWith appends dcraft-managed override files after the user's
// Compose files. The first file still determines Compose's project directory.
func composeArgsWith(t core.Target, extraFiles []string, sub ...string) []string {
	args := t.ComposeArgs()
	for _, f := range extraFiles {
		args = append(args, "-f", f)
	}
	return append(args, sub...)
}

// composeEnv keeps the process environment and adds the secret placeholders
// only for the Compose subprocess. The values are never written to a file or
// to dcraft state.
func composeEnv(extra []string) []string {
	if len(extra) == 0 {
		return nil
	}
	return append(os.Environ(), extra...)
}
