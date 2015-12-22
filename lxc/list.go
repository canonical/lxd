package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/olekukonko/tablewriter"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/i18n"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
)

type Column struct {
	Name string
	Data columnData
}

type columnData func(shared.ContainerInfo) string

type ByName [][]string

func (a ByName) Len() int {
	return len(a)
}

func (a ByName) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a ByName) Less(i, j int) bool {
	if a[i][0] == "" {
		return false
	}

	if a[j][0] == "" {
		return true
	}

	return a[i][0] < a[j][0]
}

type listCmd struct{}

func (c *listCmd) showByDefault() bool {
	return true
}

func (c *listCmd) usage() string {
	return i18n.G(
		`Lists the available resources.

lxc list [resource] [filters] -c [columns]

The filters are:
* A single keyword like "web" which will list any container with "web" in its name.
* A key/value pair referring to a configuration item. For those, the namespace can be abreviated to the smallest unambiguous identifier:
* "user.blah=abc" will list all containers with the "blah" user property set to "abc"
* "u.blah=abc" will do the same
* "security.privileged=1" will list all privileged containers
* "s.privileged=1" will do the same

The columns are:
* n - name
* s - state
* 4 - IP4
* 6 - IP6
* e - ephemeral
* S - snapshots
* p - pid of container init process`)
}

var chosenColumnRunes string

func (c *listCmd) flags() {
	gnuflag.StringVar(&chosenColumnRunes, "c", "ns46eS", i18n.G("Columns"))
}

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

func listContainers(cinfos []shared.ContainerInfo, filters []string, columns []Column, listsnaps bool) error {
	headers := []string{}
	for _, column := range columns {
		headers = append(headers, column.Name)
	}

	data := [][]string{}
	for _, cinfo := range cinfos {
		if !shouldShow(filters, &cinfo.State) {
			continue
		}
		d := []string{}
		for _, column := range columns {
			d = append(d, column.Data(cinfo))
		}
		data = append(data, d)
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetRowLine(true)
	table.SetHeader(headers)
	sort.Sort(ByName(data))
	table.AppendBulk(data)
	table.Render()

	if listsnaps && len(cinfos) == 1 {
		csnaps := cinfos[0].Snaps
		first_snapshot := true
		for _, snap := range csnaps {
			if first_snapshot {
				fmt.Println(i18n.G("Snapshots:"))
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

	if len(args) != 0 {
		filters = args
		if strings.Contains(args[0], ":") {
			remote, name = config.ParseRemoteAndContainer(args[0])
			filters = args[1:]
		} else if !strings.Contains(args[0], "=") {
			remote = config.DefaultRemote
			name = args[0]
		}
	}

	if remote == "" {
		remote = config.DefaultRemote
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

	columns_map := map[rune]Column{
		'n': Column{i18n.G("NAME"), nameColumnData},
		's': Column{i18n.G("STATE"), statusColumnData},
		'4': Column{i18n.G("IPV4"), IP4ColumnData},
		'6': Column{i18n.G("IPV6"), IP6ColumnData},
		'e': Column{i18n.G("EPHEMERAL"), isEphemeralColumnData},
		'S': Column{i18n.G("SNAPSHOTS"), numberSnapshotsColumnData},
		'p': Column{i18n.G("PID"), PIDColumnData},
	}

	columns := []Column{}
	for _, columnRune := range chosenColumnRunes {
		if column, ok := columns_map[columnRune]; ok {
			columns = append(columns, column)
		} else {
			return fmt.Errorf("%s does contain invalid column characters\n", chosenColumnRunes)
		}
	}

	return listContainers(cts, filters, columns, len(cts) == 1)
}

func nameColumnData(cinfo shared.ContainerInfo) string {
	return cinfo.State.Name
}

func statusColumnData(cinfo shared.ContainerInfo) string {
	return strings.ToUpper(cinfo.State.Status.Status)
}

func IP4ColumnData(cinfo shared.ContainerInfo) string {
	if cinfo.State.Status.StatusCode == shared.Running || cinfo.State.Status.StatusCode == shared.Frozen {
		ipv4s := []string{}
		for _, ip := range cinfo.State.Status.Ips {
			if ip.Interface == "lo" {
				continue
			}

			if ip.Protocol == "IPV4" {
				ipv4s = append(ipv4s, fmt.Sprintf("%s (%s)", ip.Address, ip.Interface))
			}
		}
		return strings.Join(ipv4s, "\n")
	} else {
		return ""
	}
}

func IP6ColumnData(cinfo shared.ContainerInfo) string {
	if cinfo.State.Status.StatusCode == shared.Running || cinfo.State.Status.StatusCode == shared.Frozen {
		ipv6s := []string{}
		for _, ip := range cinfo.State.Status.Ips {
			if ip.Interface == "lo" {
				continue
			}

			if ip.Protocol == "IPV6" {
				ipv6s = append(ipv6s, fmt.Sprintf("%s (%s)", ip.Address, ip.Interface))
			}
		}
		return strings.Join(ipv6s, "\n")
	} else {
		return ""
	}
}

func isEphemeralColumnData(cinfo shared.ContainerInfo) string {
	if cinfo.State.Ephemeral {
		return i18n.G("YES")
	} else {
		return i18n.G("NO")
	}
}

func numberSnapshotsColumnData(cinfo shared.ContainerInfo) string {
	return fmt.Sprintf("%d", len(cinfo.Snaps))
}

func PIDColumnData(cinfo shared.ContainerInfo) string {
	if cinfo.State.Status.Init != 0 {
		return fmt.Sprintf("%d", cinfo.State.Status.Init)
	} else {
		return ""
	}
}
