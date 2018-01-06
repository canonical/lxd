package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxc/config"
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

type listCmd struct {
	columnsRaw string
	fast       bool
	format     string
}

func (c *listCmd) showByDefault() bool {
	return true
}

func (c *listCmd) usage() string {
	return i18n.G(
		`Usage: lxc list [<remote>:] [filters] [--format csv|json|table|yaml] [-c <columns>] [--fast]

List the existing containers.

Default column layout: ns46tS
Fast column layout: nsacPt

*Filters*
A single keyword like "web" which will list any container with a name starting by "web".

A regular expression on the container name. (e.g. .*web.*01$).

A key/value pair referring to a configuration item. For those, the namespace can be abbreviated to the smallest unambiguous identifier.
	- "user.blah=abc" will list all containers with the "blah" user property set to "abc".

	- "u.blah=abc" will do the same

	- "security.privileged=true" will list all privileged containers

	- "s.privileged=true" will do the same

A regular expression matching a configuration item or its value. (e.g. volatile.eth0.hwaddr=00:16:3e:.*).

*Columns*
The -c option takes a comma separated list of arguments that control
which container attributes to output when displaying in table or csv
format.

Column arguments are either pre-defined shorthand chars (see below),
or (extended) config keys.

Commas between consecutive shorthand chars are optional.

Pre-defined column shorthand chars:

	4 - IPv4 address

	6 - IPv6 address

	a - Architecture

	b - Storage pool

	c - Creation date

	d - Description

	l - Last used date

	n - Name

	N - Number of Processes

	p - PID of the container's init process

	P - Profiles

	s - State

	S - Number of snapshots

	t - Type (persistent or ephemeral)

Custom columns are defined with "key[:name][:maxWidth]":

	KEY: The (extended) config key to display

	NAME: Name to display in the column header.
	Defaults to the key if not specified or empty.

	MAXWIDTH: Max width of the column (longer results are truncated).
	Defaults to -1 (unlimited). Use 0 to limit to the column header size.

*Examples*
lxc list -c n,volatile.base_image:"BASE IMAGE":0,s46,volatile.eth0.hwaddr:MAC
	Shows a list of containers using the "NAME", "BASE IMAGE", "STATE", "IPV4",
	"IPV6" and "MAC" columns.

	"BASE IMAGE" and "MAC" are custom columns generated from container configuration keys.

lxc list -c ns,user.comment:comment
	List images with their running state and user comment. `)
}

