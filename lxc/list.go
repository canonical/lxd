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
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type column struct {
	Name           string
	Data           columnData
	NeedsState     bool
	NeedsSnapshots bool
}

type columnData func(api.ContainerFull) string

type cmdList struct {
	global *cmdGlobal

	flagColumns string
	flagFast    bool
	flagFormat  string
}

func (c *cmdList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("list [<remote>:] [<filter>...]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List containers

Default column layout: ns46tS
Fast column layout: nsacPt

== Filters ==
A single keyword like "web" which will list any container with a name starting by "web".
A regular expression on the container name. (e.g. .*web.*01$).
A key/value pair referring to a configuration item. For those, the
namespace can be abbreviated to the smallest unambiguous identifier.

Examples:
  - "user.blah=abc" will list all containers with the "blah" user property set to "abc".
  - "u.blah=abc" will do the same
  - "security.privileged=true" will list all privileged containers
  - "s.privileged=true" will do the same

A regular expression matching a configuration item or its value. (e.g. volatile.eth0.hwaddr=00:16:3e:.*).

== Columns ==
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
  L - Location of the container (e.g. its cluster member)
  f - Base Image Fingerprint (short)
  F - Base Image Fingerprint (long)

Custom columns are defined with "key[:name][:maxWidth]":
  KEY: The (extended) config key to display
  NAME: Name to display in the column header.
  Defaults to the key if not specified or empty.

  MAXWIDTH: Max width of the column (longer results are truncated).
  Defaults to -1 (unlimited). Use 0 to limit to the column header size.`))

	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc list -c nFs46,volatile.eth0.hwaddr:MAC
  Show containers using the "NAME", "BASE IMAGE", "STATE", "IPV4", "IPV6" and "MAC" columns.
  "BASE IMAGE" and "MAC" are custom columns generated from container configuration keys.

lxc list -c ns,user.comment:comment
  List images with their running state and user comment.`))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultColumns, i18n.G("Columns")+"``")
	cmd.Flags().StringVar(&c.flagFormat, "format", "table", i18n.G("Format (csv|json|table|yaml)")+"``")
	cmd.Flags().BoolVar(&c.flagFast, "fast", false, i18n.G("Fast mode (same as --columns=nsacPt)"))

	return cmd
}

const defaultColumns = "ns46tSL"

