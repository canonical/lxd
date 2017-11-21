package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gorilla/mux"
	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
	"github.com/pkg/errors"
)

var clusterCmd = Command{name: "cluster", untrustedGet: true, get: clusterGet, delete: clusterDelete}

// Return information about the cluster, such as the current networks and
// storage pools, typically needed when a new node is joining.
func clusterGet(d *Daemon, r *http.Request) Response {
	// If the client is not trusted, check that it's presenting the trust
	// password.
	trusted := d.checkTrustedClient(r) == nil
	if !trusted {
		secret, err := cluster.ConfigGetString(d.cluster, "core.trust_password")
		if err != nil {
			return SmartError(err)
		}
		if util.PasswordCheck(secret, r.FormValue("password")) != nil {
			return Forbidden
		}
	}

	cluster := api.Cluster{}

	// Fill the Networks attribute
	networks, err := d.cluster.Networks()
	if err != nil {
		return SmartError(err)
	}
	for _, name := range networks {
		_, network, err := d.cluster.NetworkGet(name)
		if err != nil {
			return SmartError(err)
		}
		cluster.Networks = append(cluster.Networks, *network)
	}

	// Fill the StoragePools attribute
	pools, err := d.cluster.StoragePools()
	if err != nil {
		return SmartError(err)
	}
	for _, name := range pools {
		_, pool, err := d.cluster.StoragePoolGet(name)
		if err != nil {
			return SmartError(err)
		}
		cluster.StoragePools = append(cluster.StoragePools, *pool)
	}

	return SyncResponse(true, cluster)
}

