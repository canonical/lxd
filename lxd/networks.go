package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"path"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
	"gopkg.in/lxc/go-lxc.v2"
)

func networksGet(d *Daemon, r *http.Request) Response {
	recursion_str := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursion_str)
	if err != nil {
		recursion = 0
	}

	ifs, err := net.Interfaces()
	if err != nil {
		return InternalError(err)
	}

	result_string := make([]string, 0)
	result_map := make([]network, 0)
	for _, iface := range ifs {
		if recursion == 0 {
			result_string = append(result_string, fmt.Sprintf("/%s/networks/%s", shared.APIVersion, iface.Name))
		} else {
			net, err := doNetworkGet(d, iface.Name)
			if err != nil {
				continue
			}
			result_map = append(result_map, net)

		}
	}

	if recursion == 0 {
		return SyncResponse(true, result_string)
	} else {
		return SyncResponse(true, result_map)
	}
}

var networksCmd = Command{name: "networks", get: networksGet}

type network struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Members []string `json:"members"`
}

func children(iface string) []string {
	p := path.Join(shared.SYS_CLASS_NET, iface, "brif")

	var ret []string

	ents, err := ioutil.ReadDir(p)
	if err != nil {
		return ret
	}

	for _, ent := range ents {
		ret = append(ret, ent.Name())
	}

	return ret
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
	} else if shared.IsBridge(iface) {
		n.Type = "bridge"
		for _, ct := range lxc.ActiveContainerNames(d.lxcpath) {
			c, err := newLxdContainer(ct, d)
			if err != nil {
				return network{}, err
			}

			if isOnBridge(c.c, n.Name) {
				n.Members = append(n.Members, ct)
			}
		}
	} else {
		n.Type = "unknown"
	}

	return n, nil
}

var networkCmd = Command{name: "networks/{name}", get: networkGet}
