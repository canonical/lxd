package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/olekukonko/tablewriter"
)

type listCmd struct{}

func (c *listCmd) showByDefault() bool {
	return true
}

func (c *listCmd) usage() string {
	return gettext.Gettext(
		"Lists the available resources.\n" +
			"\n" +
			"lxc list [resource]\n" +
			"\n" +
			"Currently resource must be a defined remote, and list only lists\n" +
			"the defined containers.\n")

}

func (c *listCmd) flags() {}

func listContainers(d *lxd.Client, cts []string, showsnaps bool) error {
	data := [][]string{}

	for _, ct := range cts {
		// get more information
		c, err := d.ContainerStatus(ct)
		d := []string{}
		if err == nil {
			d = []string{ct, c.Status.State}
		} else if err == lxd.LXDErrors[http.StatusNotFound] {
			return fmt.Errorf(gettext.Gettext("Container not found"))
		} else {
			return err
		}

		if c.Status.State == "RUNNING" {
			ipv4s := []string{}
			ipv6s := []string{}
			for _, ip := range c.Status.Ips {
				if ip.Interface == "lo" {
					continue
				}

				if ip.Protocol == "IPV6" {
					ipv6s = append(ipv6s, ip.Address)
				} else {
					ipv4s = append(ipv4s, ip.Address)
				}
			}
			ipv4 := strings.Join(ipv4s, ", ")
			ipv6 := strings.Join(ipv6s, ", ")
			d = append(d, ipv4)
			d = append(d, ipv6)
		} else {
			d = append(d, "")
			d = append(d, "")
		}
		data = append(data, d)
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"NAME", "STATE", "IPV4", "IPV6"})

	for _, v := range data {
		table.Append(v)
	}

	table.Render() // Send output

	if showsnaps {
		cName := cts[0]
		first_snapshot := true
		snaps, err := d.ListSnapshots(cName)
		if err != nil {
			return nil
		}
		for _, snap := range snaps {
			if first_snapshot {
				fmt.Printf("Snapshots:\n")
			}
			fmt.Printf("  %s\n", snap)
			first_snapshot = false
		}
	}
	return nil
}

func (c *listCmd) run(config *lxd.Config, args []string) error {
	if len(args) > 1 {
		return errArgs
	}

	var remote string
	var name string
	if len(args) == 1 {
		remote, name = config.ParseRemoteAndContainer(args[0])
	} else {
		remote = config.DefaultRemote
		name = ""
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	var cts []string
	showsnapshots := false
	if name == "" {
		cts, err = d.ListContainers()
		if err != nil {
			return err
		}
	} else {
		showsnapshots = true
		cts = []string{name}
	}

	return listContainers(d, cts, showsnapshots)
}
