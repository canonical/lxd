package main

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
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

func (c *cmdList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:] [<filter>...]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List instances"
	cmd.Long = cli.FormatSection("Description", `List instances

Default column layout: ns46tS
Fast column layout: nsacPt

A single keyword like "web" which will list any instance with a name starting with "web".
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
  t - Type (container or virtual-machine, ephemeral indicated if applicable)
  u - CPU usage (in seconds)
  L - Location of the instance (e.g. its cluster member)
  f - Base Image Fingerprint (short)
  F - Base Image Fingerprint (long)

Custom columns are defined with "[config:|devices:]key[:name][:maxWidth]":
  KEY: The (extended) config or devices key to display. If [config:|devices:] is omitted then it defaults to config key.
  NAME: Name to display in the column header.
  Defaults to the key if not specified or empty.

  MAXWIDTH: Max width of the column (longer results are truncated).
  Defaults to -1 (unlimited). Use 0 to limit to the column header size.`)

	cmd.Example = cli.FormatSection("", `lxc list -c nFs46,volatile.eth0.hwaddr:MAC,config:image.os,devices:eth0.parent:ETHP
  Show instances using the "NAME", "BASE IMAGE", "STATE", "IPV4", "IPV6" and "MAC" columns.
  "BASE IMAGE", "MAC" and "IMAGE OS" are custom columns generated from instance configuration keys.
  "ETHP" is a custom column generated from a device key.

lxc list -c ns,user.comment:comment
  List instances with their running state and user comment.`)

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultColumns, "Columns"+"``")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", "Format (csv|json|table|yaml|compact)"+"``")
	cmd.Flags().BoolVar(&c.flagFast, "fast", false, "Fast mode (same as --columns=nsacPt)")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, "Display instances from all projects")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

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

