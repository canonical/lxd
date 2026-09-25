package engine

import (
	"context"
	"net/http"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/placement/internal/models"
	"github.com/canonical/lxd/shared/api"
)

// FilterStage narrows a candidate cluster member list using whatever models.PlacementContext
// fields its own algorithm needs, or returns an error explaining why no candidates remain.
type FilterStage func(ctx context.Context, tx *db.ClusterTx, pctx *models.PlacementContext, candidates []db.NodeInfo) ([]db.NodeInfo, error)

// PlacementEngine narrows a candidate cluster member list by applying a sequence of FilterStages
// using ctx/tx/models.PlacementContext.
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

// Apply runs f against the engine's current candidates.
func (e *PlacementEngine) Apply(f FilterStage) *PlacementEngine {
	if e.err != nil {
		// Skip f if an earlier Apply call already failed.
		return e
	}

	e.candidates, e.err = f(e.ctx, e.tx, e.pctx, e.candidates)
	return e
}

// Result returns the first candidate left by the last applied FilterStage, so a chain ending in
// an ordering stage yields that ordering's top pick. It returns the first error returned by any
// applied FilterStage, or NotFound when no candidates remain.
func (e *PlacementEngine) Result() (*db.NodeInfo, error) {
	if e.err != nil {
		return nil, e.err
	}

	if len(e.candidates) == 0 {
		return nil, api.StatusErrorf(http.StatusNotFound, "No suitable cluster member could be found")
	}

	return &e.candidates[0], nil
}
