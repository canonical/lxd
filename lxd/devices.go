package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"

	log "github.com/lxc/lxd/shared/log15"
)

var deviceSchedRebalance = make(chan []string, 2)

type deviceBlockLimit struct {
	readBps   int64
	readIops  int64
	writeBps  int64
	writeIops int64
}

type deviceTaskCPU struct {
	id    int
	strId string
	count *int
}
type deviceTaskCPUs []deviceTaskCPU

func (c deviceTaskCPUs) Len() int           { return len(c) }
func (c deviceTaskCPUs) Less(i, j int) bool { return *c[i].count < *c[j].count }
func (c deviceTaskCPUs) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }

type usbDevice struct {
	action string

	vendor  string
	product string

	path  string
	major int
	minor int
}

// /dev/nvidia[0-9]+
type nvidiaGpuCards struct {
	path  string
	major int
	minor int
	id    string
}

// {/dev/nvidiactl, /dev/nvidia-uvm, ...}
type nvidiaGpuDevices struct {
	path  string
	major int
	minor int
}

// /dev/dri/card0. If we detect that vendor == nvidia, then nvidia will contain
// the corresponding nvidia car, e.g. {/dev/dri/card1 --> /dev/nvidia1}.
type gpuDevice struct {
	vendorid  string
	productid string
	id        string // card id e.g. 0
	// If related devices have the same PCI address as the GPU we should
	// mount them all. Meaning if we detect /dev/dri/card0,
	// /dev/dri/controlD64, and /dev/dri/renderD128 with the same PCI
	// address, then they should all be made available in the container.
	pci    string
	nvidia nvidiaGpuCards

	path  string
	major int
	minor int
}

func (g *gpuDevice) isNvidiaGpu() bool {
	return strings.EqualFold(g.vendorid, "10de")
}

type cardIds struct {
	id  string
	pci string
}

func deviceLoadGpu() ([]gpuDevice, []nvidiaGpuDevices, error) {
	const DRI_PATH = "/sys/bus/pci/devices"
	var gpus []gpuDevice
	var nvidiaDevices []nvidiaGpuDevices
	var cards []cardIds

	ents, err := ioutil.ReadDir(DRI_PATH)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	isNvidia := false
	for _, ent := range ents {
		// The pci address == the name of the directory. So let's use
		// this cheap way of retrieving it.
		pciAddr := ent.Name()

		// Make sure that we are dealing with a GPU by looking whether
		// the "drm" subfolder exists.
		drm := filepath.Join(DRI_PATH, pciAddr, "drm")
		drmEnts, err := ioutil.ReadDir(drm)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
		}

		// Retrieve vendor ID.
		vendorIdPath := filepath.Join(DRI_PATH, pciAddr, "vendor")
		vendorId, err := ioutil.ReadFile(vendorIdPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
		}

		// Retrieve device ID.
		productIdPath := filepath.Join(DRI_PATH, pciAddr, "device")
		productId, err := ioutil.ReadFile(productIdPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
		}

		// Store all associated subdevices, e.g. controlD64, renderD128.
		// The name of the directory == the last part of the
		// /dev/dri/controlD64 path. So ent.Name() will give us
		// controlD64.
		for _, drmEnt := range drmEnts {
			vendorTmp := strings.TrimSpace(string(vendorId))
			productTmp := strings.TrimSpace(string(productId))
			vendorTmp = strings.TrimPrefix(vendorTmp, "0x")
			productTmp = strings.TrimPrefix(productTmp, "0x")
			tmpGpu := gpuDevice{
				pci:       pciAddr,
				vendorid:  vendorTmp,
				productid: productTmp,
				path:      filepath.Join("/dev/dri", drmEnt.Name()),
			}

			majMinPath := filepath.Join(drm, drmEnt.Name(), "dev")
			majMinByte, err := ioutil.ReadFile(majMinPath)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
			}
			majMin := strings.TrimSpace(string(majMinByte))
			majMinSlice := strings.Split(string(majMin), ":")
			if len(majMinSlice) != 2 {
				continue
			}
			majorInt, err := strconv.Atoi(majMinSlice[0])
			if err != nil {
				continue
			}
			minorInt, err := strconv.Atoi(majMinSlice[1])
			if err != nil {
				continue
			}

			tmpGpu.major = majorInt
			tmpGpu.minor = minorInt

			isCard, err := regexp.MatchString("^card[0-9]+", drmEnt.Name())
			if err != nil {
				continue
			}

			// Find matching /dev/nvidia* entry for /dev/dri/card*
			if tmpGpu.isNvidiaGpu() && isCard {
				if !isNvidia {
					isNvidia = true
				}

				nvidiaPath := fmt.Sprintf("/proc/driver/nvidia/gpus/%s/information", tmpGpu.pci)
				buf, err := ioutil.ReadFile(nvidiaPath)
				if err != nil {
					if os.IsNotExist(err) {
						continue
					}

					return nil, nil, err
				}
				strBuf := strings.TrimSpace(string(buf))
				idx := strings.Index(strBuf, "Device Minor:")
				idx += len("Device Minor:")
				strBuf = strBuf[idx:]
				strBuf = strings.TrimSpace(strBuf)
				idx = strings.Index(strBuf, " ")
				if idx == -1 {
					idx = strings.Index(strBuf, "\t")
				}
				if idx >= 1 {
					strBuf = strBuf[:idx]
				}

				if strBuf == "" {
					return nil, nil, fmt.Errorf("No device minor index detected")
				}

				_, err = strconv.Atoi(strBuf)
				if err != nil {
					return nil, nil, err
				}

				nvidiaPath = "/dev/nvidia" + strBuf
				stat := syscall.Stat_t{}
				err = syscall.Stat(nvidiaPath, &stat)
				if err != nil {
					if os.IsNotExist(err) {
						continue
					}

					return nil, nil, err
				}
				tmpGpu.nvidia.path = nvidiaPath
				tmpGpu.nvidia.major = shared.Major(stat.Rdev)
				tmpGpu.nvidia.minor = shared.Minor(stat.Rdev)
				tmpGpu.nvidia.id = strconv.Itoa(tmpGpu.nvidia.minor)
			}

			if isCard {
				// If it is a card it's minor number will be its id.
				tmpGpu.id = strconv.Itoa(minorInt)
				tmp := cardIds{
					id:  tmpGpu.id,
					pci: tmpGpu.pci,
				}
				cards = append(cards, tmp)
			}
			gpus = append(gpus, tmpGpu)
		}
	}

	// We detected a Nvidia card, so let's collect all other nvidia devices
	// that are not /dev/nvidia[0-9]+.
	if isNvidia {
		nvidiaEnts, err := ioutil.ReadDir("/dev")
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil, err
			}
		}
		validNvidia, err := regexp.Compile(`^nvidia[^0-9]+`)
		if err != nil {
			return nil, nil, err
		}
		for _, nvidiaEnt := range nvidiaEnts {
			if !validNvidia.MatchString(nvidiaEnt.Name()) {
				continue
			}
			nvidiaPath := filepath.Join("/dev", nvidiaEnt.Name())
			stat := syscall.Stat_t{}
			err = syscall.Stat(nvidiaPath, &stat)
			if err != nil {
				continue
			}
			tmpNividiaGpu := nvidiaGpuDevices{
				path:  nvidiaPath,
				major: shared.Major(stat.Rdev),
				minor: shared.Minor(stat.Rdev),
			}
			nvidiaDevices = append(nvidiaDevices, tmpNividiaGpu)
		}

	}

	// Since we'll give users to ability to specify and id we need to group
	// devices on the same PCI that belong to the same card by id.
	for _, card := range cards {
		for i := 0; i < len(gpus); i++ {
			if gpus[i].pci == card.pci {
				gpus[i].id = card.id
			}
		}
	}

	return gpus, nvidiaDevices, nil
}