// Disable clustering on a node.
func clusterDelete(d *Daemon, r *http.Request) Response {
	// Update our TLS configuration using our original certificate.
	for _, suffix := range []string{"crt", "key", "ca"} {
		path := filepath.Join(d.os.VarDir, "cluster."+suffix)
		if !shared.PathExists(path) {
			continue
		}
		err := os.Remove(path)
		if err != nil {
			return InternalError(err)
		}
	}
	cert, err := util.LoadCert(d.os.VarDir)
	if err != nil {
		return InternalError(errors.Wrap(err, "failed to parse node certificate"))
	}

	// Reset the cluster database and make it local to this node.
	d.endpoints.NetworkUpdateCert(cert)
	err = d.gateway.Reset(cert)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

var clusterNodesCmd = Command{
	name: "cluster/nodes",
	post: clusterNodesPost, untrustedPost: true,
	get: clusterNodesGet,
}

// Depending on the parameters passed and on local state this endpoint will
// either:
//
// - bootstrap a new cluster (if this node is not clustered yet)
// - request to join an existing cluster
// - accept the request of a node to join the cluster
//
// The client is required to be trusted when bootstrapping a cluster or request
// to join an existing cluster.
func clusterNodesPost(d *Daemon, r *http.Request) Response {
	req := api.ClusterPost{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	// Depending on the provided parameters we either bootstrap a brand new
	// cluster with this node as first node, or accept a node into our
	// cluster, or perform a request to join a given cluster.
	trusted := d.checkTrustedClient(r) == nil
	if req.Address == "" && req.TargetAddress == "" {
		// Bootstrapping a node requires the client to be trusted.
		if !trusted {
			return Forbidden
		}
		return clusterNodesPostBootstrap(d, req)
	} else if req.TargetAddress == "" {
		return clusterNodesPostAccept(d, req)
	} else {
		// Joining an existing cluster requires the client to be
		// trusted.
		if !trusted {
			return Forbidden
		}
		return clusterNodesPostJoin(d, req)
	}
}

func clusterNodesPostBootstrap(d *Daemon, req api.ClusterPost) Response {
	run := func(op *operation) error {
		return cluster.Bootstrap(d.State(), d.gateway, req.Name)
	}
	resources := map[string][]string{}
	resources["cluster"] = []string{}

	op, err := operationCreate(d.cluster, operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func clusterNodesPostAccept(d *Daemon, req api.ClusterPost) Response {
	// Redirect all requests to the leader, which is the one with
	// knowning what nodes are part of the raft cluster.
	address, err := node.HTTPSAddress(d.db)
	if err != nil {
		return SmartError(err)
	}
	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return InternalError(err)
	}
	if address != leader {
		logger.Debugf("Redirect node accept request to %s", leader)
		url := &url.URL{
			Scheme: "https",
			Path:   "/1.0/cluster/nodes",
			Host:   leader,
		}
		return SyncResponseRedirect(url.String())
	}

	// Accepting a node requires the client to provide the correct
	// trust password.
	secret, err := cluster.ConfigGetString(d.cluster, "core.trust_password")
	if err != nil {
		return SmartError(err)
	}
	if util.PasswordCheck(secret, req.TargetPassword) != nil {
		return Forbidden
	}
	nodes, err := cluster.Accept(d.State(), d.gateway, req.Name, req.Address, req.Schema, req.API)
	if err != nil {
		return BadRequest(err)
	}
	accepted := api.ClusterNodeAccepted{
		RaftNodes:  make([]api.RaftNode, len(nodes)),
		PrivateKey: d.endpoints.NetworkPrivateKey(),
	}
	for i, node := range nodes {
		accepted.RaftNodes[i].ID = node.ID
		accepted.RaftNodes[i].Address = node.Address
	}
	return SyncResponse(true, accepted)
}

func clusterNodesPostJoin(d *Daemon, req api.ClusterPost) Response {
	// Make sure basic pre-conditions are ment.
	if len(req.TargetCert) == 0 {
		return BadRequest(fmt.Errorf("No target cluster node certificate provided"))
	}
	address, err := node.HTTPSAddress(d.db)
	if err != nil {
		return InternalError(err)
	}
	if address == "" {
		return BadRequest(fmt.Errorf("No core.https_address config key is set on this node"))
	}

	// Client parameters to connect to the target cluster node.
	args := &lxd.ConnectionArgs{
		TLSServerCert: string(req.TargetCert),
		TLSCA:         string(req.TargetCA),
	}

	// Asynchronously join the cluster.
	run := func(op *operation) error {
		// First request for this node to be added to the list of
		// cluster nodes.
		client, err := lxd.ConnectLXD(fmt.Sprintf("https://%s", req.TargetAddress), args)
		if err != nil {
			return err
		}
		info, err := client.AcceptNode(
			req.TargetPassword, req.Name, address, cluster.SchemaVersion,
			len(version.APIExtensions))
		if err != nil {
			return errors.Wrap(err, "failed to request to add node")
		}

		// Update our TLS configuration using the returned cluster certificate.
		err = util.WriteCert(d.os.VarDir, "cluster", []byte(req.TargetCert), info.PrivateKey, req.TargetCA)
		if err != nil {
			return errors.Wrap(err, "failed to save cluster certificate")
		}
		cert, err := util.LoadCert(d.os.VarDir)
		if err != nil {
			return errors.Wrap(err, "failed to parse cluster certificate")
		}
		d.endpoints.NetworkUpdateCert(cert)

		// Update local setup and possibly join the raft dqlite
		// cluster.
		nodes := make([]db.RaftNode, len(info.RaftNodes))
		for i, node := range info.RaftNodes {
			nodes[i].ID = node.ID
			nodes[i].Address = node.Address
		}
		return cluster.Join(d.State(), d.gateway, cert, req.Name, nodes)
	}
	resources := map[string][]string{}
	resources["cluster"] = []string{}

	op, err := operationCreate(d.cluster, operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func clusterNodesGet(d *Daemon, r *http.Request) Response {
	dbNodes, flags, err := cluster.List(d.State())
	if err != nil {
		return SmartError(err)
	}

	nodes := make([]api.Node, len(dbNodes))
	for i, dbNode := range dbNodes {
		nodes[i].Name = dbNode.Name
		nodes[i].URL = fmt.Sprintf("https://%s", dbNode.Address)
		nodes[i].Database = flags[dbNode.ID]
		if dbNode.IsDown() {
			nodes[i].State = "OFFLINE"
		} else {
			nodes[i].State = "ONLINE"
		}
	}

	return SyncResponse(true, nodes)
}

var clusterNodeCmd = Command{
	name:   "cluster/nodes/{name}",
	get:    clusterNodeGet,
	post:   clusterNodePost,
	delete: clusterNodeDelete,
}

func clusterNodeGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	node := api.Node{}
	node.Name = name
	address := ""
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		dbNode, err := tx.NodeByName(name)
		if err != nil {
			return err
		}
		address = dbNode.Address
		node.URL = fmt.Sprintf("https://%s", dbNode.Address)
		if dbNode.IsDown() {
			node.State = "OFFLINE"
		} else {
			node.State = "ONLINE"
		}
		return nil
	})
	if err != nil {
		return SmartError(err)
	}

	// Figure out if this node is currently a database node.
	err = d.db.Transaction(func(tx *db.NodeTx) error {
		addresses, err := tx.RaftNodeAddresses()
		if err != nil {
			return err
		}
		if shared.StringInSlice(address, addresses) {
			node.Database = true
		}
		return nil
	})

	return SyncResponse(true, node)
}

func clusterNodePost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	req := api.NodePost{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.NodeRename(name, req.Name)
	})
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func clusterNodeDelete(d *Daemon, r *http.Request) Response {
	force, err := strconv.Atoi(r.FormValue("force"))
	if err != nil {
		force = 0
	}

	name := mux.Vars(r)["name"]
	address, err := cluster.Leave(d.State(), d.gateway, name, force == 1)
	if err != nil {
		return SmartError(err)
	}

	var run func(op *operation) error

	if force == 1 {
		// If the force flag is on, the returned operation is a no-op.
		run = func(op *operation) error {
			return nil
		}

	} else {
		// Try to gracefully disable clustering on the target node.
		cert := d.endpoints.NetworkCert()
		run = func(op *operation) error {
			// First request for this node to be added to the list of
			// cluster nodes.
			client, err := cluster.Connect(address, cert, false)
			if err != nil {
				return err
			}
			_, _, err = client.RawQuery("DELETE", "/1.0/cluster", nil, "")
			return err
		}
	}

	err = run(nil)
	if err != nil {
		return SmartError(err)
	}
	return EmptySyncResponse
}
