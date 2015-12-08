package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"

	"github.com/gorilla/mux"
	"gopkg.in/lxc/go-lxc.v2"

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
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Members []string `json:"members"`
}

func children(iface string) []string {
	p := path.Join("/sys/class/net", iface, "brif")

	ret, _ := shared.ReadDir(p)

	return ret
}

func isBridge(iface *net.Interface) bool {
	p := path.Join("/sys/class/net", iface.Name, "bridge")
	stat, err := os.Stat(p)
	if err != nil {
		return false
	}

	return stat.IsDir()
}

func isOnBridge(c *lxc.Container, bridge string) bool {
	kids := children(bridge)
	for i := 0; i < len(c.ConfigItem("lxc.network")); i++ {
		interfaceType := c.RunningConfigItem(fmt.Sprintf("lxc.network.%d.type", i))
		if interfaceType[0] == "veth" {
			cif := c.RunningConfigItem(fmt.Sprintf("lxc.network.%d.veth.pair", i))[0]
			for _, kif := range kids {
				if cif == kif {
					return true
				}

			}
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

	n := network{}
	n.Name = iface.Name
	n.Members = make([]string, 0)

	if shared.IsLoopback(iface) {
		n.Type = "loopback"
	} else if isBridge(iface) {
		n.Type = "bridge"
		for _, ct := range lxc.ActiveContainerNames(d.lxcpath) {
			c, err := containerLoadByName(d, ct)
			if err != nil {
				return network{}, err
			}

			if isOnBridge(c.LXContainerGet(), n.Name) {
				n.Members = append(n.Members, ct)
			}
		}
	} else {
		n.Type = "unknown"
	}

	return n, nil
}

var networkCmd = Command{name: "networks/{name}", get: networkGet}
