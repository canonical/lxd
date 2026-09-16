package engine

import (
	"context"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/placement/internal/models"
)

// FilterStage narrows a candidate cluster member list using whatever models.PlacementContext
// fields its own algorithm needs, or returns an error explaining why no candidates remain. A
// FilterStage is a plain function — it never touches a PlacementEngine — and owns its complete,
// final error output (a PlacementEngine never adds its own wrapping, so wrapping added by one
// FilterStage is never duplicated by another).
type FilterStage func(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error)

// PlacementEngine narrows a candidate cluster member list by applying a sequence of FilterStage
// values against one fixed ctx/tx/models.PlacementContext, fluently:
// New(ctx, tx, pctx, candidates).Apply(f1).Apply(f2).Result().
//
// pctx is held by reference: a caller may keep mutating the same *models.PlacementContext
// between Apply calls (e.g. setting the fields for a later stage only once its own inputs are
// known), and each Apply call reads it at the time that FilterStage actually runs, not at
// New time.
type PlacementEngine struct {
	ctx        context.Context
	tx         *db.ClusterTx
	pctx       *models.PlacementContext
	candidates []db.NodeInfo
	err        error
}

// New starts a placement decision over the given candidates.
func New(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) *PlacementEngine {
	return &PlacementEngine{ctx: ctx, tx: tx, pctx: pctx, candidates: candidates}
}

// Apply runs f against the engine's current candidates, unless an earlier Apply call already
// failed — in which case f is skipped entirely and the earlier error carries forward unchanged.
// This is the only place the chain's short-circuit-on-error check exists: individual FilterStage
// values never perform it themselves, so a new FilterStage can never accidentally skip it.
func (e *PlacementEngine) Apply(f FilterStage) *PlacementEngine {
	if e.err != nil {
		return e
	}

	e.candidates, e.err = f(e.ctx, e.tx, e.pctx, e.candidates)
	return e
}

// Result returns the fully-narrowed candidates, or the first error returned by any applied
// FilterStage.
func (e *PlacementEngine) Result() ([]db.NodeInfo, error) {
	return e.candidates, e.err
}
