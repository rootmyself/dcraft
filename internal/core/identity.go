package core

import "context"

// IdentityReader resolves the effective Compose project name of a target using
// the same local identity as stack-changing features. Implementations must be
// strictly read-only: they may read per-computer identity state, but they must
// not create files or directories, take the stack change lock, or write state.
// An empty name means "no local identity; let Compose resolve its default".
type IdentityReader interface {
	ReadProjectName(ctx context.Context, target Target) (string, error)
}
