package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
	"gopkg.in/lxc/go-lxc.v2"
)

const (
	SYS_CLASS_NET = "/sys/class/net"
)

func networksGet(d *Daemon, r *http.Request) Response {
	ifs, err := net.Interfaces()
	if err != nil {
		return InternalError(err)
	}

	var result []string
	for _, iface := range ifs {
		result = append(result, fmt.Sprintf("/%s/networks/%s", shared.APIVersion, iface.Name))
	}

	return SyncResponse(true, result)
}

var networksCmd = Command{name: "networks", get: networksGet}

type network struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Members []string `json:"members"`
}

func isBridge(iface string) bool {
	p := path.Join(SYS_CLASS_NET, iface, "bridge")
	stat, err := os.Stat(p)
	if err != nil {
		return false
	}

	return stat.IsDir()
}

func children(iface string) []string {
	p := path.Join(SYS_CLASS_NET, iface, "brif")

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

	iface, err := net.InterfaceByName(name)
	if err != nil {
		return InternalError(err)
	}

	n := network{}
	n.Name = iface.Name
	n.Members = make([]string, 0)

	if int(iface.Flags&net.FlagLoopback) > 0 {
		n.Type = "loopback"
	} else if isBridge(n.Name) {
		n.Type = "bridge"
		for _, ct := range lxc.ActiveContainerNames(d.lxcpath) {
			c, err := newLxdContainer(ct, d)
			if err != nil {
				return InternalError(err)
			}

			if isOnBridge(c.c, n.Name) {
				n.Members = append(n.Members, ct)
			}
		}
	} else {
		n.Type = "unknown"
	}

	return SyncResponse(true, &n)
}

var networkCmd = Command{name: "networks/{name}", get: networkGet}