func createUSBDevice(action string, vendor string, product string, major string, minor string, busnum string, devnum string, devname string) (usbDevice, error) {
	majorInt, err := strconv.Atoi(major)
	if err != nil {
		return usbDevice{}, err
	}

	minorInt, err := strconv.Atoi(minor)
	if err != nil {
		return usbDevice{}, err
	}

	path := devname
	if devname == "" {
		busnumInt, err := strconv.Atoi(busnum)
		if err != nil {
			return usbDevice{}, err
		}

		devnumInt, err := strconv.Atoi(devnum)
		if err != nil {
			return usbDevice{}, err
		}
		path = fmt.Sprintf("/dev/bus/usb/%03d/%03d", busnumInt, devnumInt)
	} else {
		if !filepath.IsAbs(devname) {
			path = fmt.Sprintf("/dev/%s", devname)
		}
	}

	return usbDevice{
		action,
		vendor,
		product,
		path,
		majorInt,
		minorInt,
	}, nil
}

func deviceNetlinkListener() (chan []string, chan []string, chan usbDevice, error) {
	NETLINK_KOBJECT_UEVENT := 15
	UEVENT_BUFFER_SIZE := 2048

	fd, err := syscall.Socket(
		syscall.AF_NETLINK, syscall.SOCK_RAW,
		NETLINK_KOBJECT_UEVENT,
	)

	if err != nil {
		return nil, nil, nil, err
	}

	nl := syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Pid:    uint32(os.Getpid()),
		Groups: 1,
	}

	err = syscall.Bind(fd, &nl)
	if err != nil {
		return nil, nil, nil, err
	}

	chCPU := make(chan []string, 1)
	chNetwork := make(chan []string, 0)
	chUSB := make(chan usbDevice)

	go func(chCPU chan []string, chNetwork chan []string, chUSB chan usbDevice) {
		b := make([]byte, UEVENT_BUFFER_SIZE*2)
		for {
			_, err := syscall.Read(fd, b)
			if err != nil {
				continue
			}

			props := map[string]string{}
			last := 0
			for i, e := range b {
				if i == len(b) || e == 0 {
					msg := string(b[last+1 : i])
					last = i
					if len(msg) == 0 || msg == "\x00" {
						continue
					}

					fields := strings.SplitN(msg, "=", 2)
					if len(fields) != 2 {
						continue
					}

					props[fields[0]] = fields[1]
				}
			}

			if props["SUBSYSTEM"] == "cpu" {
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

			if props["SUBSYSTEM"] == "net" {
				if props["ACTION"] != "add" && props["ACTION"] != "removed" {
					continue
				}

				if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", props["INTERFACE"])) {
					continue
				}

				// Network balancing is interface specific, so queue everything
				chNetwork <- []string{props["INTERFACE"], props["ACTION"]}
			}

			if props["SUBSYSTEM"] == "usb" {
				if props["ACTION"] != "add" && props["ACTION"] != "remove" {
					continue
				}

				parts := strings.Split(props["PRODUCT"], "/")
				if len(parts) < 2 {
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

				usb, err := createUSBDevice(
					props["ACTION"],
					/* udev doesn't zero pad these, while
					 * everything else does, so let's zero pad them
					 * for consistency
					 */
					zeroPad(parts[0], 4),
					zeroPad(parts[1], 4),
					major,
					minor,
					busnum,
					devnum,
					devname,
				)
				if err != nil {
					logger.Error("error reading usb device", log.Ctx{"err": err, "path": props["PHYSDEVPATH"]})
					continue
				}

				chUSB <- usb
			}

		}
	}(chCPU, chNetwork, chUSB)

	return chCPU, chNetwork, chUSB, nil
}

