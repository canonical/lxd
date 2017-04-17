package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/olekukonko/tablewriter"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type column struct {
	Name           string
	Data           columnData
	NeedsState     bool
	NeedsSnapshots bool
}

type columnData func(api.Container, *api.ContainerState, []api.ContainerSnapshot) string

const (
	listFormatTable = "table"
	listFormatJSON  = "json"
)

type listCmd struct {
	chosenColumnRunes string
	fast              bool
	format            string
}

func (c *listCmd) showByDefault() bool {
	return true
}

func (c *listCmd) usage() string {
	return i18n.G(
		`Usage: lxc list [<remote>:] [filters] [--format table|json] [-c <columns>] [--fast]

List the existing containers.

Default column layout: ns46tS
Fast column layout: nsacPt

*Filters*
A single keyword like "web" which will list any container with a name starting by "web".

A regular expression on the container name. (e.g. .*web.*01$).

A key/value pair referring to a configuration item. For those, the namespace can be abbreviated to the smallest unambiguous identifier.
    - "user.blah=abc" will list all containers with the "blah" user property set to "abc".

    - "u.blah=abc" will do the same

    - "security.privileged=1" will list all privileged containers

    - "s.privileged=1" will do the same

A regular expression matching a configuration item or its value. (e.g. volatile.eth0.hwaddr=00:16:3e:.*).

*Columns*
The -c option takes a comma separated list of arguments that control
which container attributes to output when displaying in table format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:

    4 - IPv4 address

    6 - IPv6 address

    a - Architecture

    c - Creation date

    l - Last used date

    n - Name

    p - PID of the container's init process

    P - Profiles

    s - State

    S - Number of snapshots

    t - Type (persistent or ephemeral)

*Examples*
lxc list -c ns46
    Shows a list of containers using the "NAME", "STATE", "IPV4", "IPV6" columns.`)
}

func (c *listCmd) flags() {
	gnuflag.StringVar(&c.chosenColumnRunes, "c", "ns46tS", i18n.G("Columns"))
	gnuflag.StringVar(&c.chosenColumnRunes, "columns", "ns46tS", i18n.G("Columns"))
	gnuflag.StringVar(&c.format, "format", "table", i18n.G("Format (table|json)"))
	gnuflag.BoolVar(&c.fast, "fast", false, i18n.G("Fast mode (same as --columns=nsacPt)"))
}

// This seems a little excessive.
func (c *listCmd) dotPrefixMatch(short string, full string) bool {
	fullMembs := strings.Split(full, ".")
	shortMembs := strings.Split(short, ".")

	if len(fullMembs) != len(shortMembs) {
		return false
	}

	for i := range fullMembs {
		if !strings.HasPrefix(fullMembs[i], shortMembs[i]) {
			return false
		}
	}

	return true
}

func (c *listCmd) shouldShow(filters []string, state *api.Container) bool {
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
			for configKey, configValue := range state.ExpandedConfig {
				if c.dotPrefixMatch(key, configKey) {
					//try to test filter value as a regexp
					regexpValue := value
					if !(strings.Contains(value, "^") || strings.Contains(value, "$")) {
						regexpValue = "^" + regexpValue + "$"
					}
					r, err := regexp.Compile(regexpValue)
					//if not regexp compatible use original value
					if err != nil {
						if value == configValue {
							found = true
							break
						} else {
							// the property was found but didn't match
							return false
						}
					} else if r.MatchString(configValue) == true {
						found = true
						break
					}
				}
			}

			if state.ExpandedConfig[key] == value {
				return true
			}

			if !found {
				return false
			}
		} else {
			regexpValue := filter
			if !(strings.Contains(filter, "^") || strings.Contains(filter, "$")) {
				regexpValue = "^" + regexpValue + "$"
			}

			r, err := regexp.Compile(regexpValue)
			if err == nil && r.MatchString(state.Name) == true {
				return true
			}

			if !strings.HasPrefix(state.Name, filter) {
				return false
			}
		}
	}

	return true
}

