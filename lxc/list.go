package main

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/units"
)

type column struct {
	Name           string
	Data           columnData
	NeedsState     bool
	NeedsSnapshots bool
}

type columnData func(api.InstanceFull) string

type cmdList struct {
	global *cmdGlobal

	flagColumns     string
	flagFast        bool
	flagFormat      string
	flagAllProjects bool

	shorthandFilters map[string]func(*api.Instance, *api.InstanceState, string) bool
}

// Command sets up and returns a Cobra command object for the 'list' sub-command, configuring its usage, description, options, and execution function.
func (c *cmdList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:] [<filter>...]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List instances

Default column layout: ns46tS
Fast column layout: nsacPt

A single keyword like "web" which will list any instance with a name starting by "web".
A regular expression on the instance name. (e.g. .*web.*01$).
A key/value pair referring to a configuration item. For those, the
namespace can be abbreviated to the smallest unambiguous identifier.
A key/value pair where the key is a shorthand. Multiple values must be delimited by ','. Available shorthands:
  - type={instance type}
  - status={instance current lifecycle status}
  - architecture={instance architecture}
  - location={location name}
  - ipv4={ip or CIDR}
  - ipv6={ip or CIDR}

Examples:
  - "user.blah=abc" will list all instances with the "blah" user property set to "abc".
  - "u.blah=abc" will do the same
  - "security.privileged=true" will list all privileged instances
  - "s.privileged=true" will do the same
  - "type=container" will list all container instances
  - "type=container status=running" will list all running container instances

A regular expression matching a configuration item or its value. (e.g. volatile.eth0.hwaddr=00:16:3e:.*).

When multiple filters are passed, they are added one on top of the other,
selecting instances which satisfy them all.

== Columns ==
The -c option takes a comma separated list of arguments that control
which instance attributes to output when displaying in table or csv
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
  D - disk usage
  e - Project name
  l - Last used date
  m - Memory usage
  M - Memory usage (%)
  n - Name
  N - Number of Processes
  p - PID of the instance's init process
  P - Profiles
  s - State
  S - Number of snapshots
  t - Type (persistent or ephemeral)
  u - CPU usage (in seconds)
  L - Location of the instance (e.g. its cluster member)
  f - Base Image Fingerprint (short)
  F - Base Image Fingerprint (long)

Custom columns are defined with "[config:|devices:]key[:name][:maxWidth]":
  KEY: The (extended) config or devices key to display. If [config:|devices:] is omitted then it defaults to config key.
  NAME: Name to display in the column header.
  Defaults to the key if not specified or empty.

  MAXWIDTH: Max width of the column (longer results are truncated).
  Defaults to -1 (unlimited). Use 0 to limit to the column header size.`))

	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc list -c nFs46,volatile.eth0.hwaddr:MAC,config:image.os,devices:eth0.parent:ETHP
  Show instances using the "NAME", "BASE IMAGE", "STATE", "IPV4", "IPV6" and "MAC" columns.
  "BASE IMAGE", "MAC" and "IMAGE OS" are custom columns generated from instance configuration keys.
  "ETHP" is a custom column generated from a device key.

lxc list -c ns,user.comment:comment
  List instances with their running state and user comment.`))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultColumns, i18n.G("Columns")+"``")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().BoolVar(&c.flagFast, "fast", false, i18n.G("Fast mode (same as --columns=nsacPt)"))
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Display instances from all projects"))

	return cmd
}

const defaultColumns = "ns46tSL"
const defaultColumnsAllProjects = "ens46tSL"
const configColumnType = "config"
const deviceColumnType = "devices"

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

// shouldShow determines whether a given instance matches the provided filters and should be included in the list output.
func (c *cmdList) shouldShow(filters []string, inst *api.Instance, state *api.InstanceState, initial bool) bool {
	c.mapShorthandFilters()

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

			if initial || c.evaluateShorthandFilter(key, value, inst, state) {
				continue
			}

			found := false
			for configKey, configValue := range inst.ExpandedConfig {
				if c.dotPrefixMatch(key, configKey) {
					// Try to test filter value as a regexp.
					regexpValue := value
					if !(strings.Contains(value, "^") || strings.Contains(value, "$")) {
						regexpValue = "^" + regexpValue + "$"
					}

					r, err := regexp.Compile(regexpValue)
					// If not regexp compatible use original value.
					if err != nil {
						if value == configValue {
							found = true
							break
						} else {
							// The property was found but didn't match.
							return false
						}
					} else if r.MatchString(configValue) {
						found = true
						break
					}
				}
			}

			if inst.ExpandedConfig[key] == value {
				continue
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
			if err == nil && r.MatchString(inst.Name) {
				continue
			}

			if !strings.HasPrefix(inst.Name, filter) {
				return false
			}
		}
	}

	return true
}

