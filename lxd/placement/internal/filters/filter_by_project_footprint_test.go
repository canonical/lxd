package filters

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/placement/internal/engine"
	"github.com/canonical/lxd/lxd/placement/internal/models"
)

// applyProjectFootprintStage runs FilterByProjectFootprint directly, via a one-stage engine —
// mirroring the slice PlaceInstance's project-footprint stage produces before its own terminal
// least-loaded pick narrows further.
func applyProjectFootprintStage(ctx context.Context, tx *db.ClusterTx, candidates []db.NodeInfo, projectName string, maxHosts int, excludeNodeID *int64) ([]db.NodeInfo, error) {
	pctx := &models.PlacementContext{ProjectName: projectName, ProjectMaxHosts: maxHosts, ExcludeNodeID: excludeNodeID}

	return engine.New(ctx, tx, pctx, candidates).Apply(FilterByProjectFootprint).Result()
}

type projectFootprintSuite struct {
	suite.Suite
}

func TestProjectFootprintSuite(t *testing.T) {
	suite.Run(t, new(projectFootprintSuite))
}

// TestRestrictToProjectFootprint covers limits.max_hosts: the project-wide footprint counts every
// instance in the project regardless of placement group membership, growing below the cap and
// forcing reuse (load-balanced, not narrowed to a single host) at or above it, always strict
// regardless of what rigor a caller might otherwise apply.
func (s *projectFootprintSuite) TestRestrictToProjectFootprint() {
	testCluster, cleanup := db.NewTestCluster(s.T())
	defer cleanup()

	nodeNames := []string{"member01", "member02", "member03", "member04"}
	candidates := make([]db.NodeInfo, 0, len(nodeNames))
	for i, name := range nodeNames {
		candidates = append(candidates, db.NodeInfo{Name: name, Address: fmt.Sprintf("192.0.2.%d", i)})
	}

	err := testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		for i, node := range candidates {
			id, err := tx.CreateNode(node.Name, node.Address)
			s.Require().NoError(err)
			candidates[i].ID = id
		}

		return nil
	})
	s.Require().NoError(err)

	candidatesOnly := func(names ...string) []db.NodeInfo {
		filtered := make([]db.NodeInfo, 0, len(names))
		for _, c := range candidates {
			for _, name := range names {
				if c.Name == name {
					filtered = append(filtered, c)
				}
			}
		}

		return filtered
	}

	placeInstance := func(name, node string, grouped bool) {
		_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
				Name:    name,
				Node:    node,
				Project: "default",
				Type:    instancetype.Container,
			})
			s.Require().NoError(err)

			if grouped {
				err = cluster.CreateInstanceConfig(ctx, tx.Tx(), instanceID, map[string]string{
					"placement.group": "some-other-group",
				})
				s.Require().NoError(err)
			}

			return nil
		})
	}

	restrict := func(maxHosts int) ([]db.NodeInfo, error) {
		var got []db.NodeInfo
		err := testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			got, err = applyProjectFootprintStage(ctx, tx, candidates, "default", maxHosts, nil)
			return err
		})

		return got, err
	}

	// One instance with no placement group at all, one instance in an unrelated placement
	// group -- the project-wide footprint counts both, since it's independent of grouping.
	placeInstance("c1", "member01", false)
	placeInstance("c2", "member02", true)

	// Below the cap: grow, restricted to the two still-unused hosts.
	got, err := restrict(3)
	s.Require().NoError(err)
	s.ElementsMatch(candidatesOnly("member03", "member04"), got)

	// A third instance brings the footprint to exactly the cap: forced reuse of the existing
	// set (all 3), load-balanced -- not narrowed to whichever host happens to have the most
	// instances.
	placeInstance("c3", "member03", false)
	got, err = restrict(3)
	s.Require().NoError(err)
	s.ElementsMatch(candidatesOnly("member01", "member02", "member03"), got)

	// Strict enforcement: with no unused host and the cap already reached, an even lower cap
	// still restricts to the existing set rather than failing outright (there IS a live,
	// already-used candidate to reuse) -- this exercises the same ceiling regime, not project
	// enforcement being "extra" strict in some different way.
	got, err = restrict(1)
	s.Require().NoError(err)
	s.ElementsMatch(candidatesOnly("member01", "member02", "member03"), got)
}

// TestRestrictToProjectFootprintExcludesNode covers the evacuation exclusion parameter: a
// candidate's own current host is excluded from the "used" accounting, mirroring PlaceInstance's
// evacuation semantics (placement decisions reason about where instances will be, not where they
// currently are).
func (s *projectFootprintSuite) TestRestrictToProjectFootprintExcludesNode() {
	testCluster, cleanup := db.NewTestCluster(s.T())
	defer cleanup()

	var member01ID, member02ID int64
	err := testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		member01ID, err = tx.CreateNode("member01", "192.0.2.10")
		s.Require().NoError(err)
		member02ID, err = tx.CreateNode("member02", "192.0.2.11")
		s.Require().NoError(err)

		_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
			Name:    "c1",
			Node:    "member01",
			Project: "default",
			Type:    instancetype.Container,
		})
		s.Require().NoError(err)

		return nil
	})
	s.Require().NoError(err)

	candidates := []db.NodeInfo{
		{ID: member01ID, Name: "member01"},
		{ID: member02ID, Name: "member02"},
	}

	err = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		got, err := applyProjectFootprintStage(ctx, tx, candidates, "default", 1, &member01ID)
		s.Require().NoError(err)
		// With member01 excluded, the project's footprint is empty -- both candidates count
		// as "unused," so growth (not forced reuse) applies.
		s.ElementsMatch(candidates, got)
		return nil
	})
	s.Require().NoError(err)
}

// TestRestrictToProjectFootprintStrictAlways confirms enforcement never falls back to a
// permissive-style widen, regardless of what a caller might otherwise want -- there is no rigor
// parameter at all, only api.PlacementRigorStrict is ever used internally.
func (s *projectFootprintSuite) TestRestrictToProjectFootprintStrictAlways() {
	testCluster, cleanup := db.NewTestCluster(s.T())
	defer cleanup()

	err := testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateNode("member01", "192.0.2.20")
		s.Require().NoError(err)

		_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
			Name:    "c1",
			Node:    "member01",
			Project: "default",
			Type:    instancetype.Container,
		})
		s.Require().NoError(err)

		return nil
	})
	s.Require().NoError(err)

	// No live candidates at all (member01 is the only used host, and it's not in the candidate
	// list) -- strict enforcement means this fails rather than silently allowing an unbounded
	// placement.
	err = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := applyProjectFootprintStage(ctx, tx, nil, "default", 1, nil)
		s.Require().ErrorIs(err, errProjectFootprintExceeded)
		return nil
	})
	s.Require().NoError(err)
}
