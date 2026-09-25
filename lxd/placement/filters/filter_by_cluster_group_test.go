package filters_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/placement/filters"
	"github.com/canonical/lxd/lxd/placement/internal/models"
	"github.com/canonical/lxd/shared/api"
)

func TestFilterByClusterGroup(t *testing.T) {
	inG1 := db.NodeInfo{ID: 1, Name: "member01", Groups: []string{"g1"}}
	inG1AndG2 := db.NodeInfo{ID: 2, Name: "member02", Groups: []string{"g1", "g2"}}
	inG2 := db.NodeInfo{ID: 3, Name: "member03", Groups: []string{"g2"}}
	candidates := []db.NodeInfo{inG1, inG1AndG2, inG2}

	tests := []struct {
		name       string
		pctx       models.PlacementContext
		candidates []db.NodeInfo
		want       []db.NodeInfo
		wantErr    bool
	}{
		{
			name:       "no cluster group: pass-through",
			pctx:       models.PlacementContext{},
			candidates: candidates,
			want:       candidates,
		},
		{
			name:       "placement group set: cluster group ignored",
			pctx:       models.PlacementContext{ClusterGroupName: "g2", PlacementGroup: api.PlacementGroup{Name: "pg1"}},
			candidates: candidates,
			want:       candidates,
		},
		{
			name:       "keeps only members of the group",
			pctx:       models.PlacementContext{ClusterGroupName: "g1"},
			candidates: candidates,
			want:       []db.NodeInfo{inG1, inG1AndG2},
		},
		{
			name:       "no member of the group among candidates",
			pctx:       models.PlacementContext{ClusterGroupName: "g3"},
			candidates: candidates,
			wantErr:    true,
		},
		{
			name:       "empty candidates: empty result, not an error",
			pctx:       models.PlacementContext{ClusterGroupName: "g1"},
			candidates: nil,
			want:       []db.NodeInfo{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := filters.FilterByClusterGroup(context.Background(), nil, &tt.pctx, tt.candidates)
			if tt.wantErr {
				var noCandidates *filters.NoCandidatesError
				require.ErrorAs(t, err, &noCandidates)
				require.Equal(t, "FilterByClusterGroup", noCandidates.Func)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