// evaluateShorthandFilter checks if a given instance and its state match the provided shorthand filter key-value pair.
func (c *cmdList) evaluateShorthandFilter(key string, value string, inst *api.Instance, state *api.InstanceState) bool {
	const shorthandValueDelimiter = ","
	shorthandFilterFunction, isShorthandFilter := c.shorthandFilters[strings.ToLower(key)]

	if isShorthandFilter {
		if strings.Contains(value, shorthandValueDelimiter) {
			matched := false
			for _, curValue := range strings.Split(value, shorthandValueDelimiter) {
				if shorthandFilterFunction(inst, state, curValue) {
					matched = true
				}
			}

			return matched
		}

		return shorthandFilterFunction(inst, state, value)
	}

	return false
}

// listInstances retrieves, filters, and displays instances with state and snapshot information based on given filters and columns.
func (c *cmdList) listInstances(conf *config.Config, d lxd.InstanceServer, instances []api.Instance, filters []string, columns []column) error {
	threads := 10
	if len(instances) < threads {
		threads = len(instances)
	}

	// Shortcut when needing state and snapshot info.
	hasSnapshots := false
	hasState := false
	for _, column := range columns {
		if column.NeedsSnapshots {
			hasSnapshots = true
		}

		if column.NeedsState {
			hasState = true
		}
	}

	if hasSnapshots && hasState {
		cInfo := []api.InstanceFull{}
		cInfoLock := sync.Mutex{}
		cInfoQueue := make(chan string, threads)
		cInfoWg := sync.WaitGroup{}

		for i := 0; i < threads; i++ {
			cInfoWg.Add(1)
			go func() {
				for {
					cName, more := <-cInfoQueue
					if !more {
						break
					}

					state, _, err := d.GetInstanceFull(cName)
					if err != nil {
						continue
					}

					cInfoLock.Lock()
					cInfo = append(cInfo, *state)
					cInfoLock.Unlock()
				}

				cInfoWg.Done()
			}()
		}

		for _, info := range instances {
			cInfoQueue <- info.Name
		}

		close(cInfoQueue)
		cInfoWg.Wait()

		return c.showInstances(cInfo, filters, columns)
	}

	cStates := map[string]*api.InstanceState{}
	cStatesLock := sync.Mutex{}
	cStatesQueue := make(chan string, threads)
	cStatesWg := sync.WaitGroup{}

	cSnapshots := map[string][]api.InstanceSnapshot{}
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

				state, _, err := d.GetInstanceState(cName)
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

				snaps, err := d.GetInstanceSnapshots(cName)
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

	for _, inst := range instances {
		for _, column := range columns {
			if column.NeedsState && inst.IsActive() {
				cStatesLock.Lock()
				_, ok := cStates[inst.Name]
				cStatesLock.Unlock()
				if ok {
					continue
				}

				cStatesLock.Lock()
				cStates[inst.Name] = nil
				cStatesLock.Unlock()

				cStatesQueue <- inst.Name
			}

			if column.NeedsSnapshots {
				cSnapshotsLock.Lock()
				_, ok := cSnapshots[inst.Name]
				cSnapshotsLock.Unlock()
				if ok {
					continue
				}

				cSnapshotsLock.Lock()
				cSnapshots[inst.Name] = nil
				cSnapshotsLock.Unlock()

				cSnapshotsQueue <- inst.Name
			}
		}
	}

	close(cStatesQueue)
	close(cSnapshotsQueue)
	cStatesWg.Wait()
	cSnapshotsWg.Wait()

	// Convert to Instance
	data := make([]api.InstanceFull, len(instances))
	for i := range instances {
		data[i].Instance = instances[i]
		data[i].State = cStates[instances[i].Name]
		data[i].Snapshots = cSnapshots[instances[i].Name]
	}

	return c.showInstances(data, filters, columns)
}

