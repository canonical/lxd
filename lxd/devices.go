package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/cgroup"
	"github.com/canonical/lxd/lxd/device"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/linux"
	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/validate"
)

type deviceTaskCPU struct {
	id       int64
	strID    string
	count    *int
	isolated bool
}

type deviceTaskCPUs []deviceTaskCPU

func (c deviceTaskCPUs) Len() int           { return len(c) }
func (c deviceTaskCPUs) Less(i, j int) bool { return *c[i].count < *c[j].count }
func (c deviceTaskCPUs) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }

type instanceCPUPinningMap map[instance.Instance][]string

func (pinning instanceCPUPinningMap) add(inst instance.Instance, cpu deviceTaskCPU) {
	id := cpu.strID
	_, ok := pinning[inst]
	if ok {
		pinning[inst] = append(pinning[inst], id)
	} else {
		pinning[inst] = []string{id}
	}
	*cpu.count += 1
}

func deviceNetlinkListener() (chCPU chan []string, chNetwork chan []string, chUSB chan device.USBEvent, chUnix chan device.UnixHotplugEvent, err error) {
	NETLINK_KOBJECT_UEVENT := 15 //nolint:revive
	UEVENT_BUFFER_SIZE := 2048   //nolint:revive

	fd, err := unix.Socket(
		unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC,
		NETLINK_KOBJECT_UEVENT,
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	nl := unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Pid:    uint32(os.Getpid()),
		Groups: 3,
	}

	err = unix.Bind(fd, &nl)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	chCPU = make(chan []string, 1)
	chNetwork = make(chan []string)
	chUSB = make(chan device.USBEvent)
	chUnix = make(chan device.UnixHotplugEvent)

	go func(chCPU chan []string, chNetwork chan []string, chUSB chan device.USBEvent, chUnix chan device.UnixHotplugEvent) {
		b := make([]byte, UEVENT_BUFFER_SIZE*2)
		for {
			r, err := unix.Read(fd, b)
			if err != nil {
				continue
			}

			ueventBuf := make([]byte, r)
			copy(ueventBuf, b)

			udevEvent := false
			if strings.HasPrefix(string(ueventBuf), "libudev") {
				udevEvent = true
				// Skip the header that libudev prepends
				ueventBuf = ueventBuf[40 : len(ueventBuf)-1]
			}

			ueventLen := 0
			ueventParts := strings.Split(string(ueventBuf), "\x00")
			for i, part := range ueventParts {
				if strings.HasPrefix(part, "SEQNUM=") {
					ueventParts = slices.Delete(ueventParts, i, i+1)
					break
				}
			}

			props := map[string]string{}
			for _, part := range ueventParts {
				// libudev string prefix distinguishes udev events from kernel uevents
				if strings.HasPrefix(part, "libudev") {
					udevEvent = true
					continue
				}

				ueventLen += len(part) + 1

				key, value, found := strings.Cut(part, "=")
				if !found {
					continue
				}

				props[key] = value
			}

			ueventLen--

			if udevEvent {
				// The kernel always prepends this and udev expects it.
				kernelPrefix := props["ACTION"] + "@" + props["DEVPATH"]
				ueventParts = append([]string{kernelPrefix}, ueventParts...)
				ueventLen += len(kernelPrefix)
			}

			if props["SUBSYSTEM"] == "cpu" && !udevEvent {
				if props["DRIVER"] != "processor" {
					continue
				}

				if props["ACTION"] != "offline" && props["ACTION"] != "online" {
					continue
				}

				// As CPU re-balancing affects all containers, no need to queue them
				select {
				case chCPU <- []string{path.Base(props["DEVPATH"]), props["ACTION"]}:
				default:
					// Channel is full, drop the event
				}
			}

			if props["SUBSYSTEM"] == "net" && !udevEvent {
				if props["ACTION"] != "add" && props["ACTION"] != "removed" {
					continue
				}

				if !shared.PathExists("/sys/class/net/" + props["INTERFACE"]) {
					continue
				}

				// Network balancing is interface specific, so queue everything
				chNetwork <- []string{props["INTERFACE"], props["ACTION"]}
			}

			if props["SUBSYSTEM"] == "usb" && !udevEvent {
				parts := strings.Split(props["PRODUCT"], "/")
				if len(parts) < 2 {
					continue
				}

				serial, ok := props["SERIAL"]
				if !ok {
					continue
				}

				major, ok := props["MAJOR"]
				if !ok {
					continue
				}

				minor, ok := props["MINOR"]
				if !ok {
					continue
				}

				devname, ok := props["DEVNAME"]
				if !ok {
					continue
				}

				busnum, ok := props["BUSNUM"]
				if !ok {
					continue
				}

				devnum, ok := props["DEVNUM"]
				if !ok {
					continue
				}

				zeroPad := func(s string, l int) string {
					return strings.Repeat("0", l-len(s)) + s
				}

				usb, err := device.USBNewEvent(
					props["ACTION"],
					/* udev doesn't zero pad these, while
					 * everything else does, so let's zero pad them
					 * for consistency
					 */
					zeroPad(parts[0], 4),
					zeroPad(parts[1], 4),
					serial,
					major,
					minor,
					busnum,
					devnum,
					devname,
					ueventParts[:len(ueventParts)-1],
					ueventLen,
				)
				if err != nil {
					logger.Error("Error reading usb device", logger.Ctx{"err": err, "path": props["PHYSDEVPATH"]})
					continue
				}

				chUSB <- usb
			}

			// unix hotplug device events rely on information added by udev
			if udevEvent {
				action := props["ACTION"]
				if action != "add" && action != "remove" {
					continue
				}

				subsystem, ok := props["SUBSYSTEM"]
				if !ok {
					continue
				}

				devname, ok := props["DEVNAME"]
				if !ok {
					continue
				}

				major, ok := props["MAJOR"]
				if !ok {
					continue
				}

				minor, ok := props["MINOR"]
				if !ok {
					continue
				}

				vendor := ""
				product := ""
				if action == "add" {
					vendor, product, ok = ueventParseVendorProduct(props, subsystem, devname)
					if !ok {
						continue
					}
				}

				zeroPad := func(s string, l int) string {
					return strings.Repeat("0", l-len(s)) + s
				}

				// zeropad
				if len(vendor) < 4 {
					vendor = zeroPad(vendor, 4)
				}

				if len(product) < 4 {
					product = zeroPad(product, 4)
				}

				unix, err := device.UnixHotplugNewEvent(
					action,
					/* udev doesn't zero pad these, while
					 * everything else does, so let's zero pad them
					 * for consistency
					 */
					vendor,
					product,
					major,
					minor,
					subsystem,
					devname,
					ueventParts[:len(ueventParts)-1],
					ueventLen,
				)
				if err != nil {
					logger.Error("Error reading unix device", logger.Ctx{"err": err, "path": props["PHYSDEVPATH"]})
					continue
				}

				chUnix <- unix
			}
		}
	}(chCPU, chNetwork, chUSB, chUnix)

	return chCPU, chNetwork, chUSB, chUnix, nil
}