func parseCpuset(cpu string) ([]int, error) {
	cpus := []int{}
	chunks := strings.Split(cpu, ",")
	for _, chunk := range chunks {
		if strings.Contains(chunk, "-") {
			// Range
			fields := strings.SplitN(chunk, "-", 2)
			if len(fields) != 2 {
				return nil, fmt.Errorf("Invalid cpuset value: %s", cpu)
			}

			low, err := strconv.Atoi(fields[0])
			if err != nil {
				return nil, fmt.Errorf("Invalid cpuset value: %s", cpu)
			}

			high, err := strconv.Atoi(fields[1])
			if err != nil {
				return nil, fmt.Errorf("Invalid cpuset value: %s", cpu)
			}

			for i := low; i <= high; i++ {
				cpus = append(cpus, i)
			}
		} else {
			// Simple entry
			nr, err := strconv.Atoi(chunk)
			if err != nil {
				return nil, fmt.Errorf("Invalid cpuset value: %s", cpu)
			}
			cpus = append(cpus, nr)
		}
	}
	return cpus, nil
}

func deviceTaskBalance(s *state.State) {
	min := func(x, y int) int {
		if x < y {
			return x
		}
		return y
	}

	// Don't bother running when CGroup support isn't there
	if !s.OS.CGroupCPUsetController {
		return
	}

	// Get effective cpus list - those are all guaranteed to be online
	effectiveCpus, err := cGroupGet("cpuset", "/", "cpuset.effective_cpus")
	if err != nil {
		// Older kernel - use cpuset.cpus
		effectiveCpus, err = cGroupGet("cpuset", "/", "cpuset.cpus")
		if err != nil {
			logger.Errorf("Error reading host's cpuset.cpus")
			return
		}
	}

	effectiveCpusInt, err := parseCpuset(effectiveCpus)
	if err != nil {
		logger.Errorf("Error parsing effective CPU set")
		return
	}

	isolatedCpusInt := []int{}
	if shared.PathExists("/sys/devices/system/cpu/isolated") {
		buf, err := ioutil.ReadFile("/sys/devices/system/cpu/isolated")
		if err != nil {
			logger.Errorf("Error reading host's isolated cpu")
			return
		}

		// File might exist even though there are no isolated cpus.
		isolatedCpus := strings.TrimSpace(string(buf))
		if isolatedCpus != "" {
			isolatedCpusInt, err = parseCpuset(isolatedCpus)
			if err != nil {
				logger.Errorf("Error parsing isolated CPU set: %s", string(isolatedCpus))
				return
			}
		}
	}

	effectiveCpusSlice := []string{}
	for _, id := range effectiveCpusInt {
		if shared.IntInSlice(id, isolatedCpusInt) {
			continue
		}

		effectiveCpusSlice = append(effectiveCpusSlice, fmt.Sprintf("%d", id))
	}

	effectiveCpus = strings.Join(effectiveCpusSlice, ",")

	err = cGroupSet("cpuset", "/lxc", "cpuset.cpus", effectiveCpus)
	if err != nil && shared.PathExists("/sys/fs/cgroup/cpuset/lxc") {
		logger.Warn("Error setting lxd's cpuset.cpus", log.Ctx{"err": err})
	}
	cpus, err := parseCpuset(effectiveCpus)
	if err != nil {
		logger.Error("Error parsing host's cpu set", log.Ctx{"cpuset": effectiveCpus, "err": err})
		return
	}

	// Iterate through the containers
	containers, err := s.Cluster.ContainersList(db.CTypeRegular)
	if err != nil {
		logger.Error("problem loading containers list", log.Ctx{"err": err})
		return
	}
	fixedContainers := map[int][]container{}
	balancedContainers := map[container]int{}
	for _, name := range containers {
		c, err := containerLoadByName(s, name)
		if err != nil {
			continue
		}

		conf := c.ExpandedConfig()
		cpulimit, ok := conf["limits.cpu"]
		if !ok || cpulimit == "" {
			cpulimit = effectiveCpus
		}

		if !c.IsRunning() {
			continue
		}

		count, err := strconv.Atoi(cpulimit)
		if err == nil {
			// Load-balance
			count = min(count, len(cpus))
			balancedContainers[c] = count
		} else {
			// Pinned
			containerCpus, err := parseCpuset(cpulimit)
			if err != nil {
				return
			}
			for _, nr := range containerCpus {
				if !shared.IntInSlice(nr, cpus) {
					continue
				}

				_, ok := fixedContainers[nr]
				if ok {
					fixedContainers[nr] = append(fixedContainers[nr], c)
				} else {
					fixedContainers[nr] = []container{c}
				}
			}
		}
	}

	// Balance things
	pinning := map[container][]string{}
	usage := map[int]deviceTaskCPU{}

	for _, id := range cpus {
		cpu := deviceTaskCPU{}
		cpu.id = id
		cpu.strId = fmt.Sprintf("%d", id)
		count := 0
		cpu.count = &count

		usage[id] = cpu
	}

	for cpu, ctns := range fixedContainers {
		c, ok := usage[cpu]
		if !ok {
			logger.Errorf("Internal error: container using unavailable cpu")
			continue
		}
		id := c.strId
		for _, ctn := range ctns {
			_, ok := pinning[ctn]
			if ok {
				pinning[ctn] = append(pinning[ctn], id)
			} else {
				pinning[ctn] = []string{id}
			}
			*c.count += 1
		}
	}

	sortedUsage := make(deviceTaskCPUs, 0)
	for _, value := range usage {
		sortedUsage = append(sortedUsage, value)
	}

	for ctn, count := range balancedContainers {
		sort.Sort(sortedUsage)
		for _, cpu := range sortedUsage {
			if count == 0 {
				break
			}
			count -= 1

			id := cpu.strId
			_, ok := pinning[ctn]
			if ok {
				pinning[ctn] = append(pinning[ctn], id)
			} else {
				pinning[ctn] = []string{id}
			}
			*cpu.count += 1
		}
	}

	// Set the new pinning
	for ctn, set := range pinning {
		// Confirm the container didn't just stop
		if !ctn.IsRunning() {
			continue
		}

		sort.Strings(set)
		err := ctn.CGroupSet("cpuset.cpus", strings.Join(set, ","))
		if err != nil {
			logger.Error("balance: Unable to set cpuset", log.Ctx{"name": ctn.Name(), "err": err, "value": strings.Join(set, ",")})
		}
	}
}