// showInstances filters instances and renders them in a table based on specified columns and output format.
func (c *cmdList) showInstances(instances []api.InstanceFull, filters []string, columns []column) error {
	// Generate the table data
	data := [][]string{}
	instancesFiltered := []api.InstanceFull{}

	for _, inst := range instances {
		if !c.shouldShow(filters, &inst.Instance, inst.State, false) {
			continue
		}

		instancesFiltered = append(instancesFiltered, inst)

		col := []string{}
		for _, column := range columns {
			col = append(col, column.Data(inst))
		}

		data = append(data, col)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	headers := []string{}
	for _, column := range columns {
		headers = append(headers, column.Name)
	}

	return cli.RenderTable(c.flagFormat, headers, data, instancesFiltered)
}

// Run executes the 'list' command, parsing arguments, connecting to the LXD instance server, fetching instance data, filtering, and rendering the output table.
func (c *cmdList) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, -1)
	if exit {
		return err
	}

	if c.global.flagProject != "" && c.flagAllProjects {
		return fmt.Errorf(i18n.G("Can't specify --project with --all-projects"))
	}

	// Parse the remote
	var remote string
	var name string
	var filters []string

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
	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// Get the list of columns
	columns, needsData, err := c.parseColumns(d.IsClustered())
	if err != nil {
		return err
	}

	if needsData && d.HasExtension("container_full") {
		// Using the GetInstancesFull shortcut
		var instances []api.InstanceFull

		serverFilters, clientFilters := getServerSupportedFilters(filters, api.InstanceFull{})

		if c.flagAllProjects {
			instances, err = d.GetInstancesFullAllProjectsWithFilter(api.InstanceTypeAny, serverFilters)
		} else {
			instances, err = d.GetInstancesFullWithFilter(api.InstanceTypeAny, serverFilters)
		}

		if err != nil {
			return err
		}

		return c.showInstances(instances, clientFilters, columns)
	}

	// Get the list of instances
	var instances []api.Instance
	serverFilters, clientFilters := getServerSupportedFilters(filters, api.Instance{})

	if c.flagAllProjects {
		instances, err = d.GetInstancesAllProjectsWithFilter(api.InstanceTypeAny, serverFilters)
	} else {
		instances, err = d.GetInstancesWithFilter(api.InstanceTypeAny, serverFilters)
	}

	if err != nil {
		return err
	}

	// Apply filters
	instancesFiltered := []api.Instance{}
	for _, inst := range instances {
		if !c.shouldShow(clientFilters, &inst, nil, true) {
			continue
		}

		instancesFiltered = append(instancesFiltered, inst)
	}

	// Fetch any remaining data and render the table
	return c.listInstances(conf, d, instancesFiltered, clientFilters, columns)
}

