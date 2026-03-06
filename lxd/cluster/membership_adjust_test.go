package cluster

import (
	"testing"

	"github.com/canonical/go-dqlite/v3/app"
	"github.com/canonical/go-dqlite/v3/client"
	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/lxd/db"
)

// TestAdjustRoles_InactiveMatchesGeneric verifies that when control-plane
// mode is inactive, the LXD-specific adjust logic matches go-dqlite's default.
func TestAdjustRoles_InactiveMatchesGeneric(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftSpare}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
	}

	memberRoles := map[string][]db.ClusterRole{
		"10.0.0.1:8443": {db.ClusterRoleControlPlane},
		"10.0.0.2:8443": {db.ClusterRoleControlPlane},
	}

	expectedRoles := testRolesChanges(nodes, connectivity, 3, 0)
	expectedRole, expectedCandidates := expectedRoles.Adjust(1)

	roles := testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates := adjustRoles(roles, 1, nodes, connectivity, memberRoles)

	assert.Equal(t, expectedRole, role)
	assert.Equal(t, testNodeIDs(expectedCandidates), testNodeIDs(candidates))
}

// TestAdjustRoles_ActiveSkipsNonControlPlanePromotion verifies that when
// control-plane mode is active, non-control-plane promotion candidates are ignored.
func TestAdjustRoles_ActiveSkipsNonControlPlanePromotion(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftSpare}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
	}

	memberRoles := map[string][]db.ClusterRole{
		"10.0.0.1:8443": {db.ClusterRoleControlPlane},
		"10.0.0.2:8443": {db.ClusterRoleControlPlane},
		"10.0.0.4:8443": {db.ClusterRoleControlPlane},
	}

	roles := testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates := adjustRoles(roles, 1, nodes, connectivity, memberRoles)

	assert.Equal(t, client.NodeRole(-1), role)
	assert.Empty(t, candidates)
}

// TestAdjustRoles_ActiveDemotesNonControlPlaneVoterWhenReplacementExists
// verifies that non-control-plane voters are demoted when an eligible
// control-plane replacement can be promoted safely.
func TestAdjustRoles_ActiveDemotesNonControlPlaneVoterWhenReplacementExists(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 4, Address: "10.0.0.4:8443", Role: db.RaftSpare}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
		"10.0.0.4:8443": true,
	}

	memberRoles := map[string][]db.ClusterRole{
		"10.0.0.1:8443": {db.ClusterRoleControlPlane},
		"10.0.0.3:8443": {db.ClusterRoleControlPlane},
		"10.0.0.4:8443": {db.ClusterRoleControlPlane},
	}

	roles := testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates := adjustRoles(roles, 1, nodes, connectivity, memberRoles)

	assert.Equal(t, db.RaftStandBy, role)
	assert.Equal(t, []uint64{2}, testNodeIDs(candidates))
}

// TestAdjustRoles_ActiveSkipsVoterDemotionWithoutControlPlaneReplacement
// verifies that voter demotion is skipped when no eligible control-plane
// replacement exists.
func TestAdjustRoles_ActiveSkipsVoterDemotionWithoutControlPlaneReplacement(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 4, Address: "10.0.0.4:8443", Role: db.RaftSpare}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
		"10.0.0.4:8443": true,
	}

	memberRoles := map[string][]db.ClusterRole{
		"10.0.0.1:8443": {db.ClusterRoleControlPlane},
		"10.0.0.3:8443": {db.ClusterRoleControlPlane},
		"10.0.0.5:8443": {db.ClusterRoleControlPlane},
	}

	roles := testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates := adjustRoles(roles, 1, nodes, connectivity, memberRoles)

	assert.Equal(t, client.NodeRole(-1), role)
	assert.Empty(t, candidates)
}