func deviceNetworkPriority(s *state.State, netif string) {
	// Don't bother running when CGroup support isn't there
	if !s.OS.CGroupNetPrioController {
		return
	}

	containers, err := s.Cluster.ContainersList(db.CTypeRegular)
	if err != nil {
		return
	}

	for _, name := range containers {
		// Get the container struct
		c, err := containerLoadByName(s, name)
		if err != nil {
			continue
		}

		// Extract the current priority
		networkPriority := c.ExpandedConfig()["limits.network.priority"]
		if networkPriority == "" {
			continue
		}

		networkInt, err := strconv.Atoi(networkPriority)
		if err != nil {
			continue
		}

		// Set the value for the new interface
		c.CGroupSet("net_prio.ifpriomap", fmt.Sprintf("%s %d", netif, networkInt))
	}

	return
}

func deviceUSBEvent(s *state.State, usb usbDevice) {
	containers, err := s.Cluster.ContainersList(db.CTypeRegular)
	if err != nil {
		logger.Error("problem loading containers list", log.Ctx{"err": err})
		return
	}

	for _, name := range containers {
		containerIf, err := containerLoadByName(s, name)
		if err != nil {
			continue
		}

		c, ok := containerIf.(*containerLXC)
		if !ok {
			logger.Errorf("got device event on non-LXC container?")
			return
		}

		if !c.IsRunning() {
			continue
		}

		devices := c.ExpandedDevices()
		for _, name := range devices.DeviceNames() {
			m := devices[name]
			if m["type"] != "usb" {
				continue
			}

			if m["vendorid"] != usb.vendor || (m["productid"] != "" && m["productid"] != usb.product) {
				continue
			}

			if usb.action == "add" {
				err := c.insertUnixDeviceNum(fmt.Sprintf("unix.%s", name), m, usb.major, usb.minor, usb.path)
				if err != nil {
					logger.Error("failed to create usb device", log.Ctx{"err": err, "usb": usb, "container": c.Name()})
					return
				}
			} else if usb.action == "remove" {
				err := c.removeUnixDeviceNum(fmt.Sprintf("unix.%s", name), m, usb.major, usb.minor, usb.path)
				if err != nil {
					logger.Error("failed to remove usb device", log.Ctx{"err": err, "usb": usb, "container": c.Name()})
					return
				}
			} else {
				logger.Error("unknown action for usb device", log.Ctx{"usb": usb})
				continue
			}
		}
	}
}

