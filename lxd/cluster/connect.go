package cluster

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"time"

	"github.com/pkg/errors"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// UserAgentNotifier used to distinguish between a regular client request and an internal cluster request when
// notifying other nodes of a cluster change.
const UserAgentNotifier = "lxd-cluster-notifier"

// UserAgentJoiner used to distinguish between a regular client request and an internal cluster request when
// joining a node to a cluster.
const UserAgentJoiner = "lxd-cluster-joiner"

// ClientType indicates which sort of client type is being used.
type ClientType string

// ClientTypeNotifier cluster notification client.
const ClientTypeNotifier ClientType = "notifier"

// ClientTypeJoiner cluster joiner client.
const ClientTypeJoiner ClientType = "joiner"

// ClientTypeNormal normal client.
const ClientTypeNormal ClientType = "normal"

// UserAgentClientType converts user agent to client type.
func UserAgentClientType(userAgent string) ClientType {
	switch userAgent {
	case UserAgentNotifier:
		return ClientTypeNotifier
	case UserAgentJoiner:
		return ClientTypeJoiner
	}

	return ClientTypeNormal
}

// Connect is a convenience around lxd.ConnectLXD that configures the client
// with the correct parameters for node-to-node communication.
//
// If 'notify' switch is true, then the user agent will be set to the special
// to the UserAgentNotifier value, which can be used in some cases to distinguish
// between a regular client request and an internal cluster request.
func Connect(address string, cert *shared.CertInfo, notify bool) (lxd.InstanceServer, error) {
	// Wait for a connection to the events API first for non-notify connections.
	if !notify {
		connected := false
		for i := 0; i < 20; i++ {
			listenersLock.Lock()
			_, ok := listeners[address]
			listenersLock.Unlock()

			if ok {
				connected = true
				break
			}

			time.Sleep(500 * time.Millisecond)
		}

		if !connected {
			return nil, fmt.Errorf("Missing event connection with target cluster member")
		}
	}

	args := &lxd.ConnectionArgs{
		TLSServerCert: string(cert.PublicKey()),
		TLSClientCert: string(cert.PublicKey()),
		TLSClientKey:  string(cert.PrivateKey()),
		SkipGetServer: true,
		UserAgent:     version.UserAgent,
	}
	if notify {
		args.UserAgent = UserAgentNotifier
	}

	url := fmt.Sprintf("https://%s", address)
	return lxd.ConnectLXD(url, args)
}

// ConnectIfInstanceIsRemote figures out the address of the node which is
// running the container with the given name. If it's not the local node will
// connect to it and return the connected client, otherwise it will just return
// nil.
func ConnectIfInstanceIsRemote(cluster *db.Cluster, project, name string, cert *shared.CertInfo, instanceType instancetype.Type) (lxd.InstanceServer, error) {
	var address string // Node address
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		address, err = tx.GetNodeAddressOfInstance(project, name, instanceType)
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
func ConnectIfVolumeIsRemote(cluster *db.Cluster, poolID int64, volumeName string, volumeType int, cert *shared.CertInfo) (lxd.InstanceServer, error) {
	var addresses []string // Node addresses
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		addresses, err = tx.GetStorageVolumeNodeAddresses(poolID, "default", volumeName, volumeType)
		return err
	})
	if err != nil {
		return nil, err
	}

	if len(addresses) > 1 {
		var driver string
		err := cluster.Transaction(func(tx *db.ClusterTx) error {
			var err error
			driver, err = tx.GetStoragePoolDriver(poolID)
			return err
		})
		if err != nil {
			return nil, err
		}

		if driver == "ceph" || driver == "cephfs" {
			return nil, nil
		}

		return nil, fmt.Errorf("more than one node has a volume named %s", volumeName)
	}

	address := addresses[0]
	if address == "" {
		return nil, nil
	}

	return Connect(address, cert, false)
}

// SetupTrust is a convenience around InstanceServer.CreateCertificate that
// adds the given client certificate to the trusted pool of the cluster at the
// given address, using the given password.
func SetupTrust(cert, targetAddress, targetCert, targetPassword string) error {
	// Connect to the target cluster node.
	args := &lxd.ConnectionArgs{
		TLSServerCert: targetCert,
		UserAgent:     version.UserAgent,
	}

	target, err := lxd.ConnectLXD(fmt.Sprintf("https://%s", targetAddress), args)
	if err != nil {
		return errors.Wrap(err, "failed to connect to target cluster node")
	}

	block, _ := pem.Decode([]byte(cert))
	if block == nil {
		return fmt.Errorf("failed to decode certificate")
	}

	certificate := base64.StdEncoding.EncodeToString(block.Bytes)
	post := api.CertificatesPost{
		Password:    targetPassword,
		Certificate: certificate,
	}

	fingerprint, err := shared.CertFingerprintStr(cert)
	if err != nil {
		return errors.Wrap(err, "failed to calculate fingerprint")
	}

	post.Name = fmt.Sprintf("lxd.cluster.%s", fingerprint)
	post.Type = "client"

	err = target.CreateCertificate(post)
	if err != nil && err.Error() != "Certificate already in trust store" {
		return errors.Wrap(err, "Failed to add client cert to cluster")
	}

	return nil
}

// HasConnectivity probes the member with the given address for connectivity.
func HasConnectivity(cert *shared.CertInfo, address string) bool {
	config, err := tlsClientConfig(cert)
	if err != nil {
		return false
	}

	var conn net.Conn
	dialer := &net.Dialer{Timeout: time.Second}
	conn, err = tls.DialWithDialer(dialer, "tcp", address, config)
	if err == nil {
		conn.Close()
		return true
	}
	return false
}