// parseColumns interprets the desired columns for output from command-line flags, validates the input, and creates the column definitions for use in rendering the table.
func (c *cmdList) parseColumns(clustered bool) ([]column, bool, error) {
	columnsShorthandMap := map[rune]column{
		'4': {i18n.G("IPV4"), c.IP4ColumnData, true, false},
		'6': {i18n.G("IPV6"), c.IP6ColumnData, true, false},
		'a': {i18n.G("ARCHITECTURE"), c.ArchitectureColumnData, false, false},
		'b': {i18n.G("STORAGE POOL"), c.StoragePoolColumnData, false, false},
		'c': {i18n.G("CREATED AT"), c.CreatedColumnData, false, false},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData, false, false},
		'D': {i18n.G("DISK USAGE"), c.diskUsageColumnData, true, false},
		'e': {i18n.G("PROJECT"), c.projectColumnData, false, false},
		'f': {i18n.G("BASE IMAGE"), c.baseImageColumnData, false, false},
		'F': {i18n.G("BASE IMAGE"), c.baseImageFullColumnData, false, false},
		'l': {i18n.G("LAST USED AT"), c.LastUsedColumnData, false, false},
		'm': {i18n.G("MEMORY USAGE"), c.memoryUsageColumnData, true, false},
		'M': {i18n.G("MEMORY USAGE%"), c.memoryUsagePercentColumnData, true, false},
		'n': {i18n.G("NAME"), c.nameColumnData, false, false},
		'N': {i18n.G("PROCESSES"), c.NumberOfProcessesColumnData, true, false},
		'p': {i18n.G("PID"), c.PIDColumnData, true, false},
		'P': {i18n.G("PROFILES"), c.ProfilesColumnData, false, false},
		'S': {i18n.G("SNAPSHOTS"), c.numberSnapshotsColumnData, false, true},
		's': {i18n.G("STATE"), c.statusColumnData, false, false},
		't': {i18n.G("TYPE"), c.typeColumnData, false, false},
		'u': {i18n.G("CPU USAGE"), c.cpuUsageSecondsColumnData, true, false},
	}

	// Add project column if --all-projects flag specified and
	// no one of --fast or --c was passed
	if c.flagAllProjects {
		if c.flagColumns == defaultColumns {
			c.flagColumns = defaultColumnsAllProjects
		}
	}

	if c.flagFast {
		if c.flagColumns != defaultColumns && c.flagColumns != defaultColumnsAllProjects {
			// --columns was specified too
			return nil, false, fmt.Errorf(i18n.G("Can't specify --fast with --columns"))
		}

		if c.flagColumns == defaultColumnsAllProjects {
			c.flagColumns = "ensacPt"
		} else {
			c.flagColumns = "nsacPt"
		}
	}

	if clustered {
		columnsShorthandMap['L'] = column{
			i18n.G("LOCATION"), c.locationColumnData, false, false}
	} else {
		if c.flagColumns != defaultColumns && c.flagColumns != defaultColumnsAllProjects {
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
				column, ok := columnsShorthandMap[columnRune]
				if ok {
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
			colType := configColumnType
			if (cc[0] == configColumnType || cc[0] == deviceColumnType) && len(cc) > 1 {
				colType = cc[0]
				cc = append(cc[:0], cc[1:]...)
			}

			if len(cc) > 3 {
				return nil, false, fmt.Errorf(i18n.G("Invalid config key column format (too many fields): '%s'"), columnEntry)
			}

			k := cc[0]
			if colType == configColumnType {
				_, err := shared.ConfigKeyChecker(k, instancetype.Any)
				if err != nil {
					return nil, false, fmt.Errorf(i18n.G("Invalid config key '%s' in '%s'"), k, columnEntry)
				}
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
			if colType == configColumnType {
				column.Data = func(cInfo api.InstanceFull) string {
					v, ok := cInfo.Config[k]
					if !ok {
						v = cInfo.ExpandedConfig[k]
					}

					// Truncate the data according to the max width.  A negative max width
					// indicates there is no effective limit.
					if maxWidth > 0 && len(v) > maxWidth {
						return v[:maxWidth]
					}

					return v
				}
			}
			if colType == deviceColumnType {
				column.Data = func(cInfo api.InstanceFull) string {
					d := strings.SplitN(k, ".", 2)
					if len(d) == 1 || len(d) > 2 {
						return ""
					}

					v, ok := cInfo.Devices[d[0]][d[1]]
					if !ok {
						v = cInfo.ExpandedDevices[d[0]][d[1]]
					}

					//// Truncate the data according to the max width.  A negative max width
					//// indicates there is no effective limit.
					if maxWidth > 0 && len(v) > maxWidth {
						return v[:maxWidth]
					}

					return v
				}
			}
			columns = append(columns, column)

			if column.NeedsState || column.NeedsSnapshots {
				needsData = true
			}
		}
	}

	return columns, needsData, nil
}

// getBaseImage extracts the base image ID from an instance's configuration, shortening it if necessary based on the 'long' parameter.
func (c *cmdList) getBaseImage(cInfo api.InstanceFull, long bool) string {
	v, ok := cInfo.Config["volatile.base_image"]
	if !ok {
		return ""
	}

	if !long && len(v) >= 12 {
		v = v[:12]
	}

	return v
}

// baseImageColumnData retrieves the shortened base image ID from an instance's configuration for displaying in a table column.
func (c *cmdList) baseImageColumnData(cInfo api.InstanceFull) string {
	return c.getBaseImage(cInfo, false)
}

// baseImageFullColumnData retrieves the full base image ID from an instance's configuration for displaying in a table column.
func (c *cmdList) baseImageFullColumnData(cInfo api.InstanceFull) string {
	return c.getBaseImage(cInfo, true)
}

// nameColumnData extracts the name of an instance for displaying in a table column.
func (c *cmdList) nameColumnData(cInfo api.InstanceFull) string {
	return cInfo.Name
}

func (c *cmdList) descriptionColumnData(cInfo api.InstanceFull) string {
	return cInfo.Description
}

// statusColumnData retrieves the uppercased status of an instance for displaying in a table column.
func (c *cmdList) statusColumnData(cInfo api.InstanceFull) string {
	return strings.ToUpper(cInfo.Status)
}

// IP4ColumnData retrieves the sorted list of active IPv4 addresses (excluding local and link scope) of an instance for a table column.
func (c *cmdList) IP4ColumnData(cInfo api.InstanceFull) string {
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

// IP6ColumnData retrieves the sorted list of active IPv6 addresses (excluding local and link scope) of an instance for a table column.
func (c *cmdList) IP6ColumnData(cInfo api.InstanceFull) string {
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

// projectColumnData returns the project name associated with the instance for a table column.
func (c *cmdList) projectColumnData(cInfo api.InstanceFull) string {
	return cInfo.Project
}

// memoryUsageColumnData retrieves the active instance's memory usage in human-readable format for a table column.
func (c *cmdList) memoryUsageColumnData(cInfo api.InstanceFull) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.Memory.Usage > 0 {
		return units.GetByteSizeStringIEC(cInfo.State.Memory.Usage, 2)
	}

	return ""
}

// memoryUsagePercentColumnData calculates the percentage of memory used by an active instance relative to its limit for a table column.
func (c *cmdList) memoryUsagePercentColumnData(cInfo api.InstanceFull) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.Memory.Usage > 0 {
		if cInfo.ExpandedConfig["limits.memory"] != "" {
			memorylimit := cInfo.ExpandedConfig["limits.memory"]

			if strings.Contains(memorylimit, "%") {
				return ""
			}

			val, err := units.ParseByteSizeString(cInfo.ExpandedConfig["limits.memory"])
			if err == nil && val > 0 {
				return fmt.Sprintf("%.1f%%", (float64(cInfo.State.Memory.Usage)/float64(val))*float64(100))
			}
		}
	}

	return ""
}

// cpuUsageSecondsColumnData returns the CPU usage time in seconds of an active instance for a table column.
func (c *cmdList) cpuUsageSecondsColumnData(cInfo api.InstanceFull) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.CPU.Usage > 0 {
		return fmt.Sprintf("%ds", cInfo.State.CPU.Usage/1000000000)
	}

	return ""
}

