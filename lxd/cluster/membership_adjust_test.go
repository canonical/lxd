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
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, memberRoles, nil)

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
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, memberRoles, nil)

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
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, memberRoles, nil)

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
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, memberRoles, nil)

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
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, memberRoles, nil)

	assert.Equal(t, db.RaftSpare, role)
	assert.Equal(t, []uint64{4}, testNodeIDs(candidates))
}

// TestAdjustRoles_ActiveDemotesNonControlPlaneStandbyBelowTarget verifies that
// when control-plane mode is active, a non-control-plane standby is demoted even
// when the standby count is below the configured target and no control-plane spare
// is available to fill the gap.
func TestAdjustRoles_ActiveDemotesNonControlPlaneStandbyBelowTarget(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 4, Address: "10.0.0.4:8443", Role: db.RaftStandBy}},
		{NodeInfo: client.NodeInfo{ID: 5, Address: "10.0.0.5:8443", Role: db.RaftSpare}},
		{NodeInfo: client.NodeInfo{ID: 6, Address: "10.0.0.6:8443", Role: db.RaftSpare}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
		"10.0.0.4:8443": true,
		"10.0.0.5:8443": true,
		"10.0.0.6:8443": true,
	}

	// All control-plane members are already voters; no control-plane spare exists.
	// Member 4 is a non-control-plane standby, members 5 and 6 are non-control-plane spares.
	memberRoles := map[string][]db.ClusterRole{
		"10.0.0.1:8443": {db.ClusterRoleControlPlane},
		"10.0.0.2:8443": {db.ClusterRoleControlPlane},
		"10.0.0.3:8443": {db.ClusterRoleControlPlane},
	}

	// Target is 2 standbys but only 1 exists (non-control-plane). Because no eligible
	// control-plane spare can fill the gap, the non-control-plane standby must still be demoted.
	roles := testRolesChanges(nodes, connectivity, 3, 2)
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, memberRoles, nil)

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
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, memberRoles, nil)
	assert.Equal(t, db.RaftStandBy, role)
	assert.Equal(t, []uint64{2}, testNodeIDs(candidates))

	// Apply change and evaluate next step.
	nodes[1].Role = db.RaftStandBy
	roles = testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates, _ = rolesAdjust(roles, 1, nodes, connectivity, memberRoles, nil)

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
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, memberRoles, nil)

	assert.Equal(t, db.RaftVoter, role)
	assert.Equal(t, []uint64{3}, testNodeIDs(candidates))
}

