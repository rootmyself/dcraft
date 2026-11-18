package core

import "context"

// Resolver turns a TargetRequest into a fully qualified Target. The working
// directory is passed explicitly so the seam is testable without changing the
// process directory.
type Resolver interface {
	Resolve(ctx context.Context, req TargetRequest, cwd string) (Target, []Diagnostic, error)
}