/*
 * fillFixedInstances fills the `fixedInstances` map with the instances that have been pinned to specific CPUs.
 * The `fixedInstances` map is a map of CPU IDs to a list of instances that have been pinned to that CPU.
 * The `targetCPUPool` is a list of CPU IDs that are available for pinning.
 * The `targetCPUNum` is the number of CPUs that are required for pinning.
 * The `loadBalancing` flag indicates whether the CPU pinning should be load balanced or not (e.g, NUMA placement when `limits.cpu` is a single number which means
 * a required number of vCPUs per instance that can be chosen within a CPU pool).
 */
func fillFixedInstances(fixedInstances map[int64][]instance.Instance, inst instance.Instance, usableCpus []int64, targetCPUPool []int64, targetCPUNum int, loadBalancing bool) {
	if len(targetCPUPool) < targetCPUNum {
		diffCount := len(targetCPUPool) - targetCPUNum
		logger.Warn("Insufficient CPUs in the target pool for required pinning: reducing required CPUs to match available CPUs", logger.Ctx{"available": len(targetCPUPool), "required": targetCPUNum, "difference": -diffCount})
		targetCPUNum = len(targetCPUPool)
	}

	// If the `targetCPUPool` has been manually specified (explicit CPU IDs/ranges specified with `limits.cpu`)
	if len(targetCPUPool) == targetCPUNum && !loadBalancing {
		for _, nr := range targetCPUPool {
			if !slices.Contains(usableCpus, nr) {
				logger.Warn("Instance using unavailable cpu", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "cpu": nr})
				continue
			}

			_, ok := fixedInstances[nr]
			if ok {
				fixedInstances[nr] = append(fixedInstances[nr], inst)
			} else {
				fixedInstances[nr] = []instance.Instance{inst}
			}
		}

		return
	}

	// If we need to load-balance the instance across the CPUs of `targetCPUPool` (e.g, NUMA placement),
	// the heuristic is to sort the `targetCPUPool` by usage (number of instances already pinned to each CPU)
	// and then assign the instance to the first `desiredCpuNum` least used CPUs.
	usage := map[int64]deviceTaskCPU{}
	for _, id := range targetCPUPool {
		cpu := deviceTaskCPU{}
		cpu.id = id
		cpu.strID = strconv.FormatInt(id, 10)

		count := 0
		_, ok := fixedInstances[id]
		if ok {
			count = len(fixedInstances[id])
		}

		cpu.count = &count
		usage[id] = cpu
	}

	sortedUsage := make(deviceTaskCPUs, 0)
	for _, value := range usage {
		sortedUsage = append(sortedUsage, value)
	}

	sort.Sort(sortedUsage)
	count := 0
	for _, cpu := range sortedUsage {
		if count == targetCPUNum {
			break
		}

		id := cpu.id
		_, ok := fixedInstances[id]
		if ok {
			fixedInstances[id] = append(fixedInstances[id], inst)
		} else {
			fixedInstances[id] = []instance.Instance{inst}
		}

		count++
	}
}