// testRolesChanges creates a RolesChanges test fixture using raft nodes and connectivity.
func testRolesChanges(nodes []db.RaftNode, connectivity map[string]bool, voters int, standbys int) *app.RolesChanges {
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
			StandBys: standbys,
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

func TestPrioritizeEvacuated(t *testing.T) {
	nodes := []client.NodeInfo{
		{ID: 1, Address: "10.0.0.1:8443"},
		{ID: 2, Address: "10.0.0.2:8443"},
		{ID: 3, Address: "10.0.0.3:8443"},
		{ID: 4, Address: "10.0.0.4:8443"},
	}

	evacuated := []string{"10.0.0.2:8443", "10.0.0.4:8443"}

	result := prioritizeEvacuated(nodes, evacuated)
	assert.Equal(t, []uint64{2, 4, 1, 3}, testNodeIDs(result), "evacuated members should be ordered first")
}

func TestPrioritizeEvacuated_NoEvacuated(t *testing.T) {
	nodes := []client.NodeInfo{
		{ID: 1, Address: "10.0.0.1:8443"},
		{ID: 2, Address: "10.0.0.2:8443"},
	}

	result := prioritizeEvacuated(nodes, nil)
	assert.Equal(t, []uint64{1, 2}, testNodeIDs(result), "order should be unchanged when no members are evacuated")
}

func TestPrioritizeEvacuated_AllEvacuated(t *testing.T) {
	nodes := []client.NodeInfo{
		{ID: 1, Address: "10.0.0.1:8443"},
		{ID: 2, Address: "10.0.0.2:8443"},
	}

	evacuated := []string{"10.0.0.1:8443", "10.0.0.2:8443"}

	result := prioritizeEvacuated(nodes, evacuated)
	assert.Equal(t, []uint64{1, 2}, testNodeIDs(result), "order should be unchanged when all members are evacuated")
}

func TestPrioritizeNonControlPlane(t *testing.T) {
	nodes := []client.NodeInfo{
		{ID: 1, Address: "10.0.0.1:8443"},
		{ID: 2, Address: "10.0.0.2:8443"},
		{ID: 3, Address: "10.0.0.3:8443"},
		{ID: 4, Address: "10.0.0.4:8443"},
	}

	memberRoles := map[string][]db.ClusterRole{
		"10.0.0.1:8443": {db.ClusterRoleControlPlane},
		"10.0.0.3:8443": {db.ClusterRoleControlPlane},
	}

	result := prioritizeNonControlPlane(nodes, memberRoles)
	assert.Equal(t, []uint64{2, 4, 1, 3}, testNodeIDs(result), "non-control-plane members should be ordered first")
}

func TestPrioritizeNonControlPlane_NoControlPlane(t *testing.T) {
	nodes := []client.NodeInfo{
		{ID: 1, Address: "10.0.0.1:8443"},
		{ID: 2, Address: "10.0.0.2:8443"},
	}

	result := prioritizeNonControlPlane(nodes, map[string][]db.ClusterRole{})
	assert.Equal(t, []uint64{1, 2}, testNodeIDs(result), "order should be unchanged when no members have control-plane role")
}

func TestPrioritizeNonControlPlane_AllControlPlane(t *testing.T) {
	nodes := []client.NodeInfo{
		{ID: 1, Address: "10.0.0.1:8443"},
		{ID: 2, Address: "10.0.0.2:8443"},
	}

	memberRoles := map[string][]db.ClusterRole{
		"10.0.0.1:8443": {db.ClusterRoleControlPlane},
		"10.0.0.2:8443": {db.ClusterRoleControlPlane},
	}

	result := prioritizeNonControlPlane(nodes, memberRoles)
	assert.Equal(t, []uint64{1, 2}, testNodeIDs(result), "order should be unchanged when all members have control-plane role")
}

func TestIsLeaderEvacuated_LeaderEvacuated(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
	}

	connectivity := map[string]bool{"10.0.0.1:8443": true, "10.0.0.2:8443": true}
	roles := testRolesChanges(nodes, connectivity, 1, 0)

	assert.True(t, isLeaderEvacuated(roles, 1, []string{"10.0.0.1:8443"}), "should detect evacuated leader")
}

func TestIsLeaderEvacuated_NonLeaderEvacuated(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
	}

	connectivity := map[string]bool{"10.0.0.1:8443": true, "10.0.0.2:8443": true}
	roles := testRolesChanges(nodes, connectivity, 1, 0)

	assert.False(t, isLeaderEvacuated(roles, 1, []string{"10.0.0.2:8443"}), "should return false when only a non-leader is evacuated")
}

func TestIsLeaderEvacuated_NoEvacuatedMembers(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
	}

	connectivity := map[string]bool{"10.0.0.1:8443": true}
	roles := testRolesChanges(nodes, connectivity, 1, 0)

	assert.False(t, isLeaderEvacuated(roles, 1, nil), "should return false when no members are evacuated")
}

// TestRolesAdjustBelowQuorum_DemoteExcessVoter verifies that when the leader is not
// evacuated and a second voter exists, that voter is demoted to spare.
func TestRolesAdjustBelowQuorum_DemoteExcessVoter(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
	}

	connectivity := map[string]bool{"10.0.0.1:8443": true, "10.0.0.2:8443": true}
	roles := testRolesChanges(nodes, connectivity, 1, 0)

	role, candidates, leaderNeedsTransfer := rolesAdjustBelowQuorum(roles, 1, nil, nil)

	assert.Equal(t, client.Spare, role)
	assert.Equal(t, []uint64{2}, testNodeIDs(candidates))
	assert.False(t, leaderNeedsTransfer)
}

