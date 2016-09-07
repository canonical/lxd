package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
)

// Helper functions
func networkGetInterfaces(d *Daemon) ([]string, error) {
	networks, err := dbNetworks(d.db)
	if err != nil {
		return nil, err
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range ifaces {
		if !shared.StringInSlice(iface.Name, networks) {
			networks = append(networks, iface.Name)
		}
	}

	return networks, nil
}

func networkIsInUse(c container, name string) bool {
	for _, d := range c.ExpandedDevices() {
		if d["type"] != "nic" {
			continue
		}

		if !shared.StringInSlice(d["nictype"], []string{"bridged", "macvlan"}) {
			continue
		}

		if d["parent"] == "" {
			continue
		}

		if d["parent"] == name {
			return true
		}
	}

	return false
}

// API endpoints
func networksGet(d *Daemon, r *http.Request) Response {
	recursionStr := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	ifs, err := networkGetInterfaces(d)
	if err != nil {
		return InternalError(err)
	}

	resultString := []string{}
	resultMap := []shared.NetworkConfig{}
	for _, iface := range ifs {
		if recursion == 0 {
			resultString = append(resultString, fmt.Sprintf("/%s/networks/%s", shared.APIVersion, iface))
		} else {
			net, err := doNetworkGet(d, iface)
			if err != nil {
				continue
			}
			resultMap = append(resultMap, net)
		}
	}

	if recursion == 0 {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

func networksPost(d *Daemon, r *http.Request) Response {
	req := shared.NetworkConfig{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	if req.Type != "" && req.Type != "bridge" {
		return BadRequest(fmt.Errorf("Only 'bridge' type networks can be created"))
	}

	networks, err := networkGetInterfaces(d)
	if err != nil {
		return InternalError(err)
	}

	if shared.StringInSlice(req.Name, networks) {
		return BadRequest(fmt.Errorf("The network already exists"))
	}

	// Create the database entry
	_, err = dbNetworkCreate(d.db, req.Name, req.Config)
	if err != nil {
		return InternalError(
			fmt.Errorf("Error inserting %s into database: %s", req.Name, err))
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/networks/%s", shared.APIVersion, req.Name))
}

var networksCmd = Command{name: "networks", get: networksGet, post: networksPost}

func networkGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	n, err := doNetworkGet(d, name)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, &n)
}

func doNetworkGet(d *Daemon, name string) (shared.NetworkConfig, error) {
	// Get some information
	osInfo, _ := net.InterfaceByName(name)
	_, dbInfo, _ := dbNetworkGet(d.db, name)

	// Sanity check
	if osInfo == nil && dbInfo == nil {
		return shared.NetworkConfig{}, os.ErrNotExist
	}

	// Prepare the response
	n := shared.NetworkConfig{}
	n.Name = name
	n.UsedBy = []string{}
	n.Config = map[string]string{}

	// Look for containers using the interface
	cts, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return shared.NetworkConfig{}, err
	}

	for _, ct := range cts {
		c, err := containerLoadByName(d, ct)
		if err != nil {
			return shared.NetworkConfig{}, err
		}

		if networkIsInUse(c, n.Name) {
			n.UsedBy = append(n.UsedBy, fmt.Sprintf("/%s/containers/%s", shared.APIVersion, ct))
		}
	}

	// Set the device type as needed
	if osInfo != nil && shared.IsLoopback(osInfo) {
		n.Type = "loopback"
	} else if dbInfo != nil || shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", n.Name)) {
		if dbInfo != nil {
			n.Managed = true
			n.Config = dbInfo.Config
		}

		n.Type = "bridge"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/device", n.Name)) {
		n.Type = "physical"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bonding", n.Name)) {
		n.Type = "bond"
	} else {
		_, err := exec.Command("ovs-vsctl", "br-exists", n.Name).CombinedOutput()
		if err == nil {
			n.Type = "bridge"
		} else {
			n.Type = "unknown"
		}
	}

	return n, nil
}

func networkDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Get the existing network
	_, dbInfo, _ := dbNetworkGet(d.db, name)
	if dbInfo == nil {
		return NotFound
	}

	// Sanity checks
	if len(dbInfo.UsedBy) != 0 {
		return BadRequest(fmt.Errorf("Network is currently in use)"))
	}

	// Remove the network
	err := dbNetworkDelete(d.db, name)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

var networkCmd = Command{name: "networks/{name}", get: networkGet, delete: networkDelete}