func (c *listCmd) flags() {
	gnuflag.StringVar(&c.columnsRaw, "c", "ns46tS", i18n.G("Columns"))
	gnuflag.StringVar(&c.columnsRaw, "columns", "ns46tS", i18n.G("Columns"))
	gnuflag.StringVar(&c.format, "format", "table", i18n.G("Format (csv|json|table|yaml)"))
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

func (c *listCmd) listContainers(conf *config.Config, remote string, cinfos []api.Container, filters []string, columns []column) error {
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
			d, err := conf.GetContainerServer(remote)
			if err != nil {
				cStatesWg.Done()
				return
			}

			for {
				cName, more := <-cStatesQueue
				if !more {
					break
				}

				state, _, err := d.GetContainerState(cName)
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
			d, err := conf.GetContainerServer(remote)
			if err != nil {
				cSnapshotsWg.Done()
				return
			}

			for {
				cName, more := <-cSnapshotsQueue
				if !more {
					break
				}

				snaps, err := d.GetContainerSnapshots(cName)
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
	case listFormatCSV:
		w := csv.NewWriter(os.Stdout)
		w.WriteAll(tableData())
		if err := w.Error(); err != nil {
			return err
		}
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
	case listFormatYAML:
		data := make([]listContainerItem, len(cinfos))
		for i := range cinfos {
			data[i].Container = &cinfos[i]
			data[i].State = cStates[cinfos[i].Name]
			data[i].Snapshots = cSnapshots[cinfos[i].Name]
		}

		out, err := yaml.Marshal(data)
		if err != nil {
			return err
		}
		fmt.Printf("%s", out)
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

func (c *listCmd) run(conf *config.Config, args []string) error {
	var remote string
	name := ""

	filters := []string{}

	if len(args) != 0 {
		filters = args
		if strings.Contains(args[0], ":") && !strings.Contains(args[0], "=") {
			var err error
			remote, name, err = conf.ParseRemote(args[0])
			if err != nil {
				return err
			}

			filters = args[1:]
		} else if !strings.Contains(args[0], "=") {
			remote = conf.DefaultRemote
			name = args[0]
		}
	}
	filters = append(filters, name)

	if remote == "" {
		remote = conf.DefaultRemote
	}

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	var cts []api.Container
	ctslist, err := d.GetContainers()
	if err != nil {
		return err
	}

	for _, cinfo := range ctslist {
		if !c.shouldShow(filters, &cinfo) {
			continue
		}

		cts = append(cts, cinfo)
	}

	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	return c.listContainers(conf, remote, cts, filters, columns)
}

func (c *listCmd) parseColumns() ([]column, error) {
	columnsShorthandMap := map[rune]column{
		'4': {i18n.G("IPV4"), c.IP4ColumnData, true, false},
		'6': {i18n.G("IPV6"), c.IP6ColumnData, true, false},
		'a': {i18n.G("ARCHITECTURE"), c.ArchitectureColumnData, false, false},
		'c': {i18n.G("CREATED AT"), c.CreatedColumnData, false, false},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData, false, false},
		'l': {i18n.G("LAST USED AT"), c.LastUsedColumnData, false, false},
		'n': {i18n.G("NAME"), c.nameColumnData, false, false},
		'N': {i18n.G("PROCESSES"), c.NumberOfProcessesColumnData, true, false},
		'p': {i18n.G("PID"), c.PIDColumnData, true, false},
		'P': {i18n.G("PROFILES"), c.ProfilesColumnData, false, false},
		'S': {i18n.G("SNAPSHOTS"), c.numberSnapshotsColumnData, false, true},
		's': {i18n.G("STATE"), c.statusColumnData, false, false},
		't': {i18n.G("TYPE"), c.typeColumnData, false, false},
		'b': {i18n.G("STORAGE POOL"), c.StoragePoolColumnData, false, false},
	}

	if c.fast {
		if c.columnsRaw != "ns46tS" {
			// --columns was specified too
			return nil, fmt.Errorf("Can't specify --fast with --columns")
		} else {
			c.columnsRaw = "nsacPt"
		}
	}

	columnList := strings.Split(c.columnsRaw, ",")

	columns := []column{}
	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf("Empty column entry (redundant, leading or trailing command) in '%s'", c.columnsRaw)
		}

		// Config keys always contain a period, parse anything without a
		// period as a series of shorthand runes.
		if !strings.Contains(columnEntry, ".") {
			for _, columnRune := range columnEntry {
				if column, ok := columnsShorthandMap[columnRune]; ok {
					columns = append(columns, column)
				} else {
					return nil, fmt.Errorf("Unknown column shorthand char '%c' in '%s'", columnRune, columnEntry)
				}
			}
		} else {
			cc := strings.Split(columnEntry, ":")
			if len(cc) > 3 {
				return nil, fmt.Errorf("Invalid config key column format (too many fields): '%s'", columnEntry)
			}

			k := cc[0]
			if _, err := shared.ConfigKeyChecker(k); err != nil {
				return nil, fmt.Errorf("Invalid config key '%s' in '%s'", k, columnEntry)
			}

			column := column{Name: k}
			if len(cc) > 1 {
				if len(cc[1]) == 0 && len(cc) != 3 {
					return nil, fmt.Errorf("Invalid name in '%s', empty string is only allowed when defining maxWidth", columnEntry)
				}
				column.Name = cc[1]
			}

			maxWidth := -1
			if len(cc) > 2 {
				temp, err := strconv.ParseInt(cc[2], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("Invalid max width (must be an integer) '%s' in '%s'", cc[2], columnEntry)
				}
				if temp < -1 {
					return nil, fmt.Errorf("Invalid max width (must -1, 0 or a positive integer) '%s' in '%s'", cc[2], columnEntry)
				}
				if temp == 0 {
					maxWidth = len(column.Name)
				} else {
					maxWidth = int(temp)
				}
			}

			column.Data = func(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
				v, ok := cInfo.Config[k]
				if !ok {
					v, _ = cInfo.ExpandedConfig[k]
				}

				// Truncate the data according to the max width.  A negative max width
				// indicates there is no effective limit.
				if maxWidth > 0 && len(v) > maxWidth {
					return v[:maxWidth]
				}
				return v
			}
			columns = append(columns, column)
		}
	}
	return columns, nil
}

func (c *listCmd) nameColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	return cInfo.Name
}

func (c *listCmd) descriptionColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	return cInfo.Description
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

func (c *listCmd) StoragePoolColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	for _, v := range cInfo.ExpandedDevices {
		if v["type"] == "disk" && v["path"] == "/" {
			return v["pool"]
		}
	}

	return ""
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

func (c *listCmd) LastUsedColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	layout := "2006/01/02 15:04 UTC"

	if !cInfo.LastUsedAt.IsZero() && shared.TimeIsSet(cInfo.LastUsedAt) {
		return cInfo.LastUsedAt.UTC().Format(layout)
	}

	return ""
}

func (c *listCmd) NumberOfProcessesColumnData(cInfo api.Container, cState *api.ContainerState, cSnaps []api.ContainerSnapshot) string {
	if cInfo.IsActive() && cState != nil {
		return fmt.Sprintf("%d", cState.Processes)
	}

	return ""

}
