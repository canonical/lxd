package cluster

import (
	"fmt"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
)

// Connect is a convenience around lxd.ConnectLXD that configures the client
// with the correct parameters for node-to-node communication.
//
// If 'notify' switch is true, then the user agent will be set to the special
// value 'lxd-cluster-notifier', which can be used in some cases to distinguish
// between a regular client request and an internal cluster request.
func Connect(address string, cert *shared.CertInfo, notify bool) (lxd.ContainerServer, error) {
	args := &lxd.ConnectionArgs{
		TLSServerCert: string(cert.PublicKey()),
		TLSClientCert: string(cert.PublicKey()),
		TLSClientKey:  string(cert.PrivateKey()),
		SkipGetServer: true,
	}
	if notify {
		args.UserAgent = "lxd-cluster-notifier"
	}

	url := fmt.Sprintf("https://%s", address)
	return lxd.ConnectLXD(url, args)
}

// ConnectIfContainerIsRemote figures out the address of the node which is
// running the container with the given name. If it's not the local node will
// connect to it and return the connected client, otherwise it will just return
// nil.
func ConnectIfContainerIsRemote(cluster *db.Cluster, name string, cert *shared.CertInfo) (lxd.ContainerServer, error) {
	var address string // Node address
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		address, err = tx.ContainerNodeAddress(name)
		return err
	})
	if err != nil {
		return nil, err
	}
	if address == "" {
		// The container is running right on this node, no need to connect.
		return nil, nil
	}
	return Connect(address, cert, false)
}

// ConnectIfVolumeIsRemote figures out the address of the node on which the
// volume with the given name is defined. If it's not the local node will
// connect to it and return the connected client, otherwise it will just return
// nil.
//
// If there is more than one node with a matching volume name, an error is
// returned.
func ConnectIfVolumeIsRemote(cluster *db.Cluster, poolID int64, volumeName string, volumeType int, cert *shared.CertInfo) (lxd.ContainerServer, error) {
	var addresses []string // Node addresses
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		addresses, err = tx.StorageVolumeNodeAddresses(poolID, volumeName, volumeType)
		return err
	})
	if err != nil {
		return nil, err
	}

	if len(addresses) > 1 {
		return nil, fmt.Errorf("more than one node has a volume named %s", volumeName)
	}

	address := addresses[0]
	if address == "" {
		return nil, nil
	}
	return Connect(address, cert, false)
}
