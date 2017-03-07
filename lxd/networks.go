package main

import (
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// Helper functions
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

	ifs, err := net.Interfaces()
	if err != nil {
		return InternalError(err)
	}

	resultString := []string{}
	resultMap := []api.Network{}
	for _, iface := range ifs {
		if recursion == 0 {
			resultString = append(resultString, fmt.Sprintf("/%s/networks/%s", version.APIVersion, iface.Name))
		} else {
			net, err := doNetworkGet(d, iface.Name)
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

var networksCmd = Command{name: "networks", get: networksGet}

func networkGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	n, err := doNetworkGet(d, name)
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, &n)
}

func doNetworkGet(d *Daemon, name string) (api.Network, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return api.Network{}, err
	}

	// Prepare the response
	n := api.Network{}
	n.Name = iface.Name
	n.UsedBy = []string{}

	// Look for containers using the interface
	cts, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return api.Network{}, err
	}

	for _, ct := range cts {
		c, err := containerLoadByName(d, ct)
		if err != nil {
			return api.Network{}, err
		}

		if networkIsInUse(c, n.Name) {
			n.UsedBy = append(n.UsedBy, fmt.Sprintf("/%s/containers/%s", version.APIVersion, ct))
		}
	}

	// Set the device type as needed
	if shared.IsLoopback(iface) {
		n.Type = "loopback"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", n.Name)) {
		n.Type = "bridge"
	} else if shared.PathExists(fmt.Sprintf("/proc/net/vlan/%s", n.Name)) {
		n.Type = "vlan"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/device", n.Name)) {
		n.Type = "physical"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bonding", n.Name)) {
		n.Type = "bond"
	} else {
		_, err := shared.RunCommand("ovs-vsctl", "br-exists", n.Name)
		if err == nil {
			n.Type = "bridge"
		} else {
			n.Type = "unknown"
		}
	}

	return n, nil
}

var networkCmd = Command{name: "networks/{name}", get: networkGet}
