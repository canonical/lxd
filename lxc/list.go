package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/chai2010/gettext-go/gettext"
	"github.com/olekukonko/tablewriter"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

type ByName [][]string

func (a ByName) Len() int {
	return len(a)
}

func (a ByName) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a ByName) Less(i, j int) bool {
	return a[i][0] < a[j][0]
}

type listCmd struct{}

func (c *listCmd) showByDefault() bool {
	return true
}

func (c *listCmd) usage() string {
	return gettext.Gettext(
		`Lists the available resources.

lxc list [resource] [filters]

The filters are:
* A single keyword like "web" which will list any container with "web" in its name.
* A key/value pair referring to a configuration item. For those, the namespace can be abreviated to the smallest unambiguous identifier:
* "user.blah=abc" will list all containers with the "blah" user property set to "abc"
* "u.blah=abc" will do the same
* "security.privileged=1" will list all privileged containers
* "s.privileged=1" will do the same`)
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
		d := []string{cstate.Name, strings.ToUpper(cstate.Status.Status)}

		if !shouldShow(filters, &cstate) {
			continue
		}

		if cstate.Status.StatusCode == shared.Running {
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
	sort.Sort(ByName(data))
	table.AppendBulk(data)
	table.Render()

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