// diskUsageColumnData returns the disk usage of the root device of an instance in IEC format for a table column.
func (c *cmdList) diskUsageColumnData(cInfo api.InstanceFull) string {
	rootDisk, _, _ := shared.GetRootDiskDevice(cInfo.ExpandedDevices)

	if cInfo.State != nil && cInfo.State.Disk != nil && cInfo.State.Disk[rootDisk].Usage > 0 {
		return units.GetByteSizeStringIEC(cInfo.State.Disk[rootDisk].Usage, 2)
	}

	return ""
}

// typeColumnData returns the type of an instance, specifying whether it is ephemeral or not for a table column.
func (c *cmdList) typeColumnData(cInfo api.InstanceFull) string {
	if cInfo.Type == "" {
		cInfo.Type = "container"
	}

	if cInfo.Ephemeral {
		return fmt.Sprintf("%s (%s)", strings.ToUpper(cInfo.Type), i18n.G("EPHEMERAL"))
	}

	return strings.ToUpper(cInfo.Type)
}

// numberSnapshotsColumnData returns the count of instance snapshots for a table column.
func (c *cmdList) numberSnapshotsColumnData(cInfo api.InstanceFull) string {
	if cInfo.Snapshots != nil {
		return fmt.Sprintf("%d", len(cInfo.Snapshots))
	}

	return "0"
}

// PIDColumnData returns the process ID of an active instance for a table column.
func (c *cmdList) PIDColumnData(cInfo api.InstanceFull) string {
	if cInfo.IsActive() && cInfo.State != nil {
		return fmt.Sprintf("%d", cInfo.State.Pid)
	}

	return ""
}

// ArchitectureColumnData returns the architecture of the given instance for a table column.
func (c *cmdList) ArchitectureColumnData(cInfo api.InstanceFull) string {
	return cInfo.Architecture
}

// StoragePoolColumnData retrieves the storage pool of the given instance where the root disk is located.
func (c *cmdList) StoragePoolColumnData(cInfo api.InstanceFull) string {
	for _, v := range cInfo.ExpandedDevices {
		if v["type"] == "disk" && v["path"] == "/" {
			return v["pool"]
		}
	}

	return ""
}

// ProfilesColumnData returns a string of instance profiles, each on a new line, for table display.
func (c *cmdList) ProfilesColumnData(cInfo api.InstanceFull) string {
	return strings.Join(cInfo.Profiles, "\n")
}

