package core

import (
	"context"
	"strings"
)

// IdentityReader resolves the effective Compose project name of a target using
// the same local identity as stack-changing features. Implementations must be
// strictly read-only: they may read per-computer identity state, but they must
// not create files or directories, take the stack change lock, or write state.
// An empty name means "no local identity; let Compose resolve its default".
type IdentityReader interface {
	ReadProjectName(ctx context.Context, target Target) (string, error)
}

// resolveReadOnlyIdentity applies the recorded project name for read-only and
// query paths. It is the single identity-resolution rule those paths share:
// stack-changing features never call it and resolve their name through the
// lifecycle allocation path instead. The helper only reads; it never allocates,
// claims or writes a name.
//
// An unreadable name map is a warning and leaves the target without a recorded
// name, so read-only output never invents an identity. Only cancellation is an
// error.
func (e *Engine) resolveReadOnlyIdentity(ctx context.Context, target *Target) (*Diagnostic, error) {
	if e == nil || e.Identity == nil || target == nil || strings.TrimSpace(target.ProjectName) != "" {
		return nil, nil
	}
	name, err := e.Identity.ReadProjectName(ctx, *target)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		warning := Diagnostic{
			Category:    CategoryIdentityUnreadable,
			Severity:    SeverityWarning,
			Message:     msgIdentityUnreadable,
			Detail:      errDetail(err),
			NextActions: []string{msgIdentityUnreadableActionOne, msgIdentityUnreadableActionTwo},
		}
		return &warning, nil
	}
	if name != "" {
		target.ProjectName = name
	}
	return nil, nil
}
