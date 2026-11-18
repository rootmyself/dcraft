// Package identity resolves the effective Compose project name of a target
// without changing anything. It shares the automatic-name map with the
// lifecycle controller so read-only features (status, later logs/exec) identify
// the same stack as up/stop/down/restart.
package identity

import (
	"context"
	"errors"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

// Reader implements core.IdentityReader.
type Reader struct {
	identity stack.Identity
	names    *state.Store
}

func New(identity stack.Identity, names *state.Store) *Reader {
	return &Reader{identity: identity, names: names}
}

// ReadProjectName returns the effective project name for a target, in the same
// priority order as the lifecycle controller:
//
//  1. an explicit -p input;
//  2. a name declared by the Compose configuration;
//  3. a recorded automatic allocation.
//
// Otherwise it returns "", which keeps Compose's folder default exactly as
// before this lookup existed. It never creates state, locks a stack or writes.
func (r *Reader) ReadProjectName(ctx context.Context, target core.Target) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if name := strings.TrimSpace(target.ProjectName); name != "" {
		return name, nil
	}
	if r.identity == nil || r.names == nil {
		return "", errors.New("identity: name resolver is not configured")
	}
	config, err := r.identity.ConfigInfo(ctx, target)
	if err != nil {
		return "", err
	}
	derived, err := r.identity.DerivedProjectName(ctx, target)
	if err != nil {
		return "", err
	}
	if config.EffectiveName != "" && config.EffectiveName != derived {
		return config.EffectiveName, nil
	}
	allocation, ok, err := r.names.LookupName(target)
	if err != nil {
		return "", err
	}
	if ok {
		return allocation.Name, nil
	}
	return "", nil
}