func deviceEventListener(s *state.State) {
	chNetlinkCPU, chNetlinkNetwork, chUSB, err := deviceNetlinkListener()
	if err != nil {
		logger.Errorf("scheduler: couldn't setup netlink listener")
		return
	}

	for {
		select {
		case e := <-chNetlinkCPU:
			if len(e) != 2 {
				logger.Errorf("Scheduler: received an invalid cpu hotplug event")
				continue
			}

			if !s.OS.CGroupCPUsetController {
				continue
			}

			logger.Debugf("Scheduler: cpu: %s is now %s: re-balancing", e[0], e[1])
			deviceTaskBalance(s)
		case e := <-chNetlinkNetwork:
			if len(e) != 2 {
				logger.Errorf("Scheduler: received an invalid network hotplug event")
				continue
			}

			if !s.OS.CGroupNetPrioController {
				continue
			}

			logger.Debugf("Scheduler: network: %s has been added: updating network priorities", e[0])
			deviceNetworkPriority(s, e[0])
			networkAutoAttach(s.Cluster, e[0])
		case e := <-chUSB:
			deviceUSBEvent(s, e)
		case e := <-deviceSchedRebalance:
			if len(e) != 3 {
				logger.Errorf("Scheduler: received an invalid rebalance event")
				continue
			}

			if !s.OS.CGroupCPUsetController {
				continue
			}

			logger.Debugf("Scheduler: %s %s %s: re-balancing", e[0], e[1], e[2])
			deviceTaskBalance(s)
		}
	}
}

func deviceTaskSchedulerTrigger(srcType string, srcName string, srcStatus string) {
	// Spawn a go routine which then triggers the scheduler
	select {
	case deviceSchedRebalance <- []string{srcType, srcName, srcStatus}:
	default:
		// Channel is full, drop the event
	}
}

func deviceIsBlockdev(path string) bool {
	// Get a stat struct from the provided path
	stat := syscall.Stat_t{}
	err := syscall.Stat(path, &stat)
	if err != nil {
		return false
	}

	// Check if it's a block device
	if stat.Mode&syscall.S_IFMT == syscall.S_IFBLK {
		return true
	}

	// Not a device
	return false
}

func deviceModeOct(strmode string) (int, error) {
	// Default mode
	if strmode == "" {
		return 0600, nil
	}

	// Converted mode
	i, err := strconv.ParseInt(strmode, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("Bad device mode: %s", strmode)
	}

	return int(i), nil
}

func deviceGetAttributes(path string) (string, int, int, error) {
	// Get a stat struct from the provided path
	stat := syscall.Stat_t{}
	err := syscall.Stat(path, &stat)
	if err != nil {
		return "", 0, 0, err
	}

	// Check what kind of file it is
	dType := ""
	if stat.Mode&syscall.S_IFMT == syscall.S_IFBLK {
		dType = "b"
	} else if stat.Mode&syscall.S_IFMT == syscall.S_IFCHR {
		dType = "c"
	} else {
		return "", 0, 0, fmt.Errorf("Not a device")
	}

	// Return the device information
	major := shared.Major(stat.Rdev)
	minor := shared.Minor(stat.Rdev)
	return dType, major, minor, nil
}

func deviceNextInterfaceHWAddr() (string, error) {
	// Generate a new random MAC address using the usual prefix
	ret := bytes.Buffer{}
	for _, c := range "00:16:3e:xx:xx:xx" {
		if c == 'x' {
			c, err := rand.Int(rand.Reader, big.NewInt(16))
			if err != nil {
				return "", err
			}
			ret.WriteString(fmt.Sprintf("%x", c.Int64()))
		} else {
			ret.WriteString(string(c))
		}
	}

	return ret.String(), nil
}

func deviceNextVeth() string {
	// Return a new random veth device name
	randBytes := make([]byte, 4)
	rand.Read(randBytes)
	return "veth" + hex.EncodeToString(randBytes)
}

func deviceRemoveInterface(nic string) error {
	_, err := shared.RunCommand("ip", "link", "del", "dev", nic)
	return err
}

