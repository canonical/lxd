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
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

var deviceSchedRebalance = make(chan []string, 0)

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

func deviceNetlinkListener() (chan []string, error) {
	NETLINK_KOBJECT_UEVENT := 15
	UEVENT_BUFFER_SIZE := 2048

	fd, err := syscall.Socket(
		syscall.AF_NETLINK, syscall.SOCK_RAW,
		NETLINK_KOBJECT_UEVENT,
	)

	if err != nil {
		return nil, err
	}

	nl := syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Pid:    uint32(os.Getpid()),
		Groups: 1,
	}

	err = syscall.Bind(fd, &nl)
	if err != nil {
		return nil, err
	}

	ch := make(chan []string, 0)

	go func(ch chan []string) {
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

				ch <- []string{"cpu", path.Base(props["DEVPATH"]), props["ACTION"]}
			}

			if props["SUBSYSTEM"] == "net" {
				if props["ACTION"] != "add" && props["ACTION"] != "removed" {
					continue
				}

				ch <- []string{"net", props["INTERFACE"], props["ACTION"]}
			}
		}
	}(ch)

	return ch, nil
}

func deviceTaskBalance(d *Daemon) {
	min := func(x, y int) int {
		if x < y {
			return x
		}
		return y
	}

	// Don't bother running when CGroup support isn't there
	if !cgCpusetController {
		return
	}

	// Count CPUs
	cpus := []int{}
	dents, err := ioutil.ReadDir("/sys/bus/cpu/devices/")
	if err != nil {
		shared.Log.Error("balance: Unable to list CPUs", log.Ctx{"err": err})
		return
	}

	for _, f := range dents {
		id := -1
		count, err := fmt.Sscanf(f.Name(), "cpu%d", &id)
		if count != 1 || id == -1 {
			shared.Log.Error("balance: Bad CPU", log.Ctx{"path": f.Name()})
			continue
		}

		onlinePath := fmt.Sprintf("/sys/bus/cpu/devices/%s/online", f.Name())
		if !shared.PathExists(onlinePath) {
			// CPUs without an online file are non-hotplug so are always online
			cpus = append(cpus, id)
			continue
		}

		online, err := ioutil.ReadFile(onlinePath)
		if err != nil {
			shared.Log.Error("balance: Bad CPU", log.Ctx{"path": f.Name(), "err": err})
			continue
		}

		if online[0] == byte('0') {
			continue
		}

		cpus = append(cpus, id)
	}

	// Iterate through the containers
	containers, err := dbContainersList(d.db, cTypeRegular)
	fixedContainers := map[int][]container{}
	balancedContainers := map[container]int{}
	for _, name := range containers {
		c, err := containerLoadByName(d, name)
		if err != nil {
			continue
		}

		conf := c.ExpandedConfig()
		cpu, ok := conf["limits.cpu"]
		if !ok || cpu == "" {
			currentCPUs, err := deviceGetCurrentCPUs()
			if err != nil {
				shared.Debugf("Couldn't get current CPU list: %s", err)
				cpu = fmt.Sprintf("%d", len(cpus))
			} else {
				cpu = currentCPUs
			}
		}

		if !c.IsRunning() {
			continue
		}

		count, err := strconv.Atoi(cpu)
		if err == nil {
			// Load-balance
			count = min(count, len(cpus))
			balancedContainers[c] = count
		} else {
			// Pinned
			chunks := strings.Split(cpu, ",")
			for _, chunk := range chunks {
				if strings.Contains(chunk, "-") {
					// Range
					fields := strings.SplitN(chunk, "-", 2)
					if len(fields) != 2 {
						shared.Log.Error("Invalid limits.cpu value.", log.Ctx{"container": c.Name(), "value": cpu})
						continue
					}

					low, err := strconv.Atoi(fields[0])
					if err != nil {
						shared.Log.Error("Invalid limits.cpu value.", log.Ctx{"container": c.Name(), "value": cpu})
						continue
					}

					high, err := strconv.Atoi(fields[1])
					if err != nil {
						shared.Log.Error("Invalid limits.cpu value.", log.Ctx{"container": c.Name(), "value": cpu})
						continue
					}

					for i := low; i <= high; i++ {
						if !shared.IntInSlice(i, cpus) {
							continue
						}

						_, ok := fixedContainers[i]
						if ok {
							fixedContainers[i] = append(fixedContainers[i], c)
						} else {
							fixedContainers[i] = []container{c}
						}
					}
				} else {
					// Simple entry
					nr, err := strconv.Atoi(chunk)
					if err != nil {
						shared.Log.Error("Invalid limits.cpu value.", log.Ctx{"container": c.Name(), "value": cpu})
						continue
					}

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
	}

	// Balance things
	pinning := map[container][]string{}
	usage := make(deviceTaskCPUs, 0)

	for _, id := range cpus {
		cpu := deviceTaskCPU{}
		cpu.id = id
		cpu.strId = fmt.Sprintf("%d", id)
		count := 0
		cpu.count = &count

		usage = append(usage, cpu)
	}

	for cpu, ctns := range fixedContainers {
		id := usage[cpu].strId
		for _, ctn := range ctns {
			_, ok := pinning[ctn]
			if ok {
				pinning[ctn] = append(pinning[ctn], id)
			} else {
				pinning[ctn] = []string{id}
			}
			*usage[cpu].count += 1
		}
	}

	for ctn, count := range balancedContainers {
		sort.Sort(usage)
		for _, cpu := range usage {
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
			shared.Log.Error("balance: Unable to set cpuset", log.Ctx{"name": ctn.Name(), "err": err, "value": strings.Join(set, ",")})
		}
	}
}

func deviceGetCurrentCPUs() (string, error) {
	// Open /proc/self/status
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Read it line by line
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()

		// We only care about MemTotal
		if !strings.HasPrefix(line, "Cpus_allowed_list:") {
			continue
		}

		// Extract the before last (value) and last (unit) fields
		fields := strings.Split(line, "\t")
		value := fields[len(fields)-1]

		return value, nil
	}

	return "", fmt.Errorf("Couldn't find cpus_allowed_list")
}

func deviceNetworkPriority(d *Daemon, netif string) {
	// Don't bother running when CGroup support isn't there
	if !cgNetPrioController {
		return
	}

	containers, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return
	}

	for _, name := range containers {
		// Get the container struct
		c, err := containerLoadByName(d, name)
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

func deviceEventListener(d *Daemon) {
	chNetlink, err := deviceNetlinkListener()
	if err != nil {
		shared.Log.Error("scheduler: couldn't setup netlink listener")
		return
	}

	for {
		select {
		case e := <-chNetlink:
			if len(e) != 3 {
				shared.Log.Error("Scheduler: received an invalid hotplug event")
				continue
			}

			if e[0] == "cpu" && cgCpusetController {
				shared.Debugf("Scheduler: %s: %s is now %s: re-balancing", e[0], e[1], e[2])
				deviceTaskBalance(d)
			}

			if e[0] == "net" && e[2] == "add" && cgNetPrioController && shared.PathExists(fmt.Sprintf("/sys/class/net/%s", e[1])) {
				shared.Debugf("Scheduler: %s: %s has been added: updating network priorities", e[0], e[1])
				deviceNetworkPriority(d, e[1])
			}
		case e := <-deviceSchedRebalance:
			if len(e) != 3 {
				shared.Log.Error("Scheduler: received an invalid rebalance event")
				continue
			}

			if cgCpusetController {
				shared.Debugf("Scheduler: %s %s %s: re-balancing", e[0], e[1], e[2])
				deviceTaskBalance(d)
			}
		}
	}
}

func deviceTaskSchedulerTrigger(srcType string, srcName string, srcStatus string) {
	// Spawn a go routine which then triggers the scheduler
	go func() {
		deviceSchedRebalance <- []string{srcType, srcName, srcStatus}
	}()
}

func deviceIsDevice(path string) bool {
	// Get a stat struct from the provided path
	stat := syscall.Stat_t{}
	err := syscall.Stat(path, &stat)
	if err != nil {
		return false
	}

	// Check if it's a character device
	if stat.Mode&syscall.S_IFMT == syscall.S_IFCHR {
		return true
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
	major := int(stat.Rdev / 256)
	minor := int(stat.Rdev % 256)
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
	return exec.Command("ip", "link", "del", nic).Run()
}

func deviceMountDisk(srcPath string, dstPath string, readonly bool) error {
	var err error

	// Prepare the mount flags
	flags := 0
	if readonly {
		flags |= syscall.MS_RDONLY
	}

	// Detect the filesystem
	fstype := "none"
	if deviceIsDevice(srcPath) {
		fstype, err = shared.BlockFsDetect(srcPath)
		if err != nil {
			return err
		}
	} else {
		flags |= syscall.MS_BIND
	}

	// Mount the filesystem
	if err = syscall.Mount(srcPath, dstPath, fstype, uintptr(flags), ""); err != nil {
		return fmt.Errorf("Unable to mount %s at %s: %s", srcPath, dstPath, err)
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

func deviceTotalMemory() (int64, error) {
	// Open /proc/meminfo
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return -1, err
	}
	defer f.Close()

	// Read it line by line
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()

		// We only care about MemTotal
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}

		// Extract the before last (value) and last (unit) fields
		fields := strings.Split(line, " ")
		value := fields[len(fields)-2] + fields[len(fields)-1]

		// Feed the result to shared.ParseByteSizeString to get an int value
		valueBytes, err := shared.ParseByteSizeString(value)
		if err != nil {
			return -1, err
		}

		return valueBytes, nil
	}

	return -1, fmt.Errorf("Couldn't find MemTotal")
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
	fs, _ := filesystemDetect(expPath)

	if fs == "zfs" && shared.PathExists("/dev/zfs") {
		// Accessible zfs filesystems
		poolName := strings.Split(device[1], "/")[0]

		output, err := exec.Command("zpool", "status", poolName).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("Failed to query zfs filesystem information for %s: %s", device[1], output)
		}

		for _, line := range strings.Split(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}

			if fields[1] != "ONLINE" {
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
			} else if shared.PathExists(fmt.Sprintf("/dev/%s", fields[0])) {
				path = fmt.Sprintf("/dev/%s", fields[0])
			} else if shared.PathExists(fmt.Sprintf("/dev/disk/by-id/%s", fields[0])) {
				path = fmt.Sprintf("/dev/disk/by-id/%s", fields[0])
			} else {
				return nil, fmt.Errorf("Unsupported zfs backing device: %s", fields[0])
			}

			if path != "" {
				_, major, minor, err := deviceGetAttributes(fields[len(fields)-1])
				if err != nil {
					return nil, err
				}

				devices = append(devices, fmt.Sprintf("%d:%d", major, minor))
			}
		}
	} else if fs == "btrfs" && shared.PathExists(device[1]) {
		// Accessible btrfs filesystems
		output, err := exec.Command("btrfs", "filesystem", "show", device[1]).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("Failed to query btrfs filesystem information for %s: %s", device[1], output)
		}

		for _, line := range strings.Split(string(output), "\n") {
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
