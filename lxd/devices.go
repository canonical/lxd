package main

import (
	"fmt"

	"github.com/lxc/lxd/shared"
)

func DeviceToLxc(d shared.Device) ([][]string, error) {
	switch d["type"] {
	case "unix-char":
		return nil, fmt.Errorf("Not implemented")
	case "unix-block":
		return nil, fmt.Errorf("Not implemented")
	case "nic":
		if d["nictype"] != "bridged" && d["nictype"] != "" {
			return nil, fmt.Errorf("Bad nic type: %s\n", d["nictype"])
		}
		var l1 = []string{"lxc.network.type", "veth"}
		var lines = [][]string{l1}
		var l2 []string
		if d["hwaddr"] != "" {
			l2 = []string{"lxc.network.hwaddr", d["hwaddr"]}
			lines = append(lines, l2)
		}
		if d["mtu"] != "" {
			l2 = []string{"lxc.network.mtu", d["mtu"]}
			lines = append(lines, l2)
		}
		if d["parent"] != "" {
			l2 = []string{"lxc.network.link", d["parent"]}
			lines = append(lines, l2)
		}
		if d["name"] != "" {
			l2 = []string{"lxc.network.name", d["name"]}
			lines = append(lines, l2)
		}
		return lines, nil
	case "disk":
		var p string
		if d["path"] == "/" || d["path"] == "" {
			p = ""
		} else if d["path"][0:1] == "/" {
			p = d["path"][1:]
		} else {
			p = d["path"]
		}
		/* TODO - check whether source is a disk, loopback, btrfs subvol, etc */
		/* for now we only handle directory bind mounts */
		source := d["source"]
		opts := "bind"
		if shared.IsDir(source) {
			opts = fmt.Sprintf("%s,create=dir", opts)
		} else {
			opts = fmt.Sprintf("%s,create=file", opts)
		}
		if d["readonly"] == "1" || d["readonly"] == "true" {
			opts = fmt.Sprintf("%s,ro", opts)
		}
		if d["optional"] == "1" || d["optional"] == "true" {
			opts = fmt.Sprintf("%s,optional", opts)
		}
		l := []string{"lxc.mount.entry", fmt.Sprintf("%s %s none %s 0 0", source, p, opts)}
		return [][]string{l}, nil
	case "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("Bad device type")
	}
}
