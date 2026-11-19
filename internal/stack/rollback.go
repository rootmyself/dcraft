package stack

import (
	"context"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/secretref"
)

// RollbackPin is one recorded service image the previous-good rollback must
// restore. ImageID is the authoritative immutable identity (the full
// sha256:... ID recorded by update). RetentionTag is the dcraft-owned tag that
// keeps resolving to that image after its container was replaced; it is part of
// the preflight and the human report, never a substitute for ImageID.
type RollbackPin struct {
	Service      string `json:"service"`
	ImageID      string `json:"image_id"`
	RetentionTag string `json:"retention_tag"`
}

// Rollbacker is the narrow write boundary for the previous-good rollback
// published by ticket 11 (issue #12). The ordinary Updater is not reused for
// the apply: rollback must pin every recorded service to an exact image ID
// through a dcraft-managed override and must never pull or build, while update
// deliberately refreshes image references and builds local services.
//
// The Compose adapter implements it. Services are Compose service names; the
// caller passes exactly the recorded services and the exact secret references
// stored in the record. The implementation never substitutes the project's
// current configuration for those references, and it keeps materialized files
// on a partial apply because a later restart may still need them.
type Rollbacker interface {
	ApplyRollback(ctx context.Context, target core.Target, services []string, pins []RollbackPin, refs []secretref.Reference) (OpResult, error)
}
