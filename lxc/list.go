package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/gosexy/gettext"
	"github.com/olekukonko/tablewriter"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

type listCmd struct{}

func (c *listCmd) showByDefault() bool {
	return true
}

func (c *listCmd) usage() string {
	return gettext.Gettext(
		"Lists the available resources.\n" +
			"\n" +
			"lxc list [resource] [filters]\n" +
			"\n" +
			"The filters are:\n" +
			"* A single keyword like \"web\" which will list any container with \"web\" in its name.\n" +
			"* A key/value pair referring to a configuration item. For those, the namespace can be abreviated to the smallest unambiguous identifier:\n" +
			"* \"user.blah=abc\" will list all containers with the \"blah\" user property set to \"abc\"\n" +
			"* \"u.blah=abc\" will do the same\n" +
			"* \"security.privileged=1\" will list all privileged containers\n" +
			"* \"s.privileged=1\" will do the same\n")
}

func (c *listCmd) flags() {}

// This seems a little excessive.
func dotPrefixMatch(short string, full string) bool {
	fullMembs := strings.Split(full, ".")
	shortMembs := strings.Split(short, ".")

	if len(fullMembs) != len(shortMembs) {
		return false
	}

	for i, _ := range fullMembs {
		if !strings.HasPrefix(fullMembs[i], shortMembs[i]) {
			return false
		}
	}

	return true
}

func shouldShow(filters []string, state *shared.ContainerState) bool {
	for _, filter := range filters {
		if strings.Contains(filter, "=") {
			membs := strings.SplitN(filter, "=", 2)

			key := membs[0]
			var value string
			if len(membs) < 2 {
				value = ""
			} else {
				value = membs[1]
			}

			found := false
			for configKey, configValue := range state.Config {
				if dotPrefixMatch(key, configKey) {
					if value == configValue {
						found = true
						break
					} else {
						// the property was found but didn't match
						return false
					}
				}
			}

			if !found {
				return false
			}
		} else {
			if !strings.Contains(state.Name, filter) {
				return false
			}
		}
	}

	return true
}

func listContainers(cinfos []shared.ContainerInfo, filters []string, listsnaps bool) error {
	data := [][]string{}

	for _, cinfo := range cinfos {
		cstate := cinfo.State
		d := []string{cstate.Name, cstate.Status.State}

		if !shouldShow(filters, &cstate) {
			continue
		}

		if cstate.Status.State == "RUNNING" {
			ipv4s := []string{}
			ipv6s := []string{}
			for _, ip := range cstate.Status.Ips {
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
		if cstate.Ephemeral {
			d = append(d, "YES")
		} else {
			d = append(d, "NO")
		}
		// List snapshots
		csnaps := cinfo.Snaps
		d = append(d, fmt.Sprintf("%d", len(csnaps)))

		data = append(data, d)
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"NAME", "STATE", "IPV4", "IPV6", "EPHEMERAL", "SNAPSHOTS"})

	for _, v := range data {
		table.Append(v)
	}

	table.Render() // Send output

	if listsnaps && len(cinfos) == 1 {
		csnaps := cinfos[0].Snaps
		first_snapshot := true
		for _, snap := range csnaps {
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
	var remote string
	name := ""

	filters := []string{}

	if len(args) == 0 {
		remote = config.DefaultRemote
	} else {
		filters = args
		if strings.Contains(args[0], ":") {
			remote, name = config.ParseRemoteAndContainer(args[0])
			filters = args[1:]
		} else if !strings.Contains(args[0], "=") {
			remote = config.DefaultRemote
			name = args[0]
		}
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	var cts []shared.ContainerInfo
	ctslist, err := d.ListContainers()
	if err != nil {
		return err
	}

	if name == "" {
		cts = ctslist
	} else {
		for _, cinfo := range ctslist {
			if len(cinfo.State.Name) >= len(name) && cinfo.State.Name[0:len(name)] == name {
				cts = append(cts, cinfo)
			}
		}
	}

	return listContainers(cts, filters, len(cts) == 1)
}
