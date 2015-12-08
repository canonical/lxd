package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"syscall"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
)

var deviceSchedRebalance = make(chan []string, 0)

type deviceTaskCPU struct {
	id    int
	strId string
	count *int
}
type deviceTaskCPUs []deviceTaskCPU

func (c deviceTaskCPUs) Len() int           { return len(c) }
func (c deviceTaskCPUs) Less(i, j int) bool { return *c[i].count < *c[j].count }
func (c deviceTaskCPUs) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }

func deviceMonitorProcessors() (chan []string, error) {
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

			if props["SUBSYSTEM"] != "cpu" || props["DRIVER"] != "processor" {
				continue
			}

			if props["ACTION"] != "offline" && props["ACTION"] != "online" {
				continue
			}

			ch <- []string{path.Base(props["DEVPATH"]), props["ACTION"]}
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

	// Count CPUs
	cpus := []int{}
	dents, err := ioutil.ReadDir("/sys/bus/cpu/devices/")
	if err != nil {
		shared.Log("error", "balance: Unable to list CPUs", shared.Ctx{"err": err})
		return
	}

	for _, f := range dents {
		id := -1
		count, err := fmt.Sscanf(f.Name(), "cpu%d", &id)
		if count != 1 || id == -1 {
			shared.Log("error", "balance: Bad CPU", shared.Ctx{"path": f.Name()})
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
			shared.Log("error", "balance: Bad CPU", shared.Ctx{"path": f.Name(), "err": err})
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
			cpu = fmt.Sprintf("%d", len(cpus))
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
						shared.Log("error", "Invalid limits.cpu value.", shared.Ctx{"container": c.Name(), "value": cpu})
						continue
					}

					low, err := strconv.Atoi(fields[0])
					if err != nil {
						shared.Log("error", "Invalid limits.cpu value.", shared.Ctx{"container": c.Name(), "value": cpu})
						continue
					}

					high, err := strconv.Atoi(fields[1])
					if err != nil {
						shared.Log("error", "Invalid limits.cpu value.", shared.Ctx{"container": c.Name(), "value": cpu})
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
						shared.Log("error", "Invalid limits.cpu value.", shared.Ctx{"container": c.Name(), "value": cpu})
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
		sort.Strings(set)
		err := ctn.CGroupSet("cpuset.cpus", strings.Join(set, ","))
		if err != nil {
			shared.Log("error", "balance: Unable to set cpuset", shared.Ctx{"name": ctn.Name(), "err": err, "value": strings.Join(set, ",")})
		}
	}
}

func deviceTaskScheduler(d *Daemon) {
	chHotplug, err := deviceMonitorProcessors()
	if err != nil {
		shared.Log("error", "scheduler: couldn't setup uevent watcher, no automatic re-balance")
		return
	}

	shared.Debugf("Scheduler: doing initial balance")
	deviceTaskBalance(d)

	for {
		select {
		case e := <-chHotplug:
			if len(e) != 2 {
				shared.Log("error", "Scheduler: received an invalid hotplug event")
				continue
			}
			shared.Debugf("Scheduler: %s is now %s: re-balancing", e[0], e[1])
			deviceTaskBalance(d)
		case e := <-deviceSchedRebalance:
			if len(e) != 3 {
				shared.Log("error", "Scheduler: received an invalid rebalance event")
				continue
			}
			shared.Debugf("Scheduler: %s %s %s: re-balancing", e[0], e[1], e[2])
			deviceTaskBalance(d)
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
