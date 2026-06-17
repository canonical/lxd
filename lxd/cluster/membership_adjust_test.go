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

// TestAdjustRoles_EvacuatedMemberIsNeverPromoted verifies that evacuated members
// are excluded from voter promotion even when they are otherwise eligible.
func TestAdjustRoles_EvacuatedMemberIsNeverPromoted(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftSpare}},
		{NodeInfo: client.NodeInfo{ID: 4, Address: "10.0.0.4:8443", Role: db.RaftSpare}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
		"10.0.0.4:8443": true,
	}

	evacuatedMembers := []string{
		"10.0.0.3:8443",
	}

	roles := testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, nil, evacuatedMembers)

	assert.Equal(t, db.RaftVoter, role)
	assert.Equal(t, []uint64{4}, testNodeIDs(candidates))
}

// TestAdjustRoles_EvacuatedVoterPromotionFirst verifies that evacuation first
// promotes a replacement voter and then demotes the evacuated member.
func TestAdjustRoles_EvacuatedVoterPromotionFirst(t *testing.T) {
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

	evacuatedMembers := []string{
		"10.0.0.2:8443",
	}

	roles := testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, nil, evacuatedMembers)

	assert.Equal(t, db.RaftVoter, role)
	assert.Equal(t, []uint64{4}, testNodeIDs(candidates))

	nodes[3].Role = db.RaftVoter
	roles = testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates, _ = rolesAdjust(roles, 1, nodes, connectivity, nil, evacuatedMembers)

	assert.Equal(t, db.RaftSpare, role)
	assert.NotEmpty(t, candidates)
	assert.Equal(t, uint64(2), candidates[0].ID)
}

// TestAdjustRoles_EvacuatedVoterDemotedWithoutReplacement verifies that an
// evacuated voter is still demoted when no replacement candidate exists.
func TestAdjustRoles_EvacuatedVoterDemotedWithoutReplacement(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
	}

	evacuatedMembers := []string{
		"10.0.0.2:8443",
	}

	roles := testRolesChanges(nodes, connectivity, 2, 0)
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, nil, evacuatedMembers)

	assert.Equal(t, db.RaftSpare, role)
	assert.Equal(t, []uint64{2}, testNodeIDs(candidates))
}

// TestAdjustRoles_EvacuatedVoterDemotedWithStandbyTarget verifies that an
// evacuated voter is demoted even when the standby target is unsatisfied.
// This covers the case where all nodes are voters (no spares to promote to
// standby) and the standby promotion check must not prevent the evacuated
// voter from being demoted.
func TestAdjustRoles_EvacuatedVoterDemotedWithStandbyTarget(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftVoter}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
	}

	evacuatedMembers := []string{
		"10.0.0.2:8443",
	}

	// 3 voters with max_voters=3 and max_standby=2 (the default).
	// The standby target is unmet (0 standbys vs 2 target) but there are
	// no spares to promote. The evacuated voter must still be demoted.
	roles := testRolesChanges(nodes, connectivity, 3, 2)
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, nil, evacuatedMembers)

	assert.Equal(t, db.RaftSpare, role)
	assert.Equal(t, []uint64{2}, testNodeIDs(candidates))
}

// TestAdjustRoles_EvacuatedStandbyDemotedWithoutReplacement verifies that an
// evacuated standby is still demoted when no replacement candidate exists.
func TestAdjustRoles_EvacuatedStandbyDemotedWithoutReplacement(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftStandBy}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
	}

	evacuatedMembers := []string{
		"10.0.0.3:8443",
	}

	roles := testRolesChanges(nodes, connectivity, 2, 1)
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, nil, evacuatedMembers)

	assert.Equal(t, db.RaftSpare, role)
	assert.Equal(t, []uint64{3}, testNodeIDs(candidates))
}

// TestAdjustRoles_EvacuatedStandbyReplacedWhenSpareAvailable verifies that
// when an evacuated standby can be replaced by a spare, the spare is promoted
// first before the evacuated standby is demoted.
func TestAdjustRoles_EvacuatedStandbyReplacedWhenSpareAvailable(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftStandBy}},
		{NodeInfo: client.NodeInfo{ID: 4, Address: "10.0.0.4:8443", Role: db.RaftSpare}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
		"10.0.0.4:8443": true,
	}

	evacuatedMembers := []string{
		"10.0.0.3:8443",
	}

	// Step 1: promote spare to standby (replacement for evacuated standby).
	roles := testRolesChanges(nodes, connectivity, 2, 1)
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, nil, evacuatedMembers)

	assert.Equal(t, db.RaftStandBy, role)
	assert.Equal(t, []uint64{4}, testNodeIDs(candidates))

	// Step 2: after promotion, we have 2 standbys vs target of 1, and the
	// evacuated standby (node3) is prioritized first for demotion.
	nodes[3].Role = db.RaftStandBy
	roles = testRolesChanges(nodes, connectivity, 2, 1)
	role, candidates, _ = rolesAdjust(roles, 1, nodes, connectivity, nil, evacuatedMembers)

	assert.Equal(t, client.Spare, role)
	assert.NotEmpty(t, candidates)
	assert.Equal(t, uint64(3), candidates[0].ID)
}