func (c *listCmd) listContainers(d *lxd.Client, cinfos []api.Container, filters []string, columns []column) error {
	headers := []string{}
	for _, column := range columns {
		headers = append(headers, column.Name)
	}

	threads := 10
	if len(cinfos) < threads {
		threads = len(cinfos)
	}

	cStates := map[string]*api.ContainerState{}
	cStatesLock := sync.Mutex{}
	cStatesQueue := make(chan string, threads)
	cStatesWg := sync.WaitGroup{}

	cSnapshots := map[string][]api.ContainerSnapshot{}
	cSnapshotsLock := sync.Mutex{}
	cSnapshotsQueue := make(chan string, threads)
	cSnapshotsWg := sync.WaitGroup{}

	for i := 0; i < threads; i++ {
		cStatesWg.Add(1)
		go func() {
			d, err := lxd.NewClient(&d.Config, d.Name)
			if err != nil {
				cStatesWg.Done()
				return
			}

			for {
				cName, more := <-cStatesQueue
				if !more {
					break
				}

				state, err := d.ContainerState(cName)
				if err != nil {
					continue
				}

				cStatesLock.Lock()
				cStates[cName] = state
				cStatesLock.Unlock()
			}
			cStatesWg.Done()
		}()

		cSnapshotsWg.Add(1)
		go func() {
			d, err := lxd.NewClient(&d.Config, d.Name)
			if err != nil {
				cSnapshotsWg.Done()
				return
			}

			for {
				cName, more := <-cSnapshotsQueue
				if !more {
					break
				}

				snaps, err := d.ListSnapshots(cName)
				if err != nil {
					continue
				}

				cSnapshotsLock.Lock()
				cSnapshots[cName] = snaps
				cSnapshotsLock.Unlock()
			}
			cSnapshotsWg.Done()
		}()
	}

	for _, cInfo := range cinfos {
		for _, column := range columns {
			if column.NeedsState && cInfo.IsActive() {
				cStatesLock.Lock()
				_, ok := cStates[cInfo.Name]
				cStatesLock.Unlock()
				if ok {
					continue
				}

				cStatesLock.Lock()
				cStates[cInfo.Name] = nil
				cStatesLock.Unlock()

				cStatesQueue <- cInfo.Name
			}

			if column.NeedsSnapshots {
				cSnapshotsLock.Lock()
				_, ok := cSnapshots[cInfo.Name]
				cSnapshotsLock.Unlock()
				if ok {
					continue
				}

				cSnapshotsLock.Lock()
				cSnapshots[cInfo.Name] = nil
				cSnapshotsLock.Unlock()

				cSnapshotsQueue <- cInfo.Name
			}
		}
	}

	close(cStatesQueue)
	close(cSnapshotsQueue)
	cStatesWg.Wait()
	cSnapshotsWg.Wait()

	tableData := func() [][]string {
		data := [][]string{}
		for _, cInfo := range cinfos {
			if !c.shouldShow(filters, &cInfo) {
				continue
			}

			col := []string{}
			for _, column := range columns {
				col = append(col, column.Data(cInfo, cStates[cInfo.Name], cSnapshots[cInfo.Name]))
			}
			data = append(data, col)
		}

		sort.Sort(byName(data))
		return data
	}

	switch c.format {
	case listFormatTable:
		table := tablewriter.NewWriter(os.Stdout)
		table.SetAutoWrapText(false)
		table.SetAlignment(tablewriter.ALIGN_LEFT)
		table.SetRowLine(true)
		table.SetHeader(headers)
		table.AppendBulk(tableData())
		table.Render()
	case listFormatJSON:
		data := make([]listContainerItem, len(cinfos))
		for i := range cinfos {
			data[i].Container = &cinfos[i]
			data[i].State = cStates[cinfos[i].Name]
			data[i].Snapshots = cSnapshots[cinfos[i].Name]
		}
		enc := json.NewEncoder(os.Stdout)
		err := enc.Encode(data)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid format %q", c.format)
	}

	return nil
}

type listContainerItem struct {
	*api.Container

	State     *api.ContainerState     `json:"state" yaml:"state"`
	Snapshots []api.ContainerSnapshot `json:"snapshots" yaml:"snapshots"`
}