// TestAdjustRoles_ActiveDemotesNonControlPlaneStandby verifies that
// non-control-plane standbys are demoted to spare when control-plane mode is active.
func TestAdjustRoles_ActiveDemotesNonControlPlaneStandby(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 4, Address: "10.0.0.4:8443", Role: db.RaftStandBy}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
		"10.0.0.4:8443": true,
	}

	memberRoles := map[string][]db.ClusterRole{
		"10.0.0.1:8443": {db.ClusterRoleControlPlane},
		"10.0.0.2:8443": {db.ClusterRoleControlPlane},
		"10.0.0.3:8443": {db.ClusterRoleControlPlane},
	}

	roles := testRolesChanges(nodes, connectivity, 3, 1)
	role, candidates := adjustRoles(roles, 1, nodes, connectivity, memberRoles)

	assert.Equal(t, db.RaftSpare, role)
	assert.Equal(t, []uint64{4}, testNodeIDs(candidates))
}

// TestAdjustRoles_ActiveInterleavesDemotionsAndPromotions verifies the
// activation transition used by integration tests: non-control-plane voters are
// demoted, then control-plane spares are promoted before the next demotion.
func TestAdjustRoles_ActiveInterleavesDemotionsAndPromotions(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 4, Address: "10.0.0.4:8443", Role: db.RaftSpare}},
		{NodeInfo: client.NodeInfo{ID: 5, Address: "10.0.0.5:8443", Role: db.RaftSpare}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
		"10.0.0.4:8443": true,
		"10.0.0.5:8443": true,
	}

	memberRoles := map[string][]db.ClusterRole{
		"10.0.0.1:8443": {db.ClusterRoleControlPlane},
		"10.0.0.4:8443": {db.ClusterRoleControlPlane},
		"10.0.0.5:8443": {db.ClusterRoleControlPlane},
	}

	// Step 1: demote non-control-plane voter.
	roles := testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates := adjustRoles(roles, 1, nodes, connectivity, memberRoles)
	assert.Equal(t, db.RaftStandBy, role)
	assert.Equal(t, []uint64{2}, testNodeIDs(candidates))

	// Apply change and evaluate next step.
	nodes[1].Role = db.RaftStandBy
	roles = testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates = adjustRoles(roles, 1, nodes, connectivity, memberRoles)

	// Step 2: promote an eligible control-plane spare to refill voters.
	assert.Equal(t, db.RaftVoter, role)
	assert.NotEmpty(t, candidates)
	assert.Contains(t, []string{"10.0.0.4:8443", "10.0.0.5:8443"}, candidates[0].Address)
}

// TestAdjustRoles_InactiveAllowsNonControlPlanePromotion verifies that
// below the control-plane activation threshold, non-control-plane members can
// still be promoted to database roles.
func TestAdjustRoles_InactiveAllowsNonControlPlanePromotion(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftSpare}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
	}

	// Only two control-plane members: mode inactive.
	memberRoles := map[string][]db.ClusterRole{
		"10.0.0.1:8443": {db.ClusterRoleControlPlane},
		"10.0.0.2:8443": {db.ClusterRoleControlPlane},
	}

	roles := testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates := adjustRoles(roles, 1, nodes, connectivity, memberRoles)

	assert.Equal(t, db.RaftVoter, role)
	assert.Equal(t, []uint64{3}, testNodeIDs(candidates))
}

// testRolesChanges creates a RolesChanges test fixture using raft nodes and connectivity.
func testRolesChanges(nodes []db.RaftNode, connectivity map[string]bool, voters int, standBys int) *app.RolesChanges {
	state := make(map[client.NodeInfo]*client.NodeMetadata, len(nodes))
	for i, node := range nodes {
		if connectivity[node.Address] {
			state[node.NodeInfo] = &client.NodeMetadata{
				FailureDomain: uint64(i + 1),
				Weight:        uint64(i + 1),
			}

			continue
		}

		state[node.NodeInfo] = nil
	}

	return &app.RolesChanges{
		Config: app.RolesConfig{
			Voters:   voters,
			StandBys: standBys,
		},
		State: state,
	}
}

// testNodeIDs extracts node IDs from a node list to simplify candidate assertions.
func testNodeIDs(nodes []client.NodeInfo) []uint64 {
	ids := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}

	return ids
}