// TestRolesAdjustBelowQuorum_NoChangesNeeded verifies that when the leader is the only
// voter, no role change is needed.
func TestRolesAdjustBelowQuorum_NoChangesNeeded(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftSpare}},
	}

	connectivity := map[string]bool{"10.0.0.1:8443": true, "10.0.0.2:8443": true}
	roles := testRolesChanges(nodes, connectivity, 1, 0)

	role, candidates, leaderNeedsTransfer := rolesAdjustBelowQuorum(roles, 1, nil, nil)

	assert.Equal(t, client.NodeRole(-1), role)
	assert.Empty(t, candidates)
	assert.False(t, leaderNeedsTransfer)
}

// TestRolesAdjustBelowQuorum_EvacuatedLeader_PromoteSpare verifies that when the leader
// is evacuated and a spare is available, the spare is promoted to voter so leadership
// can be transferred to it.
func TestRolesAdjustBelowQuorum_EvacuatedLeader_PromoteSpare(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftSpare}},
	}

	connectivity := map[string]bool{"10.0.0.1:8443": true, "10.0.0.2:8443": true}
	evacuated := []string{"10.0.0.1:8443"}
	roles := testRolesChanges(nodes, connectivity, 1, 0)

	role, candidates, leaderNeedsTransfer := rolesAdjustBelowQuorum(roles, 1, nil, evacuated)

	assert.Equal(t, client.Voter, role)
	assert.Equal(t, []uint64{2}, testNodeIDs(candidates))
	assert.False(t, leaderNeedsTransfer)
}

// TestRolesAdjustBelowQuorum_EvacuatedLeader_ReplacementExists verifies that when the
// leader is evacuated and a non-evacuated replacement voter already exists, a leadership
// transfer is signaled so the evacuated leader can be demoted in the next rebalance cycle.
func TestRolesAdjustBelowQuorum_EvacuatedLeader_ReplacementExists(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
	}

	connectivity := map[string]bool{"10.0.0.1:8443": true, "10.0.0.2:8443": true}
	evacuated := []string{"10.0.0.1:8443"}
	roles := testRolesChanges(nodes, connectivity, 1, 0)

	role, candidates, leaderNeedsTransfer := rolesAdjustBelowQuorum(roles, 1, nil, evacuated)

	assert.Equal(t, client.NodeRole(-1), role)
	assert.Empty(t, candidates)
	assert.True(t, leaderNeedsTransfer)
}

func TestEvacuatedMembersByState(t *testing.T) {
	voters := []client.NodeInfo{
		{ID: 1, Address: "10.0.0.1:8443"},
		{ID: 2, Address: "10.0.0.2:8443"},
	}

	standbys := []client.NodeInfo{
		{ID: 3, Address: "10.0.0.3:8443"},
		{ID: 4, Address: "10.0.0.4:8443"},
	}

	evacuated := []string{"10.0.0.1:8443", "10.0.0.3:8443"}

	result := evacuatedMembersByState(map[client.NodeRole][]client.NodeInfo{
		client.Voter:   voters,
		client.StandBy: standbys,
	}, evacuated)

	assert.Equal(t, []uint64{1}, testNodeIDs(result[client.Voter]), "only evacuated voters should be returned")
	assert.Equal(t, []uint64{3}, testNodeIDs(result[client.StandBy]), "only evacuated standbys should be returned")
}

func TestEvacuatedMembersByState_NoEvacuated(t *testing.T) {
	voters := []client.NodeInfo{
		{ID: 1, Address: "10.0.0.1:8443"},
		{ID: 2, Address: "10.0.0.2:8443"},
	}

	result := evacuatedMembersByState(map[client.NodeRole][]client.NodeInfo{
		client.Voter: voters,
	}, nil)

	assert.Empty(t, result[client.Voter], "no evacuated members should yield empty result")
}

func TestEvacuatedMembersByState_AllEvacuated(t *testing.T) {
	voters := []client.NodeInfo{
		{ID: 1, Address: "10.0.0.1:8443"},
		{ID: 2, Address: "10.0.0.2:8443"},
	}

	evacuated := []string{"10.0.0.1:8443", "10.0.0.2:8443"}

	result := evacuatedMembersByState(map[client.NodeRole][]client.NodeInfo{
		client.Voter: voters,
	}, evacuated)

	assert.Equal(t, []uint64{1, 2}, testNodeIDs(result[client.Voter]), "all members should appear when all are evacuated")
}