func deviceMountDisk(srcPath string, dstPath string, readonly bool, recursive bool) error {
	var err error

	// Prepare the mount flags
	flags := 0
	if readonly {
		flags |= syscall.MS_RDONLY
	}

	// Detect the filesystem
	fstype := "none"
	if deviceIsBlockdev(srcPath) {
		fstype, err = shared.BlockFsDetect(srcPath)
		if err != nil {
			return err
		}
	} else {
		flags |= syscall.MS_BIND
		if recursive {
			flags |= syscall.MS_REC
		}
	}

	// Mount the filesystem
	if err = syscall.Mount(srcPath, dstPath, fstype, uintptr(flags), ""); err != nil {
		return fmt.Errorf("Unable to mount %s at %s: %s", srcPath, dstPath, err)
	}

	// Remount bind mounts in readonly mode if requested
	if readonly == true && flags&syscall.MS_BIND == syscall.MS_BIND {
		flags = syscall.MS_RDONLY | syscall.MS_BIND | syscall.MS_REMOUNT
		if err = syscall.Mount("", dstPath, fstype, uintptr(flags), ""); err != nil {
			return fmt.Errorf("Unable to mount %s in readonly mode: %s", dstPath, err)
		}
	}

	flags = syscall.MS_REC | syscall.MS_SLAVE
	if err = syscall.Mount("", dstPath, "", uintptr(flags), ""); err != nil {
		return fmt.Errorf("unable to make mount %s private: %s", dstPath, err)
	}

	return nil
}

func deviceParseCPU(cpuAllowance string, cpuPriority string) (string, string, string, error) {
	var err error

	// Parse priority
	cpuShares := 0
	cpuPriorityInt := 10
	if cpuPriority != "" {
		cpuPriorityInt, err = strconv.Atoi(cpuPriority)
		if err != nil {
			return "", "", "", err
		}
	}
	cpuShares -= 10 - cpuPriorityInt

	// Parse allowance
	cpuCfsQuota := "-1"
	cpuCfsPeriod := "100000"

	if cpuAllowance != "" {
		if strings.HasSuffix(cpuAllowance, "%") {
			// Percentage based allocation
			percent, err := strconv.Atoi(strings.TrimSuffix(cpuAllowance, "%"))
			if err != nil {
				return "", "", "", err
			}

			cpuShares += (10 * percent) + 24
		} else {
			// Time based allocation
			fields := strings.SplitN(cpuAllowance, "/", 2)
			if len(fields) != 2 {
				return "", "", "", fmt.Errorf("Invalid allowance: %s", cpuAllowance)
			}

			quota, err := strconv.Atoi(strings.TrimSuffix(fields[0], "ms"))
			if err != nil {
				return "", "", "", err
			}

			period, err := strconv.Atoi(strings.TrimSuffix(fields[1], "ms"))
			if err != nil {
				return "", "", "", err
			}

			// Set limit in ms
			cpuCfsQuota = fmt.Sprintf("%d", quota*1000)
			cpuCfsPeriod = fmt.Sprintf("%d", period*1000)
			cpuShares += 1024
		}
	} else {
		// Default is 100%
		cpuShares += 1024
	}

	// Deal with a potential negative score
	if cpuShares < 0 {
		cpuShares = 0
	}

	return fmt.Sprintf("%d", cpuShares), cpuCfsQuota, cpuCfsPeriod, nil
}

func deviceGetParentBlocks(path string) ([]string, error) {
	var devices []string
	var device []string

	// Expand the mount path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	expPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		expPath = absPath
	}

	// Find the source mount of the path
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	match := ""
	for scanner.Scan() {
		line := scanner.Text()
		rows := strings.Fields(line)

		if len(rows[4]) <= len(match) {
			continue
		}

		if expPath != rows[4] && !strings.HasPrefix(expPath, rows[4]) {
			continue
		}

		match = rows[4]

		// Go backward to avoid problems with optional fields
		device = []string{rows[2], rows[len(rows)-2]}
	}

	if device == nil {
		return nil, fmt.Errorf("Couldn't find a match /proc/self/mountinfo entry")
	}

	// Handle the most simple case
	if !strings.HasPrefix(device[0], "0:") {
		return []string{device[0]}, nil
	}

	// Deal with per-filesystem oddities. We don't care about failures here
	// because any non-special filesystem => directory backend.
	fs, _ := util.FilesystemDetect(expPath)

	if fs == "zfs" && shared.PathExists("/dev/zfs") {
		// Accessible zfs filesystems
		poolName := strings.Split(device[1], "/")[0]

		output, err := shared.RunCommand("zpool", "status", "-P", "-L", poolName)
		if err != nil {
			return nil, fmt.Errorf("Failed to query zfs filesystem information for %s: %s", device[1], output)
		}

		header := true
		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}

			if fields[1] != "ONLINE" {
				continue
			}

			if header {
				header = false
				continue
			}

			var path string
			if shared.PathExists(fields[0]) {
				if shared.IsBlockdevPath(fields[0]) {
					path = fields[0]
				} else {
					subDevices, err := deviceGetParentBlocks(fields[0])
					if err != nil {
						return nil, err
					}

					for _, dev := range subDevices {
						devices = append(devices, dev)
					}
				}
			} else {
				continue
			}

			if path != "" {
				_, major, minor, err := deviceGetAttributes(path)
				if err != nil {
					continue
				}

				devices = append(devices, fmt.Sprintf("%d:%d", major, minor))
			}
		}

		if len(devices) == 0 {
			return nil, fmt.Errorf("Unable to find backing block for zfs pool: %s", poolName)
		}
	} else if fs == "btrfs" && shared.PathExists(device[1]) {
		// Accessible btrfs filesystems
		output, err := shared.RunCommand("btrfs", "filesystem", "show", device[1])
		if err != nil {
			return nil, fmt.Errorf("Failed to query btrfs filesystem information for %s: %s", device[1], output)
		}

		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || fields[0] != "devid" {
				continue
			}

			_, major, minor, err := deviceGetAttributes(fields[len(fields)-1])
			if err != nil {
				return nil, err
			}

			devices = append(devices, fmt.Sprintf("%d:%d", major, minor))
		}
	} else if shared.PathExists(device[1]) {
		// Anything else with a valid path
		_, major, minor, err := deviceGetAttributes(device[1])
		if err != nil {
			return nil, err
		}

		devices = append(devices, fmt.Sprintf("%d:%d", major, minor))
	} else {
		return nil, fmt.Errorf("Invalid block device: %s", device[1])
	}

	return devices, nil
}

