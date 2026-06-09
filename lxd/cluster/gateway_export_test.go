package cluster

import (
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/shared"
)

// IsLeader returns true if this node is the leader.
func (g *Gateway) IsLeader() (bool, error) {
	return g.isLeader()
}

// ServerCert returns the gateway's internal TLS server certificate information.
func (g *Gateway) ServerCert() *shared.CertInfo {
	return g.networkCert
}

// NetworkCert returns the gateway's internal TLS NetworkCert certificate information.
func (g *Gateway) NetworkCert() *shared.CertInfo {
	return g.networkCert
}

// RaftNodes returns the nodes currently part of the raft cluster.
func (g *Gateway) RaftNodes() ([]db.RaftNode, error) {
	return g.currentRaftNodes()
}

// TriggerUpdate calls the internal triggerUpdate method.
func (g *Gateway) TriggerUpdate() {
	g.triggerUpdate()
}

// UpgradeTriggered returns whether an upgrade has been triggered.
func (g *Gateway) UpgradeTriggered() bool {
	g.lock.RLock()
	defer g.lock.RUnlock()
	return g.upgradeTriggered
}

// SetUpdateFunc replaces the update function called by triggerUpdate.
func (g *Gateway) SetUpdateFunc(f func() error) {
	g.updateFunc = f
}

// TryRLock attempts to acquire the gateway read lock without blocking.
// Returns true if the lock was acquired; the caller must call RUnlock to release it.
func (g *Gateway) TryRLock() bool {
	return g.lock.TryRLock()
}

// RUnlock releases a read lock previously acquired by TryRLock.
func (g *Gateway) RUnlock() {
	g.lock.RUnlock()
}
