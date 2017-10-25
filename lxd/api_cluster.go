package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
	"github.com/pkg/errors"
)

var clusterCmd = Command{name: "cluster", untrustedGet: true, get: clusterGet, untrustedPost: true, post: clusterPost}

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

func clusterPost(d *Daemon, r *http.Request) Response {
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
		return clusterPostBootstrap(d, req)
	} else if req.TargetAddress == "" {
		return clusterPostAccept(d, req)
	} else {
		// Joining an existing cluster requires the client to be
		// trusted.
		if !trusted {
			return Forbidden
		}
		return clusterPostJoin(d, req)
	}
}

func clusterPostBootstrap(d *Daemon, req api.ClusterPost) Response {
	run := func(op *operation) error {
		return cluster.Bootstrap(d.State(), d.gateway, req.Name)
	}
	resources := map[string][]string{}
	resources["cluster"] = []string{}

	op, err := operationCreate(operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func clusterPostAccept(d *Daemon, req api.ClusterPost) Response {
	// Accepting a node requires the client to provide the correct
	// trust password.
	secret, err := cluster.ConfigGetString(d.cluster, "core.trust_password")
	if err != nil {
		return SmartError(err)
	}
	if util.PasswordCheck(secret, req.TargetPassword) != nil {
		return Forbidden
	}
	nodes, err := cluster.Accept(d.State(), req.Name, req.Address, req.Schema, req.API)
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

func clusterPostJoin(d *Daemon, req api.ClusterPost) Response {
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
		err = util.WriteCert(d.os.VarDir, "cluster", req.TargetCert, info.PrivateKey, req.TargetCA)
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

	op, err := operationCreate(operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}
