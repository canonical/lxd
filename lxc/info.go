package main

import (
	"fmt"
	"io/ioutil"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type infoCmd struct {
	showLog bool
}

func (c *infoCmd) showByDefault() bool {
	return true
}

func (c *infoCmd) usage() string {
	return i18n.G(
		`List information on containers.

This will support remotes and images as well, but only containers for now.

lxc info [<remote>:]container [--show-log]`)
}

func (c *infoCmd) flags() {
	gnuflag.BoolVar(&c.showLog, "show-log", false, i18n.G("Show the container's last 100 log lines?"))
}

func (c *infoCmd) run(config *lxd.Config, args []string) error {
	var remote string
	var cName string
	if len(args) == 1 {
		remote, cName = config.ParseRemoteAndContainer(args[0])
	} else {
		remote, cName = config.ParseRemoteAndContainer("")
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	if cName == "" {
		return c.remoteInfo(d)
	} else {
		return c.containerInfo(d, cName, c.showLog)
	}
}

func (c *infoCmd) remoteInfo(d *lxd.Client) error {
	serverStatus, err := d.ServerStatus()
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&serverStatus)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

func (c *infoCmd) containerInfo(d *lxd.Client, name string, showLog bool) error {
	ct, err := d.ContainerInfo(name)
	if err != nil {
		return err
	}

	cs, err := d.ContainerState(name)
	if err != nil {
		return err
	}

	const layout = "2006/01/02 15:04 UTC"

	fmt.Printf(i18n.G("Name: %s")+"\n", ct.Name)
	fmt.Printf(i18n.G("Architecture: %s")+"\n", ct.Architecture)
	if ct.CreationDate.UTC().Unix() != 0 {
		fmt.Printf(i18n.G("Created: %s")+"\n", ct.CreationDate.UTC().Format(layout))
	}

	fmt.Printf(i18n.G("Status: %s")+"\n", ct.Status)
	if ct.Ephemeral {
		fmt.Printf(i18n.G("Type: ephemeral") + "\n")
	} else {
		fmt.Printf(i18n.G("Type: persistent") + "\n")
	}
	fmt.Printf(i18n.G("Profiles: %s")+"\n", strings.Join(ct.Profiles, ", "))
	if cs.Pid != 0 {
		fmt.Printf(i18n.G("Pid: %d")+"\n", cs.Pid)
		fmt.Printf(i18n.G("Processes: %d")+"\n", cs.Processes)

		ipInfo := ""
		for netName, net := range cs.Network {
			vethStr := ""
			if net.HostName != "" {
				vethStr = fmt.Sprintf("\t%s", net.HostName)
			}

			for _, addr := range net.Addresses {
				ipInfo += fmt.Sprintf("  %s:\t%s\t%s%s\n", netName, addr.Family, addr.Address, vethStr)
			}
		}

		if ipInfo != "" {
			fmt.Printf(i18n.G("Ips:") + "\n")
			fmt.Printf(ipInfo)
		}
	}

	// List snapshots
	first_snapshot := true
	snaps, err := d.ListSnapshots(name)
	if err != nil {
		return nil
	}

	for _, snap := range snaps {
		if first_snapshot {
			fmt.Println(i18n.G("Snapshots:"))
		}
		fmt.Printf("  %s", snap.Name)

		if snap.CreationDate.UTC().Unix() != 0 {
			fmt.Printf(" ("+i18n.G("taken at %s")+")", snap.CreationDate.UTC().Format(layout))
		}

		if snap.Stateful {
			fmt.Printf(" (" + i18n.G("stateful") + ")")
		} else {
			fmt.Printf(" (" + i18n.G("stateless") + ")")
		}
		fmt.Printf("\n")

		first_snapshot = false
	}

	if showLog {
		log, err := d.GetLog(name, "lxc.log")
		if err != nil {
			return err
		}

		stuff, err := ioutil.ReadAll(log)
		if err != nil {
			return err
		}

		fmt.Printf("\n"+i18n.G("Log:")+"\n\n%s\n", string(stuff))
	}

	return nil
}
