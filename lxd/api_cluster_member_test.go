package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/canonical/go-dqlite/v3/client"
	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/lxd/db"
)

func TestCheckEvacuationQuorum(t *testing.T) {
	t.Parallel()

	// threshold is large enough that only nodes with a zero-value Heartbeat
	// are considered offline by IsOffline.
	const threshold = time.Minute

	recentHeartbeat := time.Now()

	// addr returns a synthetic cluster member address for node index i.
	addr := func(i int) string { return fmt.Sprintf("10.0.0.%d:8443", i) }

	// node builds a db.RaftNode with the given index, address, and role.
	node := func(i int, role db.RaftRole) db.RaftNode {
		return db.RaftNode{NodeInfo: client.NodeInfo{ID: uint64(i), Address: addr(i), Role: role}}
	}

	tests := []struct {
		name             string
		targetMember     db.NodeInfo
		raftNodes        []db.RaftNode
		connectivity     map[string]bool
		evacuatedMembers []string
		memberRoles      map[string][]db.ClusterRole
		wantErrContains  string // empty means no error expected
	}{
		{
			name:             "Target is standby; allow",
			targetMember:     db.NodeInfo{Address: addr(1), Heartbeat: recentHeartbeat},
			raftNodes:        []db.RaftNode{node(1, db.RaftStandBy), node(2, db.RaftVoter), node(3, db.RaftVoter), node(4, db.RaftVoter)},
			connectivity:     map[string]bool{addr(1): true, addr(2): true, addr(3): true, addr(4): true},
			evacuatedMembers: []string{},
			memberRoles:      map[string][]db.ClusterRole{},
		},
		{
			name:             "Target is spare; allow",
			targetMember:     db.NodeInfo{Address: addr(1), Heartbeat: recentHeartbeat},
			raftNodes:        []db.RaftNode{node(1, db.RaftSpare), node(2, db.RaftVoter), node(3, db.RaftVoter), node(4, db.RaftVoter)},
			connectivity:     map[string]bool{addr(1): true, addr(2): true, addr(3): true, addr(4): true},
			evacuatedMembers: []string{},
			memberRoles:      map[string][]db.ClusterRole{},
		},
		{
			name: "Target is offline voter; allow",
			// Zero-value Heartbeat makes IsOffline return true.
			targetMember:     db.NodeInfo{Address: addr(1)},
			raftNodes:        []db.RaftNode{node(1, db.RaftVoter), node(2, db.RaftVoter), node(3, db.RaftVoter)},
			connectivity:     map[string]bool{addr(1): false, addr(2): true, addr(3): true},
			evacuatedMembers: []string{},
			memberRoles:      map[string][]db.ClusterRole{},
		},
		{
			name:             "3-voter cluster fully online; allow (2 remain >= majority-of-2)",
			targetMember:     db.NodeInfo{Address: addr(1), Heartbeat: recentHeartbeat},
			raftNodes:        []db.RaftNode{node(1, db.RaftVoter), node(2, db.RaftVoter), node(3, db.RaftVoter)},
			connectivity:     map[string]bool{addr(1): true, addr(2): true, addr(3): true},
			evacuatedMembers: []string{},
			memberRoles:      map[string][]db.ClusterRole{},
		},
		{
			name:             "3 voters 2 online no standby; deny",
			targetMember:     db.NodeInfo{Address: addr(1), Heartbeat: recentHeartbeat},
			raftNodes:        []db.RaftNode{node(1, db.RaftVoter), node(2, db.RaftVoter), node(3, db.RaftVoter)},
			connectivity:     map[string]bool{addr(1): true, addr(2): true, addr(3): false},
			evacuatedMembers: []string{},
			memberRoles:      map[string][]db.ClusterRole{},
			wantErrContains:  "Insufficient online voters",
		},
		{
			name:             "3 voters 2 online with online standby; allow (replacement can be promoted)",
			targetMember:     db.NodeInfo{Address: addr(1), Heartbeat: recentHeartbeat},
			raftNodes:        []db.RaftNode{node(1, db.RaftVoter), node(2, db.RaftVoter), node(3, db.RaftVoter), node(4, db.RaftStandBy)},
			connectivity:     map[string]bool{addr(1): true, addr(2): true, addr(3): false, addr(4): true},
			evacuatedMembers: []string{},
			memberRoles:      map[string][]db.ClusterRole{},
		},
		{
			name:             "3 voters 2 online standby is evacuated; deny",
			targetMember:     db.NodeInfo{Address: addr(1), Heartbeat: recentHeartbeat},
			raftNodes:        []db.RaftNode{node(1, db.RaftVoter), node(2, db.RaftVoter), node(3, db.RaftVoter), node(4, db.RaftStandBy)},
			connectivity:     map[string]bool{addr(1): true, addr(2): true, addr(3): false, addr(4): true},
			evacuatedMembers: []string{addr(4)},
			memberRoles:      map[string][]db.ClusterRole{},
			wantErrContains:  "Insufficient online voters",
		},
		{
			// 5 voters, only 2 online. Current majority = 3, so the cluster cannot
			// make the promotion decision even though a standby is available.
			name:             "Cluster below current majority; deny even with standby",
			targetMember:     db.NodeInfo{Address: addr(1), Heartbeat: recentHeartbeat},
			raftNodes:        []db.RaftNode{node(1, db.RaftVoter), node(2, db.RaftVoter), node(3, db.RaftVoter), node(4, db.RaftVoter), node(5, db.RaftVoter), node(6, db.RaftStandBy)},
			connectivity:     map[string]bool{addr(1): true, addr(2): true, addr(3): false, addr(4): false, addr(5): false, addr(6): true},
			evacuatedMembers: []string{},
			memberRoles:      map[string][]db.ClusterRole{},
			wantErrContains:  "Insufficient online voters",
		},
		{
			// Single voter means onlineVoters == 1 after evacuation, triggering the
			// onlineVoters > 1 guard that prevents a degenerate promotion.
			name:             "Single voter with standby; deny",
			targetMember:     db.NodeInfo{Address: addr(1), Heartbeat: recentHeartbeat},
			raftNodes:        []db.RaftNode{node(1, db.RaftVoter), node(2, db.RaftStandBy)},
			connectivity:     map[string]bool{addr(1): true, addr(2): true},
			evacuatedMembers: []string{},
			memberRoles:      map[string][]db.ClusterRole{},
			wantErrContains:  "Insufficient online voters",
		},
		{
			// 5 CP voters; addr(3) and addr(5) are offline. Removing the target
			// (addr(1)) leaves 4 CP members — CP mode stays active (≥3). The
			// non-CP standby (addr(4)) is ineligible as a replacement, and the
			// remaining online voters (addr(2), addr(6)) do not meet the
			// required majority of 3 for a 4-voter post-evacuation cluster.
			name:         "Control plane active; standby lacks role; deny",
			targetMember: db.NodeInfo{Address: addr(1), Heartbeat: recentHeartbeat},
			raftNodes: []db.RaftNode{
				node(1, db.RaftVoter), node(2, db.RaftVoter), node(3, db.RaftVoter),
				node(4, db.RaftStandBy), node(5, db.RaftVoter), node(6, db.RaftVoter),
			},
			connectivity:     map[string]bool{addr(1): true, addr(2): true, addr(3): false, addr(4): true, addr(5): false, addr(6): true},
			evacuatedMembers: []string{},
			memberRoles: map[string][]db.ClusterRole{
				addr(1): {db.ClusterRoleControlPlane},
				addr(2): {db.ClusterRoleControlPlane},
				addr(3): {db.ClusterRoleControlPlane},
				addr(5): {db.ClusterRoleControlPlane},
				addr(6): {db.ClusterRoleControlPlane},
			},
			wantErrContains: "Insufficient online voters",
		},
		{
			// Target is already EVACUATED in the DB (it appears in evacuatedMembers)
			// but still holds a voter raft role that rebalance has not yet demoted.
			// Its heartbeat is still fresh. The quorum check must allow the request
			// rather than double-counting the target's absence and returning
			// "Insufficient online voters".
			name:             "Target is already evacuated voter; allow",
			targetMember:     db.NodeInfo{Address: addr(1), Heartbeat: recentHeartbeat},
			raftNodes:        []db.RaftNode{node(1, db.RaftVoter), node(2, db.RaftVoter), node(3, db.RaftVoter)},
			connectivity:     map[string]bool{addr(1): true, addr(2): true, addr(3): true},
			evacuatedMembers: []string{addr(1)},
			memberRoles:      map[string][]db.ClusterRole{},
		},
		{
			// Target is one of exactly 3 CP members, so without the fix
			// IsControlPlaneActive would return true and exclude the non-CP standby.
			// After removing the target from the roles map, only 2 CP members remain,
			// CP mode becomes inactive, and the CP standby (addr(4)) is eligible.
			name:         "Control plane active; target is CP voter; standby has CP role; allow",
			targetMember: db.NodeInfo{Address: addr(1), Heartbeat: recentHeartbeat},
			raftNodes: []db.RaftNode{
				node(1, db.RaftVoter), node(2, db.RaftVoter), node(3, db.RaftVoter), node(4, db.RaftStandBy),
			},
			connectivity:     map[string]bool{addr(1): true, addr(2): true, addr(3): false, addr(4): true},
			evacuatedMembers: []string{},
			memberRoles: map[string][]db.ClusterRole{
				addr(1): {db.ClusterRoleControlPlane},
				addr(2): {db.ClusterRoleControlPlane},
				addr(3): {db.ClusterRoleControlPlane},
				addr(4): {db.ClusterRoleControlPlane},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkEvacuationQuorum(
				tc.targetMember,
				tc.raftNodes,
				tc.connectivity,
				tc.evacuatedMembers,
				tc.memberRoles,
				threshold,
			)
			if tc.wantErrContains != "" {
				assert.ErrorContains(t, err, tc.wantErrContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