// TestAdjustRoles_EvacuatedStandbyNotBlockedByVoterPromotion verifies that
// an evacuated standby is demoted even when the voter count is below target
// and cannot be filled. The voter promotion check must not prevent subsequent
// standby demotion.
func TestAdjustRoles_EvacuatedStandbyNotBlockedByVoterPromotion(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftStandBy}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
	}

	evacuatedMembers := []string{
		"10.0.0.3:8443",
	}

	// max_voters=3 but only 2 actual voters and no spares to promote.
	// The evacuated standby must still be demoted despite the voter deficit.
	roles := testRolesChanges(nodes, connectivity, 3, 1)
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, nil, evacuatedMembers)

	assert.Equal(t, db.RaftSpare, role)
	assert.Equal(t, []uint64{3}, testNodeIDs(candidates))
}

// TestAdjustRoles_ExtraVotersDemoteEvacuatedFirst verifies that when the
// cluster has more voters than the target, the evacuated voter is prioritized
// for demotion.
func TestAdjustRoles_ExtraVotersDemoteEvacuatedFirst(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 4, Address: "10.0.0.4:8443", Role: db.RaftVoter}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
		"10.0.0.4:8443": true,
	}

	evacuatedMembers := []string{
		"10.0.0.3:8443",
	}

	// 4 voters with max_voters=3. The evacuated voter (node3) should be
	// prioritized for demotion over non-evacuated voters.
	roles := testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, nil, evacuatedMembers)

	assert.Equal(t, client.Spare, role)
	assert.NotEmpty(t, candidates)
	assert.Equal(t, uint64(3), candidates[0].ID)
}

// TestAdjustRoles_OfflineVoterDemoted verifies that an offline voter is
// demoted to spare when no standby or spare is available for promotion.
func TestAdjustRoles_OfflineVoterDemoted(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftVoter}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": false,
	}

	roles := testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, nil, nil)

	assert.Equal(t, client.Spare, role)
	assert.Equal(t, []uint64{3}, testNodeIDs(candidates))
}

// TestAdjustRoles_StandbyPromoted verifies that a spare is promoted to standby
// when the cluster is below the standby target.
func TestAdjustRoles_StandbyPromoted(t *testing.T) {
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

	roles := testRolesChanges(nodes, connectivity, 3, 1)
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, nil, nil)

	assert.Equal(t, client.StandBy, role)
	assert.Equal(t, []uint64{4}, testNodeIDs(candidates))
}

// TestAdjustRoles_EvacuatedVoterWithControlPlaneActive verifies that an
// evacuated voter with the control-plane role is still demoted when
// control-plane mode is active.
func TestAdjustRoles_EvacuatedVoterWithControlPlaneActive(t *testing.T) {
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
		"10.0.0.2:8443": {db.ClusterRoleControlPlane},
		"10.0.0.3:8443": {db.ClusterRoleControlPlane},
		"10.0.0.4:8443": {db.ClusterRoleControlPlane},
	}

	evacuatedMembers := []string{
		"10.0.0.2:8443",
	}

	// Step 1: promote spare (node4) to replace evacuated voter (node2).
	roles := testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates, _ := rolesAdjust(roles, 1, nodes, connectivity, memberRoles, evacuatedMembers)

	assert.Equal(t, db.RaftVoter, role)
	assert.Equal(t, []uint64{4}, testNodeIDs(candidates))

	// Step 2: after promotion, evacuated voter is demoted.
	nodes[3].Role = db.RaftVoter
	roles = testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates, _ = rolesAdjust(roles, 1, nodes, connectivity, memberRoles, evacuatedMembers)

	assert.Equal(t, db.RaftSpare, role)
	assert.NotEmpty(t, candidates)
	assert.Equal(t, uint64(2), candidates[0].ID)
}

// TestAdjustRoles_EvacuatedLeaderSignalsTransfer verifies that when the only
// remaining evacuated voter is the raft leader, rolesAdjust signals a leadership
// transfer (leaderNeedsTransfer=true) so the leader can hand off before being
// demoted in a subsequent rebalance cycle.
func TestAdjustRoles_EvacuatedLeaderSignalsTransfer(t *testing.T) {
	nodes := []db.RaftNode{
		{NodeInfo: client.NodeInfo{ID: 1, Address: "10.0.0.1:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 2, Address: "10.0.0.2:8443", Role: db.RaftVoter}},
		{NodeInfo: client.NodeInfo{ID: 3, Address: "10.0.0.3:8443", Role: db.RaftVoter}},
	}

	connectivity := map[string]bool{
		"10.0.0.1:8443": true,
		"10.0.0.2:8443": true,
		"10.0.0.3:8443": true,
	}

	// Node 1 is the leader and is evacuated.
	evacuatedMembers := []string{
		"10.0.0.1:8443",
	}

	// Leader ID is 1. Node 1 is evacuated but is the leader and cannot be
	// demoted directly; rolesAdjust must signal a leadership transfer.
	roles := testRolesChanges(nodes, connectivity, 3, 0)
	role, candidates, leaderNeedsTransfer := rolesAdjust(roles, 1, nodes, connectivity, nil, evacuatedMembers)

	assert.Equal(t, client.NodeRole(-1), role)
	assert.Empty(t, candidates)
	assert.True(t, leaderNeedsTransfer)
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