// getCPULists returns lists of CPUs:
// cpus: list of CPUs which are available for instances to be automatically pinned on,
// isolCPUs: list of CPUs listed in "isolcpus" kernel parameter (not available for automatic pinning).
func getCPULists() (cpus []int64, isolCPUs []int64, err error) {
	// Get effective cpus list - those are all guaranteed to be online
	cg, err := cgroup.NewFileReadWriter(1)
	if err != nil {
		logger.Error("Unable to load cgroup writer", logger.Ctx{"err": err})
		return nil, nil, err
	}

	effectiveCPUs, err := cg.GetEffectiveCpuset()
	if err != nil {
		// Older kernel - use cpuset.cpus
		effectiveCPUs, err = cg.GetCpuset()
		if err != nil {
			logger.Error("Error reading host's cpuset.cpus", logger.Ctx{"err": err, "cpuset.cpus": effectiveCPUs})
			return nil, nil, err
		}
	}

	effectiveCPUsInt, err := resources.ParseCpuset(effectiveCPUs)
	if err != nil {
		logger.Error("Error parsing effective CPU set", logger.Ctx{"err": err, "cpuset.cpus": effectiveCPUs})
		return nil, nil, err
	}

	isolCPUs = resources.GetCPUIsolated()
	effectiveCPUsSlice := []string{}
	for _, id := range effectiveCPUsInt {
		if slices.Contains(isolCPUs, id) {
			continue
		}

		effectiveCPUsSlice = append(effectiveCPUsSlice, strconv.FormatInt(id, 10))
	}

	effectiveCPUs = strings.Join(effectiveCPUsSlice, ",")
	cpus, err = resources.ParseCpuset(effectiveCPUs)
	if err != nil {
		logger.Error("Error parsing host's cpu set", logger.Ctx{"cpuset": effectiveCPUs, "err": err})
		return nil, nil, err
	}

	return cpus, isolCPUs, nil
}

