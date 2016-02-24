package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/olekukonko/tablewriter"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type Column struct {
	Name           string
	Data           columnData
	NeedsState     bool
	NeedsSnapshots bool
}

type columnData func(shared.ContainerInfo, *shared.ContainerState, []shared.SnapshotInfo) string

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

type listCmd struct {
	chosenColumnRunes string
	fast              bool
}

func (c *listCmd) showByDefault() bool {
	return true
}

func (c *listCmd) usage() string {
	return i18n.G(
		`Lists the available resources.

lxc list [resource] [filters] [-c columns] [--fast]

The filters are:
* A single keyword like "web" which will list any container with "web" in its name.
* A key/value pair referring to a configuration item. For those, the namespace can be abreviated to the smallest unambiguous identifier:
* "user.blah=abc" will list all containers with the "blah" user property set to "abc"
* "u.blah=abc" will do the same
* "security.privileged=1" will list all privileged containers
* "s.privileged=1" will do the same

The columns are:
* 4 - IPv4 address
* 6 - IPv6 address
* a - architecture
* c - creation date
* n - name
* p - pid of container init process
* P - profiles
* s - state
* t - type (persistent or ephemeral)

Default column layout: ns46tS
Fast column layout: nsacPt`)
}

func (c *listCmd) flags() {
	gnuflag.StringVar(&c.chosenColumnRunes, "c", "ns46tS", i18n.G("Columns"))
	gnuflag.StringVar(&c.chosenColumnRunes, "columns", "ns46tS", i18n.G("Columns"))
	gnuflag.BoolVar(&c.fast, "fast", false, i18n.G("Fast mode (same as --columns=nsacPt"))
}

// This seems a little excessive.
func (c *listCmd) dotPrefixMatch(short string, full string) bool {
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

func (c *listCmd) shouldShow(filters []string, state *shared.ContainerInfo) bool {
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

func (c *listCmd) listContainers(d *lxd.Client, cinfos []shared.ContainerInfo, filters []string, columns []Column) error {
	headers := []string{}
	for _, column := range columns {
		headers = append(headers, column.Name)
	}

	threads := 10
	if len(cinfos) < threads {
		threads = len(cinfos)
	}

	cStates := map[string]*shared.ContainerState{}
	cStatesLock := sync.Mutex{}
	cStatesQueue := make(chan string, threads)
	cStatesWg := sync.WaitGroup{}

	cSnapshots := map[string][]shared.SnapshotInfo{}
	cSnapshotsLock := sync.Mutex{}
	cSnapshotsQueue := make(chan string, threads)
	cSnapshotsWg := sync.WaitGroup{}

	for i := 0; i < threads; i++ {
		cStatesWg.Add(1)
		go func() {
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
		if !c.shouldShow(filters, &cInfo) {
			continue
		}

		for _, column := range columns {
			if column.NeedsState && cInfo.StatusCode != shared.Stopped {
				_, ok := cStates[cInfo.Name]
				if ok {
					continue
				}

				cStatesLock.Lock()
				cStates[cInfo.Name] = nil
				cStatesLock.Unlock()

				cStatesQueue <- cInfo.Name
			}

			if column.NeedsSnapshots {
				_, ok := cSnapshots[cInfo.Name]
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

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetRowLine(true)
	table.SetHeader(headers)
	sort.Sort(ByName(data))
	table.AppendBulk(data)
	table.Render()

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
			if len(cinfo.Name) >= len(name) && cinfo.Name[0:len(name)] == name {
				cts = append(cts, cinfo)
			}
		}
	}

	columns_map := map[rune]Column{
		'4': Column{i18n.G("IPV4"), c.IP4ColumnData, true, false},
		'6': Column{i18n.G("IPV6"), c.IP6ColumnData, true, false},
		'a': Column{i18n.G("ARCHITECTURE"), c.ArchitectureColumnData, false, false},
		'c': Column{i18n.G("CREATED AT"), c.CreatedColumnData, false, false},
		'n': Column{i18n.G("NAME"), c.nameColumnData, false, false},
		'p': Column{i18n.G("PID"), c.PIDColumnData, true, false},
		'P': Column{i18n.G("PROFILES"), c.ProfilesColumnData, false, false},
		'S': Column{i18n.G("SNAPSHOTS"), c.numberSnapshotsColumnData, false, true},
		's': Column{i18n.G("STATE"), c.statusColumnData, false, false},
		't': Column{i18n.G("TYPE"), c.typeColumnData, false, false},
	}

	if c.fast {
		c.chosenColumnRunes = "nsacPt"
	}

	columns := []Column{}
	for _, columnRune := range c.chosenColumnRunes {
		if column, ok := columns_map[columnRune]; ok {
			columns = append(columns, column)
		} else {
			return fmt.Errorf("%s does contain invalid column characters\n", c.chosenColumnRunes)
		}
	}

	return c.listContainers(d, cts, filters, columns)
}

func (c *listCmd) nameColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	return cInfo.Name
}

func (c *listCmd) statusColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	return strings.ToUpper(cInfo.Status)
}

func (c *listCmd) IP4ColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	if cInfo.StatusCode != shared.Stopped {
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
		return strings.Join(ipv4s, "\n")
	} else {
		return ""
	}
}

func (c *listCmd) IP6ColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	if cInfo.StatusCode != shared.Stopped {
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
		return strings.Join(ipv6s, "\n")
	} else {
		return ""
	}
}

func (c *listCmd) typeColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	if cInfo.Ephemeral {
		return i18n.G("EPHEMERAL")
	} else {
		return i18n.G("PERSISTENT")
	}
}

func (c *listCmd) numberSnapshotsColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	return fmt.Sprintf("%d", len(cSnaps))
}

func (c *listCmd) PIDColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	if cState.Pid != 0 {
		return fmt.Sprintf("%d", cState.Pid)
	} else {
		return ""
	}
}

func (c *listCmd) ArchitectureColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	return cInfo.Architecture
}

func (c *listCmd) ProfilesColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	return strings.Join(cInfo.Profiles, "\n")
}

func (c *listCmd) CreatedColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	layout := "2006/01/02 15:04 UTC"

	if cInfo.CreationDate.UTC().Unix() != 0 {
		return cInfo.CreationDate.UTC().Format(layout)
	}

	return ""
}