func deviceParseDiskLimit(readSpeed string, writeSpeed string) (int64, int64, int64, int64, error) {
	parseValue := func(value string) (int64, int64, error) {
		var err error

		bps := int64(0)
		iops := int64(0)

		if readSpeed == "" {
			return bps, iops, nil
		}

		if strings.HasSuffix(value, "iops") {
			iops, err = strconv.ParseInt(strings.TrimSuffix(value, "iops"), 10, 64)
			if err != nil {
				return -1, -1, err
			}
		} else {
			bps, err = shared.ParseByteSizeString(value)
			if err != nil {
				return -1, -1, err
			}
		}

		return bps, iops, nil
	}

	readBps, readIops, err := parseValue(readSpeed)
	if err != nil {
		return -1, -1, -1, -1, err
	}

	writeBps, writeIops, err := parseValue(writeSpeed)
	if err != nil {
		return -1, -1, -1, -1, err
	}

	return readBps, readIops, writeBps, writeIops, nil
}

const USB_PATH = "/sys/bus/usb/devices"

func loadRawValues(p string) (map[string]string, error) {
	values := map[string]string{
		"idVendor":  "",
		"idProduct": "",
		"dev":       "",
		"busnum":    "",
		"devnum":    "",
	}

	for k := range values {
		v, err := ioutil.ReadFile(path.Join(p, k))
		if err != nil {
			return nil, err
		}

		values[k] = strings.TrimSpace(string(v))
	}

	return values, nil
}