// getNumaNodeToCPUMap returns map of NUMA node to CPU threads IDs.
func getNumaNodeToCPUMap() (numaNodeToCPU map[int64][]int64, err error) {
	// Get CPU topology.
	cpusTopology, err := resources.GetCPU()
	if err != nil {
		logger.Error("Unable to load system CPUs information", logger.Ctx{"err": err})
		return nil, err
	}

	// Build a map of NUMA node to CPU threads.
	numaNodeToCPU = make(map[int64][]int64)
	for _, cpu := range cpusTopology.Sockets {
		for _, core := range cpu.Cores {
			for _, thread := range core.Threads {
				numaNodeToCPU[int64(thread.NUMANode)] = append(numaNodeToCPU[int64(thread.NUMANode)], thread.ID)
			}
		}
	}

	return numaNodeToCPU, err
}

func makeCPUUsageMap(cpus, isolCPUs []int64) map[int64]deviceTaskCPU {
	usage := make(map[int64]deviceTaskCPU, len(cpus)+len(isolCPUs))

	// Helper function to create deviceTaskCPU
	createCPU := func(id int64, isolated bool) deviceTaskCPU {
		count := 0
		return deviceTaskCPU{
			id:       id,
			strID:    strconv.FormatInt(id, 10),
			count:    &count,
			isolated: isolated,
		}
	}

	// Process regular CPUs
	for _, id := range cpus {
		usage[id] = createCPU(id, false)
	}

	// Process isolated CPUs
	for _, id := range isolCPUs {
		usage[id] = createCPU(id, true)
	}

	return usage
}

func getNumaCPUs(numaNodeToCPU map[int64][]int64, cpuNodes string) ([]int64, error) {
	var numaCpus []int64
	if cpuNodes != "" {
		numaNodeSet, err := resources.ParseNumaNodeSet(cpuNodes)
		if err != nil {
			return nil, err
		}

		for _, numaNode := range numaNodeSet {
			numaCpus = append(numaCpus, numaNodeToCPU[numaNode]...)
		}
	}

	return numaCpus, nil
}