func (c *listCmd) run(config *lxd.Config, args []string) error {
	var remote string
	name := ""

	filters := []string{}

	if len(args) != 0 {
		filters = args
		if strings.Contains(args[0], ":") && !strings.Contains(args[0], "=") {
			remote, name = config.ParseRemoteAndContainer(args[0])
			filters = args[1:]
		} else if !strings.Contains(args[0], "=") {
			remote = config.DefaultRemote
			name = args[0]
		}
	}
	filters = append(filters, name)

	if remote == "" {
		remote = config.DefaultRemote
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	var cts []api.Container
	ctslist, err := d.ListContainers()
	if err != nil {
		return err
	}

	for _, cinfo := range ctslist {
		if !c.shouldShow(filters, &cinfo) {
			continue
		}

		cts = append(cts, cinfo)
	}

	columns_map := map[rune]column{
		'4': {i18n.G("IPV4"), c.IP4ColumnData, true, false},
		'6': {i18n.G("IPV6"), c.IP6ColumnData, true, false},
		'a': {i18n.G("ARCHITECTURE"), c.ArchitectureColumnData, false, false},
		'c': {i18n.G("CREATED AT"), c.CreatedColumnData, false, false},
		'n': {i18n.G("NAME"), c.nameColumnData, false, false},
		'p': {i18n.G("PID"), c.PIDColumnData, true, false},
		'P': {i18n.G("PROFILES"), c.ProfilesColumnData, false, false},
		'S': {i18n.G("SNAPSHOTS"), c.numberSnapshotsColumnData, false, true},
		's': {i18n.G("STATE"), c.statusColumnData, false, false},
		't': {i18n.G("TYPE"), c.typeColumnData, false, false},
	}

	if c.fast {
		c.chosenColumnRunes = "nsacPt"
	}

	columns := []column{}
	for _, columnRune := range c.chosenColumnRunes {
		if column, ok := columns_map[columnRune]; ok {
			columns = append(columns, column)
		} else {
			return fmt.Errorf("%s does contain invalid column characters\n", c.chosenColumnRunes)
		}
	}

	return c.listContainers(d, cts, filters, columns)
}

func (c *listCmd) nameColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	return cInfo.Name
}

func (c *listCmd) statusColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	return strings.ToUpper(cInfo.Status)
}

func (c *listCmd) IP4ColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	if cInfo.IsActive() && cState != nil && cState.Network != nil {
		ipv4s := []string{}
		for netName, net := range cState.Network {
			if net.Type == "loopback" {
				continue
			}

			for _, addr := range net.Addresses {
				if shared.StringInSlice(addr.Scope, []string{"link", "local"}) {
					continue
				}

				if addr.Family == "inet" {
					ipv4s = append(ipv4s, fmt.Sprintf("%s (%s)", addr.Address, netName))
				}
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(ipv4s)))
		return strings.Join(ipv4s, "\n")
	} else {
		return ""
	}
}

func (c *listCmd) IP6ColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	if cInfo.IsActive() && cState != nil && cState.Network != nil {
		ipv6s := []string{}
		for netName, net := range cState.Network {
			if net.Type == "loopback" {
				continue
			}

			for _, addr := range net.Addresses {
				if shared.StringInSlice(addr.Scope, []string{"link", "local"}) {
					continue
				}

				if addr.Family == "inet6" {
					ipv6s = append(ipv6s, fmt.Sprintf("%s (%s)", addr.Address, netName))
				}
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(ipv6s)))
		return strings.Join(ipv6s, "\n")
	} else {
		return ""
	}
}

func (c *listCmd) typeColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	if cInfo.Ephemeral {
		return i18n.G("EPHEMERAL")
	} else {
		return i18n.G("PERSISTENT")
	}
}

func (c *listCmd) numberSnapshotsColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	if cSnaps != nil {
		return fmt.Sprintf("%d", len(cSnaps))
	}

	return ""
}

func (c *listCmd) PIDColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	if cInfo.IsActive() && cState != nil {
		return fmt.Sprintf("%d", cState.Pid)
	}

	return ""
}

func (c *listCmd) ArchitectureColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	return cInfo.Architecture
}

func (c *listCmd) ProfilesColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	return strings.Join(cInfo.Profiles, "\n")
}

func (c *listCmd) CreatedColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	layout := "2006/01/02 15:04 UTC"

	if shared.TimeIsSet(cInfo.CreatedAt) {
		return cInfo.CreatedAt.UTC().Format(layout)
	}

	return ""
}
