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
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type column struct {
	Name           string
	Data           columnData
	NeedsState     bool
	NeedsSnapshots bool
}

type columnData func(shared.ContainerInfo, *shared.ContainerState, []shared.SnapshotInfo) string

type byName [][]string

func (a byName) Len() int {
	return len(a)
}

func (a byName) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a byName) Less(i, j int) bool {
	if a[i][0] == "" {
		return false
	}

	if a[j][0] == "" {
		return true
	}

	return a[i][0] < a[j][0]
}

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
		`Lists the available resources.

lxc list [resource] [filters] [--format table|json] [-c columns] [--fast]

The filters are:
* A single keyword like "web" which will list any container with a name starting by "web".
* A regular expression on the container name. (e.g. .*web.*01$)
* A key/value pair referring to a configuration item. For those, the namespace can be abreviated to the smallest unambiguous identifier:
 * "user.blah=abc" will list all containers with the "blah" user property set to "abc".
 * "u.blah=abc" will do the same
 * "security.privileged=1" will list all privileged containers
 * "s.privileged=1" will do the same
* A regular expression matching a configuration item or its value. (e.g. volatile.eth0.hwaddr=00:16:3e:.*)

Columns for table format are:
* 4 - IPv4 address
* 6 - IPv6 address
* a - architecture
* c - creation date
* l - last used date
* n - name
* p - pid of container init process
* P - profiles
* s - state
* S - number of snapshots
* t - type (persistent or ephemeral)
* b - base image

Default column layout: ns46tS
Fast column layout: nsacPtb`)
}

const fastColumns string = "nsacPtb"

func (c *listCmd) flags() {
	gnuflag.StringVar(&c.chosenColumnRunes, "c", "ns46tS", i18n.G("Columns"))
	gnuflag.StringVar(&c.chosenColumnRunes, "columns", "ns46tS", i18n.G("Columns"))
	gnuflag.StringVar(&c.format, "format", "table", i18n.G("Format"))
	gnuflag.BoolVar(&c.fast, "fast", false, fmt.Sprintf(i18n.G("Fast mode (same as --columns=%s)"), fastColumns))
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

func (c *listCmd) listContainers(d *lxd.Client, cinfos []shared.ContainerInfo, filters []string, columns []column) error {
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
		for _, column := range columns {
			if column.NeedsState && cInfo.IsActive() {
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

	switch c.format {
	case listFormatTable:
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
		table.SetAlignment(tablewriter.ALIGN_LEFT)
		table.SetRowLine(true)
		table.SetHeader(headers)
		sort.Sort(byName(data))
		table.AppendBulk(data)
		table.Render()
	case listFormatJSON:
		data := make([]listContainerItem, len(cinfos))
		for i := range cinfos {
			data[i].ContainerInfo = &cinfos[i]
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
	*shared.ContainerInfo
	State     *shared.ContainerState `json:"state"`
	Snapshots []shared.SnapshotInfo  `json:"snapshots"`
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

	var cts []shared.ContainerInfo
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
		'4': column{i18n.G("IPV4"), c.IP4ColumnData, true, false},
		'6': column{i18n.G("IPV6"), c.IP6ColumnData, true, false},
		'a': column{i18n.G("ARCHITECTURE"), c.ArchitectureColumnData, false, false},
		'c': column{i18n.G("CREATED AT"), c.CreatedColumnData, false, false},
		'l': column{i18n.G("LAST USED AT"), c.LastUsedColumnData, false, false},
		'n': column{i18n.G("NAME"), c.nameColumnData, false, false},
		'p': column{i18n.G("PID"), c.PIDColumnData, true, false},
		'P': column{i18n.G("PROFILES"), c.ProfilesColumnData, false, false},
		'S': column{i18n.G("SNAPSHOTS"), c.numberSnapshotsColumnData, false, true},
		's': column{i18n.G("STATE"), c.statusColumnData, false, false},
		't': column{i18n.G("TYPE"), c.typeColumnData, false, false},
		'b': column{i18n.G("BASE IMAGE"), c.baseImageColumnData, false, false},
	}

	if c.fast {
		c.chosenColumnRunes = fastColumns
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

func (c *listCmd) nameColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	return cInfo.Name
}

func (c *listCmd) statusColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	return strings.ToUpper(cInfo.Status)
}

func (c *listCmd) IP4ColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
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
		return strings.Join(ipv4s, "\n")
	} else {
		return ""
	}
}

func (c *listCmd) IP6ColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
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
	if cSnaps != nil {
		return fmt.Sprintf("%d", len(cSnaps))
	}

	return ""
}

func (c *listCmd) PIDColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	if cInfo.IsActive() && cState != nil {
		return fmt.Sprintf("%d", cState.Pid)
	}

	return ""
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

func (c *listCmd) LastUsedColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	layout := "2006/01/02 15:04 UTC"

	if !cInfo.LastUsedDate.IsZero() && cInfo.LastUsedDate.UTC().Unix() != 0 {
		return cInfo.LastUsedDate.UTC().Format(layout)
	}

	return ""
}

func (c *listCmd) baseImageColumnData(cInfo shared.ContainerInfo, cState *shared.ContainerState, cSnaps []shared.SnapshotInfo) string {
	if bi, ok := cInfo.Config["volatile.base_image"]; ok {
		// Truncate the hash ID to the same length as the column title,
		// i.e. print as many characters as possible without increasing
		// the width of the column.
		if len(bi) >= len("BASE IMAGE") {
			return bi[:len("BASE IMAGE")]
		}
		return bi
	}
	return ""
}