// deviceTaskBalance is used to balance the CPU load across instances running on a host.
// It first checks if CGroup support is available and returns if it isn't.
// It then retrieves the effective CPU list (the CPUs that are guaranteed to be online) and isolates any isolated CPUs.
// After that, it loads all instances running on the node and iterates through them.
//
// For each instance, it checks its CPU limits and determines whether it is pinned to specific CPUs or can use the load-balancing mechanism.
// If it is pinned, the function adds it to the fixedInstances map with the CPU numbers it is pinned to.
// If not, the instance will be included in the load-balancing calculation,
// and the number of CPUs it can use is determined by taking the minimum of its assigned CPUs and the available CPUs. Note that if
// NUMA placement is enabled (`limits.cpu.nodes` is not empty), we apply a similar load-balancing logic to the `fixedInstances` map
// with a constraint being the number of vCPUs and the CPU pool being the CPUs pinned to a set of NUMA nodes.
//
// Next, the function balance the CPU usage by iterating over all the CPUs and dividing the instances into those that
// are pinned to a specific CPU and those that are load-balanced. For the pinned instances,
// it adds them to the pinning map with the CPU number it's pinned to.
// For the load-balanced instances, it sorts the available CPUs based on their usage count and assigns them to instances
// in ascending order until the required number of CPUs have been assigned.
// Finally, the pinning map is used to set the new CPU pinning for each instance, updating it to the new balanced state.
//
// Overall, this function ensures that the CPU resources of the host are utilized effectively amongst all the instances running on it.
func deviceTaskBalance(s *state.State) {
	// Don't bother running when CGroup support isn't there
	if !s.OS.CGInfo.Supports(cgroup.CPUSet, nil) {
		return
	}

	cpus, isolCPUs, err := getCPULists()
	if err != nil {
		return
	}

	// Iterate through the instances
	instances, err := instance.LoadNodeAll(s, instancetype.Any)
	if err != nil {
		logger.Error("Problem loading instances list", logger.Ctx{"err": err})
		return
	}

	numaNodeToCPU, err := getNumaNodeToCPUMap()
	if err != nil {
		return
	}

	fixedInstances := map[int64][]instance.Instance{}
	balancedInstances := map[instance.Instance]int{}
	for _, c := range instances {
		conf := c.ExpandedConfig()
		cpuNodes := conf["limits.cpu.nodes"]
		numaCpus, err := getNumaCPUs(numaNodeToCPU, cpuNodes)
		if err != nil {
			logger.Error("Error parsing numa node set", logger.Ctx{"numaNodes": cpuNodes, "err": err})
			return
		}

		cpulimit, ok := conf["limits.cpu"]
		if !ok || cpulimit == "" {
			// For VMs empty limits.cpu means 1,
			// but for containers it means "unlimited"
			if c.Type() == instancetype.VM {
				cpulimit = "1"
			} else {
				cpulimit = strconv.Itoa(len(cpus))
			}
		}

		// Determine CPU pinning strategy and static pinning settings.
		// When pinning strategy does not equal auto (none or empty), don't auto pin CPUs.
		cpuPinStrategy := conf["limits.cpu.pin_strategy"]
		err = validate.IsStaticCPUPinning(cpulimit)
		if err != nil && c.Type() == instancetype.VM && cpuPinStrategy != "auto" {
			continue
		}

		// Check that the instance is running.
		// We use InitPID here rather than IsRunning because this task can be triggered during the container's
		// onStart hook, which is during the time that the start lock is held, which causes IsRunning to
		// return false (because the container hasn't fully started yet) but it is sufficiently started to
		// have its cgroup CPU limits set.
		if c.InitPID() <= 0 {
			continue
		}

		count, err := strconv.Atoi(cpulimit)
		if err == nil {
			// Load-balance
			count = min(count, len(cpus))
			if len(numaCpus) > 0 {
				fillFixedInstances(fixedInstances, c, cpus, numaCpus, count, true)
			} else {
				balancedInstances[c] = count
			}
		} else {
			// Pinned
			instanceCpus, err := resources.ParseCpuset(cpulimit)
			if err != nil {
				return
			}

			if len(numaCpus) > 0 {
				logger.Warn("The pinned CPUs override the NUMA configuration CPUs", logger.Ctx{"pinnedCPUs": instanceCpus, "numaCPUs": numaCpus})
			}

			usableCpus := cpus
			//
			// For VM instances, historically, we allow explicit pinning to a CPUs
			// listed in "isolcpus" kernel parameter. While for containers it was
			// never allowed and let's keep it like this.
			//
			if c.Type() == instancetype.VM {
				usableCpus = append(usableCpus, isolCPUs...)
			}

			fillFixedInstances(fixedInstances, c, usableCpus, instanceCpus, len(instanceCpus), false)
		}
	}

	// Balance things
	pinning := instanceCPUPinningMap{}
	usage := makeCPUUsageMap(cpus, isolCPUs)

	// Handle instances with explicit CPU pinnings
	for cpu, instances := range fixedInstances {
		c, ok := usage[cpu]
		if !ok {
			logger.Error("Internal error: instance using unavailable cpu", logger.Ctx{"cpu": cpu})
			continue
		}

		for _, inst := range instances {
			pinning.add(inst, c)
		}
	}

	sortedUsage := make(deviceTaskCPUs, 0)
	for _, cpu := range usage {
		if cpu.isolated {
			continue
		}

		sortedUsage = append(sortedUsage, cpu)
	}

	// Handle instances where automatic CPU pinning is needed
	for inst, cpusToPin := range balancedInstances {
		sort.Sort(sortedUsage)
		for _, cpu := range sortedUsage {
			if cpusToPin == 0 {
				break
			}

			cpusToPin -= 1
			pinning.add(inst, cpu)
		}
	}

	// Set the new pinning
	for inst, set := range pinning {
		err = inst.SetAffinity(set)
		if err != nil {
			logger.Error("Error setting CPU affinity for the instance", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "err": err})
		}
	}
}