// This seems a little excessive.
func (c *cmdList) dotPrefixMatch(short string, full string) bool {
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

func (c *cmdList) shouldShow(filters []string, state *api.Container) bool {
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

func (c *cmdList) listContainers(conf *config.Config, d lxd.ContainerServer, cinfos []api.Container, filters []string, columns []column) error {
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

	// Convert to ContainerFull
	data := make([]api.ContainerFull, len(cinfos))
	for i := range cinfos {
		data[i].Container = cinfos[i]
		data[i].State = cStates[cinfos[i].Name]
		data[i].Snapshots = cSnapshots[cinfos[i].Name]
	}

	return c.showContainers(data, filters, columns)
}

func (c *cmdList) showContainers(cts []api.ContainerFull, filters []string, columns []column) error {
	// Generate the table data
	tableData := func() [][]string {
		data := [][]string{}
		for _, ct := range cts {
			if !c.shouldShow(filters, &ct.Container) {
				continue
			}

			col := []string{}
			for _, column := range columns {
				col = append(col, column.Data(ct))
			}
			data = append(data, col)
		}

		sort.Sort(byName(data))
		return data
	}

	// Deal with various output formats
	switch c.flagFormat {
	case listFormatCSV:
		w := csv.NewWriter(os.Stdout)
		w.WriteAll(tableData())
		if err := w.Error(); err != nil {
			return err
		}
	case listFormatTable:
		headers := []string{}
		for _, column := range columns {
			headers = append(headers, column.Name)
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetAutoWrapText(false)
		table.SetAlignment(tablewriter.ALIGN_LEFT)
		table.SetRowLine(true)
		table.SetHeader(headers)
		table.AppendBulk(tableData())
		table.Render()
	case listFormatJSON:
		enc := json.NewEncoder(os.Stdout)
		err := enc.Encode(cts)
		if err != nil {
			return err
		}
	case listFormatYAML:
		out, err := yaml.Marshal(cts)
		if err != nil {
			return err
		}
		fmt.Printf("%s", out)
	default:
		return fmt.Errorf(i18n.G("Invalid format %q"), c.flagFormat)
	}

	return nil
}

func (c *cmdList) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 0, -1)
	if exit {
		return err
	}

	// Parse the remote
	var remote string
	var name string
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
	if name != "" {
		filters = append(filters, name)
	}

	if remote == "" {
		remote = conf.DefaultRemote
	}

	// Connect to LXD
	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	// Get the list of columns
	columns, needsData, err := c.parseColumns(d.IsClustered())
	if err != nil {
		return err
	}

	if len(filters) == 0 && needsData && d.HasExtension("container_full") {
		// Using the GetContainersFull shortcut
		cts, err := d.GetContainersFull()
		if err != nil {
			return err
		}

		return c.showContainers(cts, filters, columns)
	}

	// Get the list of containers
	var cts []api.Container
	ctslist, err := d.GetContainers()
	if err != nil {
		return err
	}

	// Apply filters
	for _, cinfo := range ctslist {
		if !c.shouldShow(filters, &cinfo) {
			continue
		}

		cts = append(cts, cinfo)
	}

	// Fetch any remaining data and render the table
	return c.listContainers(conf, d, cts, filters, columns)
}

func (c *cmdList) parseColumns(clustered bool) ([]column, bool, error) {
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
		'f': {i18n.G("BASE IMAGE"), c.baseImageColumnData, false, false},
		'F': {i18n.G("BASE IMAGE"), c.baseImageFullColumnData, false, false},
	}

	if c.flagFast {
		if c.flagColumns != defaultColumns {
			// --columns was specified too
			return nil, false, fmt.Errorf(i18n.G("Can't specify --fast with --columns"))
		}

		c.flagColumns = "nsacPt"
	}

	if clustered {
		columnsShorthandMap['L'] = column{
			i18n.G("LOCATION"), c.locationColumnData, false, false}
	} else {
		if c.flagColumns != defaultColumns {
			if strings.ContainsAny(c.flagColumns, "L") {
				return nil, false, fmt.Errorf(i18n.G("Can't specify column L when not clustered"))
			}
		}
		c.flagColumns = strings.Replace(c.flagColumns, "L", "", -1)
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []column{}
	needsData := false
	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, false, fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing command) in '%s'"), c.flagColumns)
		}

		// Config keys always contain a period, parse anything without a
		// period as a series of shorthand runes.
		if !strings.Contains(columnEntry, ".") {
			for _, columnRune := range columnEntry {
				if column, ok := columnsShorthandMap[columnRune]; ok {
					columns = append(columns, column)

					if column.NeedsState || column.NeedsSnapshots {
						needsData = true
					}
				} else {
					return nil, false, fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
				}
			}
		} else {
			cc := strings.Split(columnEntry, ":")
			if len(cc) > 3 {
				return nil, false, fmt.Errorf(i18n.G("Invalid config key column format (too many fields): '%s'"), columnEntry)
			}

			k := cc[0]
			if _, err := shared.ConfigKeyChecker(k); err != nil {
				return nil, false, fmt.Errorf(i18n.G("Invalid config key '%s' in '%s'"), k, columnEntry)
			}

			column := column{Name: k}
			if len(cc) > 1 {
				if len(cc[1]) == 0 && len(cc) != 3 {
					return nil, false, fmt.Errorf(i18n.G("Invalid name in '%s', empty string is only allowed when defining maxWidth"), columnEntry)
				}
				column.Name = cc[1]
			}

			maxWidth := -1
			if len(cc) > 2 {
				temp, err := strconv.ParseInt(cc[2], 10, 64)
				if err != nil {
					return nil, false, fmt.Errorf(i18n.G("Invalid max width (must be an integer) '%s' in '%s'"), cc[2], columnEntry)
				}
				if temp < -1 {
					return nil, false, fmt.Errorf(i18n.G("Invalid max width (must -1, 0 or a positive integer) '%s' in '%s'"), cc[2], columnEntry)
				}
				if temp == 0 {
					maxWidth = len(column.Name)
				} else {
					maxWidth = int(temp)
				}
			}

			column.Data = func(cInfo api.ContainerFull) string {
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

			if column.NeedsState || column.NeedsSnapshots {
				needsData = true
			}
		}
	}

	return columns, needsData, nil
}