func deviceLoadUsb() ([]usbDevice, error) {
	result := []usbDevice{}

	ents, err := ioutil.ReadDir(USB_PATH)
	if err != nil {
		/* if there are no USB devices, let's render an empty list,
		 * i.e. no usb devices */
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}

	for _, ent := range ents {
		values, err := loadRawValues(path.Join(USB_PATH, ent.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return []usbDevice{}, err
		}

		parts := strings.Split(values["dev"], ":")
		if len(parts) != 2 {
			return []usbDevice{}, fmt.Errorf("invalid device value %s", values["dev"])
		}

		usb, err := createUSBDevice(
			"add",
			values["idVendor"],
			values["idProduct"],
			parts[0],
			parts[1],
			values["busnum"],
			values["devnum"],
			values["devname"],
		)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		result = append(result, usb)
	}

	return result, nil
}

const SCIB string = "/sys/class/infiniband"
const SCNET string = "/sys/class/net"

type IBF struct {
	// port the function belongs to
	Port int64

	// name of the {physical,virtual} function
	Fun string

	// whether this is a physical (true) or virtual (false) function
	PF bool

	// device of the function
	Device string

	// uverb device of the function
	PerPortDevices []string
	PerFunDevices  []string
}

func deviceLoadInfiniband() (map[string]IBF, error) {
	// check if there are any infiniband devices
	fscib, err := os.Open(SCIB)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	defer fscib.Close()

	// eg.g. mlx_i for i = 0, 1, ..., n
	IBDevNames, err := fscib.Readdirnames(-1)
	if err != nil {
		return nil, err
	}

	if len(IBDevNames) == 0 {
		return nil, os.ErrNotExist
	}

	// retrieve all network device names
	fscnet, err := os.Open(SCNET)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	defer fscnet.Close()

	// retrieve all network devices
	NetDevNames, err := fscnet.Readdirnames(-1)
	if err != nil {
		return nil, err
	}

	if len(NetDevNames) == 0 {
		return nil, os.ErrNotExist
	}

	UseableDevices := make(map[string]IBF)
	for _, IBDevName := range IBDevNames {
		IBDevResourceFile := fmt.Sprintf("/sys/class/infiniband/%s/device/resource", IBDevName)
		IBDevResourceBuf, err := ioutil.ReadFile(IBDevResourceFile)
		if err != nil {
			return nil, err
		}

		for _, NetDevName := range NetDevNames {
			NetDevResourceFile := fmt.Sprintf("/sys/class/net/%s/device/resource", NetDevName)
			NetDevResourceBuf, err := ioutil.ReadFile(NetDevResourceFile)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}

			// If the device and the VF have the same address space
			// they belong together.
			if bytes.Compare(IBDevResourceBuf, NetDevResourceBuf) != 0 {
				continue
			}

			// Now let's find the ports.
			IBDevID := fmt.Sprintf("/sys/class/net/%s/dev_id", NetDevName)
			IBDevPort := fmt.Sprintf("/sys/class/net/%s/dev_port", NetDevName)
			DevIDBuf, err := ioutil.ReadFile(IBDevID)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}

			DevIDString := strings.TrimSpace(string(DevIDBuf))
			DevIDPort, err := strconv.ParseInt(DevIDString, 0, 64)
			if err != nil {
				return nil, err
			}

			DevPort := int64(0)
			DevPortBuf, err := ioutil.ReadFile(IBDevPort)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				DevPortString := strings.TrimSpace(string(DevPortBuf))
				DevPort, err = strconv.ParseInt(DevPortString, 0, 64)
				if err != nil {
					return nil, err
				}
			}

			Port := DevIDPort
			if DevPort > DevIDPort {
				Port = DevPort
			}
			Port++

			NewIBF := IBF{
				Port:   Port,
				Fun:    IBDevName,
				Device: NetDevName,
			}

			// identify the /dev/infiniband/uverb<idx> device
			tmp := []string{}
			IBUverb := fmt.Sprintf("/sys/class/net/%s/device/infiniband_verbs", NetDevName)
			fuverb, err := os.Open(IBUverb)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				defer fuverb.Close()

				// optional: retrieve all network devices
				tmp, err = fuverb.Readdirnames(-1)
				if err != nil {
					return nil, err
				}

				if len(tmp) == 0 {
					return nil, os.ErrNotExist
				}
			}
			for _, v := range tmp {
				if strings.Index(v, "-") != -1 {
					return nil, fmt.Errorf("Infiniband character device \"%s\" contains \"-\". Cannot guarantee unique encoding", v)
				}
				NewIBF.PerPortDevices = append(NewIBF.PerPortDevices, v)
			}

			// identify the /dev/infiniband/ucm<idx> device
			tmp = []string{}
			IBcm := fmt.Sprintf("/sys/class/net/%s/device/infiniband_ucm", NetDevName)
			fcm, err := os.Open(IBcm)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				defer fcm.Close()

				// optional: retrieve all network devices
				tmp, err = fcm.Readdirnames(-1)
				if err != nil {
					return nil, err
				}

				if len(tmp) == 0 {
					return nil, os.ErrNotExist
				}
			}
			for _, v := range tmp {
				if strings.Index(v, "-") != -1 {
					return nil, fmt.Errorf("Infiniband character device \"%s\" contains \"-\". Cannot guarantee unique encoding", v)
				}
				devPath := fmt.Sprintf("/dev/infiniband/%s", v)
				NewIBF.PerPortDevices = append(NewIBF.PerPortDevices, devPath)
			}

			// identify the /dev/infiniband/{issm,umad}<idx> devices
			IBmad := fmt.Sprintf("/sys/class/net/%s/device/infiniband_mad", NetDevName)
			ents, err := ioutil.ReadDir(IBmad)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				for _, ent := range ents {
					IBmadPort := fmt.Sprintf("%s/%s/port", IBmad, ent.Name())
					portBuf, err := ioutil.ReadFile(IBmadPort)
					if err != nil {
						if !os.IsNotExist(err) {
							return nil, err
						}
						continue
					}

					portStr := strings.TrimSpace(string(portBuf))
					PortMad, err := strconv.ParseInt(portStr, 0, 64)
					if err != nil {
						return nil, err
					}

					if PortMad != NewIBF.Port {
						continue
					}

					if strings.Index(ent.Name(), "-") != -1 {
						return nil, fmt.Errorf("Infiniband character device \"%s\" contains \"-\". Cannot guarantee unique encoding", ent.Name())
					}

					NewIBF.PerFunDevices = append(NewIBF.PerFunDevices, ent.Name())
				}
			}

			// figure out whether this is a physical function
			IBPF := fmt.Sprintf("/sys/class/net/%s/device/physfn", NetDevName)
			NewIBF.PF = !shared.PathExists(IBPF)

			UseableDevices[NetDevName] = NewIBF
		}
	}

	// check whether the device is an infiniband device
	return UseableDevices, nil
}
