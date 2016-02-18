package main

import (
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
)

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
	resultMap := []network{}
	for _, iface := range ifs {
		if recursion == 0 {
			resultString = append(resultString, fmt.Sprintf("/%s/networks/%s", shared.APIVersion, iface.Name))
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

type network struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	UsedBy []string `json:"used_by"`
}

func isOnBridge(c container, bridge string) bool {
	for _, device := range c.ExpandedDevices() {
		if device["type"] != "nic" {
			continue
		}

		if !shared.StringInSlice(device["nictype"], []string{"bridged", "macvlan"}) {
			continue
		}

		if device["parent"] == "" {
			continue
		}

		if device["parent"] == bridge {
			return true
		}
	}

	return false
}

func networkGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	n, err := doNetworkGet(d, name)
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, &n)
}

func doNetworkGet(d *Daemon, name string) (network, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return network{}, err
	}

	// Prepare the response
	n := network{}
	n.Name = iface.Name
	n.UsedBy = []string{}

	// Look for containers using the interface
	cts, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return network{}, err
	}

	for _, ct := range cts {
		c, err := containerLoadByName(d, ct)
		if err != nil {
			return network{}, err
		}

		if isOnBridge(c, n.Name) {
			n.UsedBy = append(n.UsedBy, fmt.Sprintf("/%s/containers/%s", shared.APIVersion, ct))
		}
	}

	// Set the device type as needed
	if shared.IsLoopback(iface) {
		n.Type = "loopback"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", n.Name)) {
		n.Type = "bridge"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/device", n.Name)) {
		n.Type = "physical"
	} else {
		n.Type = "unknown"
	}

	return n, nil
}

var networkCmd = Command{name: "networks/{name}", get: networkGet}