// deviceEventListener starts the event listener for resource scheduling.
// Accepts stateFunc which will be called each time it needs a fresh state.State.
func deviceEventListener(stateFunc func() *state.State) {
	chNetlinkCPU, chNetlinkNetwork, chUSB, chUnix, err := deviceNetlinkListener()
	if err != nil {
		logger.Error("Scheduler: Couldn't setup netlink listener", logger.Ctx{"err": err})
		return
	}

	for {
		select {
		case e := <-chNetlinkCPU:
			if len(e) != 2 {
				logger.Error("Scheduler: received an invalid cpu hotplug event")
				continue
			}

			s := stateFunc()

			if !s.OS.CGInfo.Supports(cgroup.CPUSet, nil) {
				continue
			}

			logger.Debug("Scheduler: re-balancing", logger.Ctx{"devpath": e[0], "action": e[1]})
			deviceTaskBalance(s)
		case e := <-chNetlinkNetwork:
			if len(e) != 2 {
				logger.Error("Scheduler: received an invalid network hotplug event")
				continue
			}

			s := stateFunc()

			// we want to catch all new devices at the host and process them in networkAutoAttach
			if e[1] != "add" {
				continue
			}

			logger.Debug("Scheduler: network has been added: updating network priorities", logger.Ctx{"network": e[0]})
			err = networkAutoAttach(s.DB.Cluster, e[0])
			if err != nil {
				logger.Warn("Failed to auto-attach network", logger.Ctx{"err": err, "dev": e[0]})
			}

		case e := <-chUSB:
			device.USBRunHandlers(stateFunc(), &e)
		case e := <-chUnix:
			device.UnixHotplugRunHandlers(stateFunc(), &e)
		case e := <-cgroup.DeviceSchedRebalance:
			if len(e) != 3 {
				logger.Error("Scheduler: received an invalid rebalance event")
				continue
			}

			s := stateFunc()

			if !s.OS.CGInfo.Supports(cgroup.CPUSet, nil) {
				continue
			}

			logger.Debug("Scheduler: re-balancing", logger.Ctx{"srcName": e[0], "srcType": e[1], "srcStatus": e[2]})
			deviceTaskBalance(s)
		}
	}
}

// devicesRegister calls the Register() function on all supported devices so they receive events.
// This also has the effect of actively reconnecting to any running VM monitor sockets.
func devicesRegister(instances []instance.Instance) {
	logger.Debug("Registering running instances")

	for _, inst := range instances {
		if !inst.IsRunning() { // For VMs this will also trigger a connection to the QMP socket if running.
			continue
		}

		inst.RegisterDevices()
	}
}

func getHidrawDevInfo(fd int) (vendor string, product string, err error) {
	type hidInfo struct {
		busType uint32
		vendor  int16
		product int16
	}

	var info hidInfo
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), linux.IoctlHIDIOCGrawInfo, uintptr(unsafe.Pointer(&info)))
	if errno != 0 {
		return "", "", fmt.Errorf("Failed setting received UUID: %w", unix.Errno(errno))
	}

	return fmt.Sprintf("%04x", info.vendor), fmt.Sprintf("%04x", info.product), nil
}

func ueventParseVendorProduct(props map[string]string, subsystem string, devname string) (vendor string, product string, ok bool) {
	vendor, vendorOk := props["ID_VENDOR_ID"]
	product, productOk := props["ID_MODEL_ID"]

	if vendorOk && productOk {
		return vendor, product, true
	}

	// Retrieve missing device info.
	if subsystem == "hidraw" {
		if !filepath.IsAbs(devname) {
			return "", "", false
		}

		file, err := os.OpenFile(devname, os.O_RDWR, 0000)
		if err != nil {
			return "", "", false
		}

		defer func() { _ = file.Close() }()

		vendor, product, err = getHidrawDevInfo(int(file.Fd()))
		if err != nil {
			logger.Debug("Failed to retrieve device info from hidraw device", logger.Ctx{"devname": devname})
			return "", "", false
		}
	}

	return vendor, product, true
}
