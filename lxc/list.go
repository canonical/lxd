package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
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

func listContainers(d *lxd.Client, cts []string, filters []string, showsnaps bool) error {
	data := [][]string{}

	for _, ct := range cts {
		// get more information
		c, err := d.ContainerStatus(ct)
		d := []string{}
		if err == nil {
			d = []string{ct, c.Status.State}
		} else if err == lxd.LXDErrors[http.StatusNotFound] {
			continue
		} else {
			return err
		}

		if !shouldShow(filters, c) {
			continue
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
		if c.Ephemeral {
			d = append(d, "YES")
		} else {
			d = append(d, "NO")
		}
		data = append(data, d)
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"NAME", "STATE", "IPV4", "IPV6", "EPHEMERAL"})

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
	var remote string
	var name string

	filters := []string{}

	if len(args) == 0 {
		remote = config.DefaultRemote
		name = ""
	} else {
		filters = args
		if strings.Contains(args[0], ":") {
			remote, name = config.ParseRemoteAndContainer(args[0])
			filters = args[1:]
		} else if !strings.Contains(args[0], "=") {
			remote = config.DefaultRemote
			name = ""
		}
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	var cts []string
	if name == "" {
		cts, err = d.ListContainers()
		if err != nil {
			return err
		}
	} else {
		cts = []string{name}
	}

	return listContainers(d, cts, filters, len(cts) == 1)
}