func (c *cmdList) shouldShow(filters []string, inst *api.Instance, state *api.InstanceState, initial bool) bool {
	c.mapShorthandFilters()

	for _, filter := range filters {
		key, value, ok := strings.Cut(filter, "=")
		if ok {
			if initial || c.evaluateShorthandFilter(key, value, inst, state) {
				continue
			}

			found := false
			for configKey, configValue := range inst.ExpandedConfig {
				if c.dotPrefixMatch(key, configKey) {
					// Try to test filter value as a regexp.
					regexpValue := value
					if !strings.Contains(value, "^") && !strings.Contains(value, "$") {
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
			if !strings.Contains(filter, "^") && !strings.Contains(filter, "$") {
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

func (c *cmdList) evaluateShorthandFilter(key string, value string, inst *api.Instance, state *api.InstanceState) bool {
	const shorthandValueDelimiter = ","
	shorthandFilterFunction, isShorthandFilter := c.shorthandFilters[strings.ToLower(key)]

	if isShorthandFilter {
		if strings.Contains(value, shorthandValueDelimiter) {
			matched := false
			for curValue := range strings.SplitSeq(value, shorthandValueDelimiter) {
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

func (c *cmdList) listInstances(d lxd.InstanceServer, instances []api.Instance, filters []string, columns []column, filtersNeedState bool) error {
	threads := min(len(instances), 10)

	// Shortcut when needing state and snapshot info.
	hasSnapshots := false
	hasState := filtersNeedState
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

		for range threads {
			cInfoWg.Go(func() {
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
			})
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

	for range threads {
		cStatesWg.Go(func() {
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
		})

		cSnapshotsWg.Go(func() {
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
		})
	}

	for _, inst := range instances {
		if hasState && inst.IsActive() {
			cStatesLock.Lock()
			_, ok := cStates[inst.Name]
			if !ok {
				cStates[inst.Name] = nil
			}
			cStatesLock.Unlock()

			if !ok {
				cStatesQueue <- inst.Name
			}
		}

		if hasSnapshots {
			cSnapshotsLock.Lock()
			_, ok := cSnapshots[inst.Name]
			if !ok {
				cSnapshots[inst.Name] = nil
			}
			cSnapshotsLock.Unlock()

			if !ok {
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

func (c *cmdList) showInstances(instances []api.InstanceFull, filters []string, columns []column) error {
	// Generate the table data
	data := [][]string{}
	instancesFiltered := []api.InstanceFull{}

	for _, inst := range instances {
		if !c.shouldShow(filters, &inst.Instance, inst.State, false) {
			continue
		}

		instancesFiltered = append(instancesFiltered, inst)

		col := make([]string, 0, len(columns))
		for _, column := range columns {
			col = append(col, column.Data(inst))
		}

		data = append(data, col)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	headers := make([]string, 0, len(columns))
	for _, column := range columns {
		headers = append(headers, column.Name)
	}

	return cli.RenderTable(c.flagFormat, headers, data, instancesFiltered)
}

func (c *cmdList) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, -1)
	if exit {
		return err
	}

	if c.global.flagProject != "" && c.flagAllProjects {
		return errors.New("Can't specify --project with --all-projects")
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

	filtersNeedState := c.filtersNeedState(filters)
	if filtersNeedState {
		needsData = true
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
	return c.listInstances(d, instancesFiltered, clientFilters, columns, filtersNeedState)
}

func (c *cmdList) parseColumns(clustered bool) ([]column, bool, error) {
	columnsShorthandMap := map[rune]column{
		'4': {"IPV4", c.ipv4ColumnData, true, false},
		'6': {"IPV6", c.ipv6ColumnData, true, false},
		'a': {"ARCHITECTURE", c.architectureColumnData, false, false},
		'b': {"STORAGE POOL", c.storagePoolColumnData, false, false},
		'c': {"CREATED AT", c.createdColumnData, false, false},
		'd': {"DESCRIPTION", c.descriptionColumnData, false, false},
		'D': {"DISK USAGE", c.diskUsageColumnData, true, false},
		'e': {"PROJECT", c.projectColumnData, false, false},
		'f': {"BASE IMAGE", c.baseImageColumnData, false, false},
		'F': {"BASE IMAGE", c.baseImageFullColumnData, false, false},
		'l': {"LAST USED AT", c.lastUsedColumnData, false, false},
		'm': {"MEMORY USAGE", c.memoryUsageColumnData, true, false},
		'M': {"MEMORY USAGE%", c.memoryUsagePercentColumnData, true, false},
		'n': {"NAME", c.nameColumnData, false, false},
		'N': {"PROCESSES", c.numberOfProcessesColumnData, true, false},
		'p': {"PID", c.pidColumnData, true, false},
		'P': {"PROFILES", c.profilesColumnData, false, false},
		'S': {"SNAPSHOTS", c.numberSnapshotsColumnData, false, true},
		's': {"STATE", c.statusColumnData, false, false},
		't': {"TYPE", c.typeColumnData, false, false},
		'u': {"CPU USAGE", c.cpuUsageSecondsColumnData, true, false},
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
			return nil, false, errors.New("Can't specify --fast with --columns")
		}

		if c.flagColumns == defaultColumnsAllProjects {
			c.flagColumns = "ensacPt"
		} else {
			c.flagColumns = "nsacPt"
		}
	}

	if clustered {
		columnsShorthandMap['L'] = column{
			"LOCATION", c.locationColumnData, false, false}
	} else {
		if c.flagColumns != defaultColumns && c.flagColumns != defaultColumnsAllProjects {
			if strings.ContainsAny(c.flagColumns, "L") {
				return nil, false, errors.New("Can't specify column L when not clustered")
			}
		}
		c.flagColumns = strings.ReplaceAll(c.flagColumns, "L", "")
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []column{}
	needsData := false
	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, false, fmt.Errorf("Empty column entry (redundant, leading or trailing command) in %q", c.flagColumns)
		}

		// Config keys always contain a period, parse anything without a
		// period as a series of shorthand runes.
		if !strings.Contains(columnEntry, ".") {
			for _, columnRune := range columnEntry {
				column, ok := columnsShorthandMap[columnRune]
				if !ok {
					return nil, false, fmt.Errorf("Unknown column shorthand char '%c' in %q", columnRune, columnEntry)
				}

				columns = append(columns, column)

				if column.NeedsState || column.NeedsSnapshots {
					needsData = true
				}
			}
		} else {
			cc := strings.Split(columnEntry, ":")
			colType := configColumnType
			if (cc[0] == configColumnType || cc[0] == deviceColumnType) && len(cc) > 1 {
				colType = cc[0]
				cc = slices.Delete(cc, 0, 1)
			}

			if len(cc) > 3 {
				return nil, false, fmt.Errorf("Invalid config key column format (too many fields): %q", columnEntry)
			}

			k := cc[0]
			if colType == configColumnType {
				_, err := instancetype.ConfigKeyChecker(k, instancetype.Any)
				if err != nil {
					return nil, false, fmt.Errorf("Invalid config key %q in %q", k, columnEntry)
				}
			}

			column := column{Name: k}
			if len(cc) > 1 {
				if len(cc[1]) == 0 && len(cc) != 3 {
					return nil, false, fmt.Errorf("Invalid name in %q, empty string is only allowed when defining maxWidth", columnEntry)
				}

				column.Name = cc[1]
			}

			maxWidth := -1
			if len(cc) > 2 {
				temp, err := strconv.ParseInt(cc[2], 10, 32)
				if err != nil {
					return nil, false, fmt.Errorf("Invalid max width (must be an integer) %q in %q", cc[2], columnEntry)
				}

				if temp < -1 {
					return nil, false, fmt.Errorf("Invalid max width (must -1, 0 or a positive integer) %q in %q", cc[2], columnEntry)
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
					deviceName, deviceKey, found := strings.Cut(k, ".")
					if !found {
						return ""
					}

					v, ok := cInfo.Devices[deviceName][deviceKey]
					if !ok {
						v = cInfo.ExpandedDevices[deviceName][deviceKey]
					}

					// Truncate the data according to the max width.  A negative max width
					// indicates there is no effective limit.
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

func (c *cmdList) baseImageColumnData(cInfo api.InstanceFull) string {
	return c.getBaseImage(cInfo, false)
}

func (c *cmdList) baseImageFullColumnData(cInfo api.InstanceFull) string {
	return c.getBaseImage(cInfo, true)
}

func (c *cmdList) nameColumnData(cInfo api.InstanceFull) string {
	return cInfo.Name
}

func (c *cmdList) descriptionColumnData(cInfo api.InstanceFull) string {
	return cInfo.Description
}

func (c *cmdList) statusColumnData(cInfo api.InstanceFull) string {
	return strings.ToUpper(cInfo.Status)
}

func (c *cmdList) ipv4ColumnData(cInfo api.InstanceFull) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.Network != nil {
		ipv4s := []string{}
		for netName, net := range cInfo.State.Network {
			if net.Type == "loopback" {
				continue
			}

			for _, addr := range net.Addresses {
				if slices.Contains([]string{"link", "local"}, addr.Scope) {
					continue
				}

				if addr.Family == "inet" {
					ipv4s = append(ipv4s, addr.Address+" ("+netName+")")
				}
			}
		}

		sort.Sort(sort.Reverse(sort.StringSlice(ipv4s)))
		return strings.Join(ipv4s, "\n")
	}

	return ""
}

func (c *cmdList) ipv6ColumnData(cInfo api.InstanceFull) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.Network != nil {
		ipv6s := []string{}
		for netName, net := range cInfo.State.Network {
			if net.Type == "loopback" {
				continue
			}

			for _, addr := range net.Addresses {
				if slices.Contains([]string{"link", "local"}, addr.Scope) {
					continue
				}

				if addr.Family == "inet6" {
					ipv6s = append(ipv6s, addr.Address+" ("+netName+")")
				}
			}
		}

		sort.Sort(sort.Reverse(sort.StringSlice(ipv6s)))
		return strings.Join(ipv6s, "\n")
	}

	return ""
}

func (c *cmdList) projectColumnData(cInfo api.InstanceFull) string {
	return cInfo.Project
}

func (c *cmdList) memoryUsageColumnData(cInfo api.InstanceFull) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.Memory.Usage > 0 {
		return units.GetByteSizeStringIEC(cInfo.State.Memory.Usage, 2)
	}

	return ""
}

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

func (c *cmdList) cpuUsageSecondsColumnData(cInfo api.InstanceFull) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.CPU.Usage > 0 {
		return fmt.Sprint(cInfo.State.CPU.Usage/1000000000, "s")
	}

	return ""
}

func (c *cmdList) diskUsageColumnData(cInfo api.InstanceFull) string {
	rootDisk, _, _ := instancetype.GetRootDiskDevice(cInfo.ExpandedDevices)

	if cInfo.State != nil && cInfo.State.Disk != nil && cInfo.State.Disk[rootDisk].Usage > 0 {
		return units.GetByteSizeStringIEC(cInfo.State.Disk[rootDisk].Usage, 2)
	}

	return ""
}

func (c *cmdList) typeColumnData(cInfo api.InstanceFull) string {
	instType := "CONTAINER"
	if cInfo.Type == string(api.InstanceTypeVM) {
		instType = "VIRTUAL-MACHINE"
	}

	if cInfo.Ephemeral {
		return instType + " (EPHEMERAL)"
	}

	return instType
}

func (c *cmdList) numberSnapshotsColumnData(cInfo api.InstanceFull) string {
	if cInfo.Snapshots != nil {
		return strconv.Itoa(len(cInfo.Snapshots))
	}

	return "0"
}

func (c *cmdList) pidColumnData(cInfo api.InstanceFull) string {
	if cInfo.IsActive() && cInfo.State != nil {
		return strconv.FormatInt(cInfo.State.Pid, 10)
	}

	return ""
}

func (c *cmdList) architectureColumnData(cInfo api.InstanceFull) string {
	return cInfo.Architecture
}

func (c *cmdList) storagePoolColumnData(cInfo api.InstanceFull) string {
	for _, v := range cInfo.ExpandedDevices {
		if v["type"] == "disk" && v["path"] == "/" {
			return v["pool"]
		}
	}

	return ""
}

func (c *cmdList) profilesColumnData(cInfo api.InstanceFull) string {
	return strings.Join(cInfo.Profiles, "\n")
}

func (c *cmdList) createdColumnData(cInfo api.InstanceFull) string {
	layout := "2006/01/02 15:04 UTC"

	if shared.TimeIsSet(cInfo.CreatedAt) {
		return cInfo.CreatedAt.UTC().Format(layout)
	}

	return ""
}

func (c *cmdList) lastUsedColumnData(cInfo api.InstanceFull) string {
	layout := "2006/01/02 15:04 UTC"

	if !cInfo.LastUsedAt.IsZero() && shared.TimeIsSet(cInfo.LastUsedAt) {
		return cInfo.LastUsedAt.UTC().Format(layout)
	}

	return ""
}

func (c *cmdList) numberOfProcessesColumnData(cInfo api.InstanceFull) string {
	if cInfo.IsActive() && cInfo.State != nil {
		return strconv.FormatInt(cInfo.State.Processes, 10)
	}

	return ""
}

func (c *cmdList) locationColumnData(cInfo api.InstanceFull) string {
	return cInfo.Location
}

func (c *cmdList) matchByType(cInfo *api.Instance, cState *api.InstanceState, query string) bool {
	return strings.EqualFold(cInfo.Type, query)
}

func (c *cmdList) matchByStatus(cInfo *api.Instance, cState *api.InstanceState, query string) bool {
	return strings.EqualFold(cInfo.Status, query)
}

func (c *cmdList) matchByArchitecture(cInfo *api.Instance, cState *api.InstanceState, query string) bool {
	return strings.EqualFold(cInfo.Architecture, query)
}

func (c *cmdList) matchByLocation(cInfo *api.Instance, cState *api.InstanceState, query string) bool {
	return strings.EqualFold(cInfo.Location, query)
}

func (c *cmdList) matchByNet(cState *api.InstanceState, query string, family string) bool {
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

func (c *cmdList) matchByIPV6(cInfo *api.Instance, cState *api.InstanceState, query string) bool {
	return c.matchByNet(cState, query, "ipv6")
}

func (c *cmdList) matchByIPV4(cInfo *api.Instance, cState *api.InstanceState, query string) bool {
	return c.matchByNet(cState, query, "ipv4")
}

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

func (c *cmdList) filtersNeedState(filters []string) bool {
	for _, filter := range filters {
		key, _, found := strings.Cut(filter, "=")
		if !found {
			continue
		}

		switch strings.ToLower(strings.TrimSpace(key)) {
		case "ipv4", "ipv6":
			return true
		}
	}

	return false
}
