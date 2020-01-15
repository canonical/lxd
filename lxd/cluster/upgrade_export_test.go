package cluster

import "github.com/lxc/lxd/lxd/db"

func UpgradeMembersWithoutRole(gateway *Gateway, members []db.NodeInfo, nodes []db.RaftNode) error {
	return upgradeMembersWithoutRole(gateway, members, nodes)
}