// CreatedColumnData returns the creation date of the instance in UTC format for table display.
func (c *cmdList) CreatedColumnData(cInfo api.InstanceFull) string {
	layout := "2006/01/02 15:04 UTC"

	if shared.TimeIsSet(cInfo.CreatedAt) {
		return cInfo.CreatedAt.UTC().Format(layout)
	}

	return ""
}

// LastUsedColumnData returns the last usage date of the instance in UTC format for table display.
func (c *cmdList) LastUsedColumnData(cInfo api.InstanceFull) string {
	layout := "2006/01/02 15:04 UTC"

	if !cInfo.LastUsedAt.IsZero() && shared.TimeIsSet(cInfo.LastUsedAt) {
		return cInfo.LastUsedAt.UTC().Format(layout)
	}

	return ""
}

// NumberOfProcessesColumnData returns the number of processes running inside the instance for table display.
func (c *cmdList) NumberOfProcessesColumnData(cInfo api.InstanceFull) string {
	if cInfo.IsActive() && cInfo.State != nil {
		return fmt.Sprintf("%d", cInfo.State.Processes)
	}

	return ""
}

// locationColumnData returns the location of the instance for table display.
func (c *cmdList) locationColumnData(cInfo api.InstanceFull) string {
	return cInfo.Location
}

// matchByType checks if the instance type matches the given query.
func (c *cmdList) matchByType(cInfo *api.Instance, cState *api.InstanceState, query string) bool {
	return strings.EqualFold(cInfo.Type, query)
}

// matchByStatus checks if the instance status matches the given query.
func (c *cmdList) matchByStatus(cInfo *api.Instance, cState *api.InstanceState, query string) bool {
	return strings.EqualFold(cInfo.Status, query)
}

// matchByArchitecture checks if the instance architecture matches the given query.
func (c *cmdList) matchByArchitecture(cInfo *api.Instance, cState *api.InstanceState, query string) bool {
	return strings.EqualFold(cInfo.InstancePut.Architecture, query)
}

// matchByLocation checks if the instance's location matches the given query.
func (c *cmdList) matchByLocation(cInfo *api.Instance, cState *api.InstanceState, query string) bool {
	return strings.EqualFold(cInfo.Location, query)
}

// matchByNet checks if the instance has a network interface with an IP address matching the given query and family.
func (c *cmdList) matchByNet(cInfo *api.Instance, cState *api.InstanceState, query string, family string) bool {
	// Skip if no state.
	if cState == nil {
		return false
	}

	// Skip if no network data.
	if cState.Network == nil {
		return false
	}

	// Consider the filter as a CIDR.
	_, subnet, _ := net.ParseCIDR(query)

	// Go through interfaces.
	for _, network := range cState.Network {
		for _, addr := range network.Addresses {
			if family == "ipv6" && addr.Family != "inet6" {
				continue
			}

			if family == "ipv4" && addr.Family != "inet" {
				continue
			}

			if addr.Address == query {
				return true
			}

			if subnet != nil {
				ipAddr := net.ParseIP(addr.Address)
				if ipAddr != nil && subnet.Contains(ipAddr) {
					return true
				}
			}
		}
	}

	return false
}

// matchByIPV6 checks if the instance has an IPv6 address matching the given query. It uses the matchByNet function with the "ipv6" family.
func (c *cmdList) matchByIPV6(cInfo *api.Instance, cState *api.InstanceState, query string) bool {
	return c.matchByNet(cInfo, cState, query, "ipv6")
}

// matchByIPV4 checks if the instance has an IPv4 address matching the given query. It uses the matchByNet function with the "ipv4" family.
func (c *cmdList) matchByIPV4(cInfo *api.Instance, cState *api.InstanceState, query string) bool {
	return c.matchByNet(cInfo, cState, query, "ipv4")
}

// mapShorthandFilters maps shorthand filter names to their corresponding match functions for filtering instances.
func (c *cmdList) mapShorthandFilters() {
	c.shorthandFilters = map[string]func(*api.Instance, *api.InstanceState, string) bool{
		"type":         c.matchByType,
		"status":       c.matchByStatus,
		"architecture": c.matchByArchitecture,
		"location":     c.matchByLocation,
		"ipv4":         c.matchByIPV4,
		"ipv6":         c.matchByIPV6,
	}
}