func (c *cmdList) getBaseImage(cInfo api.ContainerFull, long bool) string {
	v, ok := cInfo.Config["volatile.base_image"]
	if !ok {
		return ""
	}

	if !long && len(v) >= 12 {
		v = v[:12]
	}

	return v
}

func (c *cmdList) baseImageColumnData(cInfo api.ContainerFull) string {
	return c.getBaseImage(cInfo, false)
}

func (c *cmdList) baseImageFullColumnData(cInfo api.ContainerFull) string {
	return c.getBaseImage(cInfo, true)
}

func (c *cmdList) nameColumnData(cInfo api.ContainerFull) string {
	return cInfo.Name
}

func (c *cmdList) descriptionColumnData(cInfo api.ContainerFull) string {
	return cInfo.Description
}

func (c *cmdList) statusColumnData(cInfo api.ContainerFull) string {
	return strings.ToUpper(cInfo.Status)
}

func (c *cmdList) IP4ColumnData(cInfo api.ContainerFull) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.Network != nil {
		ipv4s := []string{}
		for netName, net := range cInfo.State.Network {
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
	}

	return ""
}

func (c *cmdList) IP6ColumnData(cInfo api.ContainerFull) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.Network != nil {
		ipv6s := []string{}
		for netName, net := range cInfo.State.Network {
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
	}

	return ""
}

func (c *cmdList) typeColumnData(cInfo api.ContainerFull) string {
	if cInfo.Ephemeral {
		return i18n.G("EPHEMERAL")
	}

	return i18n.G("PERSISTENT")
}

func (c *cmdList) numberSnapshotsColumnData(cInfo api.ContainerFull) string {
	if cInfo.Snapshots != nil {
		return fmt.Sprintf("%d", len(cInfo.Snapshots))
	}

	return ""
}

func (c *cmdList) PIDColumnData(cInfo api.ContainerFull) string {
	if cInfo.IsActive() && cInfo.State != nil {
		return fmt.Sprintf("%d", cInfo.State.Pid)
	}

	return ""
}

func (c *cmdList) ArchitectureColumnData(cInfo api.ContainerFull) string {
	return cInfo.Architecture
}

func (c *cmdList) StoragePoolColumnData(cInfo api.ContainerFull) string {
	for _, v := range cInfo.ExpandedDevices {
		if v["type"] == "disk" && v["path"] == "/" {
			return v["pool"]
		}
	}

	return ""
}

func (c *cmdList) ProfilesColumnData(cInfo api.ContainerFull) string {
	return strings.Join(cInfo.Profiles, "\n")
}

func (c *cmdList) CreatedColumnData(cInfo api.ContainerFull) string {
	layout := "2006/01/02 15:04 UTC"

	if shared.TimeIsSet(cInfo.CreatedAt) {
		return cInfo.CreatedAt.UTC().Format(layout)
	}

	return ""
}

func (c *cmdList) LastUsedColumnData(cInfo api.ContainerFull) string {
	layout := "2006/01/02 15:04 UTC"

	if !cInfo.LastUsedAt.IsZero() && shared.TimeIsSet(cInfo.LastUsedAt) {
		return cInfo.LastUsedAt.UTC().Format(layout)
	}

	return ""
}

func (c *cmdList) NumberOfProcessesColumnData(cInfo api.ContainerFull) string {
	if cInfo.IsActive() && cInfo.State != nil {
		return fmt.Sprintf("%d", cInfo.State.Processes)
	}

	return ""
}

func (c *cmdList) locationColumnData(cInfo api.ContainerFull) string {
	return cInfo.Location
}
