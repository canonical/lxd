package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/3rdParty/github.com/gorilla/mux"
	"github.com/lxc/lxd/3rdParty/gopkg.in/lxc/go-lxc.v2"
)

const (
	SYS_CLASS_NET = "/sys/class/net"
)

func networksGet(d *Daemon, w http.ResponseWriter, r *http.Request) {
	ifs, err := net.Interfaces()
	if err != nil {
		InternalError(w, err)
		return
	}

	result := make([]string, 0)
	for _, iface := range ifs {
		result = append(result, fmt.Sprintf("/%s/networks/%s", lxd.ApiVersion, iface.Name))
	}

	SyncResponse(true, result, w)
}

var networksCmd = Command{"networks", false, networksGet, nil, nil, nil}

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

	ret := make([]string, 0)

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

func networkGet(d *Daemon, w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]

	iface, err := net.InterfaceByName(name)
	if err != nil {
		InternalError(w, err)
		return
	}

	n := network{}
	n.Name = iface.Name
	n.Members = make([]string, 0)

	if int(iface.Flags&net.FlagLoopback) > 0 {
		n.Type = "loopback"
	} else if isBridge(n.Name) {
		n.Type = "bridge"
		for _, ct := range lxc.ActiveContainerNames(d.lxcpath) {
			c, err := lxc.NewContainer(ct, d.lxcpath)
			if err != nil {
				InternalError(w, err)
				return
			}

			if isOnBridge(c, n.Name) {
				n.Members = append(n.Members, ct)
			}
		}
	} else {
		n.Type = "unknown"
	}

	SyncResponse(true, &n, w)
}

var networkCmd = Command{"networks/{name}", false, networkGet, nil, nil, nil}
