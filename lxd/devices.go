package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	// Used by cgo
	_ "github.com/lxc/lxd/lxd/include"

	"github.com/lxc/lxd/lxd/cgroup"
	"github.com/lxc/lxd/lxd/device"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <stdio.h>
#include <linux/hidraw.h>

#include "include/memory_utils.h"

#ifndef HIDIOCGRAWINFO
#define HIDIOCGRAWINFO _IOR('H', 0x03, struct hidraw_devinfo)
struct hidraw_devinfo {
	__u32 bustype;
	__s16 vendor;
	__s16 product;
};
#endif

static int get_hidraw_devinfo(int fd, struct hidraw_devinfo *info)
{
	int ret;

	ret = ioctl(fd, HIDIOCGRAWINFO, info);
	if (ret)
		return -1;

	return 0;
}

*/
import "C"

type deviceTaskCPU struct {
	id    int64
	strId string
	count *int
}
type deviceTaskCPUs []deviceTaskCPU

func (c deviceTaskCPUs) Len() int           { return len(c) }
func (c deviceTaskCPUs) Less(i, j int) bool { return *c[i].count < *c[j].count }
func (c deviceTaskCPUs) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }

func deviceNetlinkListener() (chan []string, chan []string, chan device.USBEvent, chan device.UnixHotplugEvent, error) {
	NETLINK_KOBJECT_UEVENT := 15
	UEVENT_BUFFER_SIZE := 2048

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

	chCPU := make(chan []string, 1)
	chNetwork := make(chan []string)
	chUSB := make(chan device.USBEvent)
	chUnix := make(chan device.UnixHotplugEvent)

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
					ueventParts = append(ueventParts[:i], ueventParts[i+1:]...)
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

				fields := strings.SplitN(part, "=", 2)
				if len(fields) != 2 {
					continue
				}

				props[fields[0]] = fields[1]
			}

			ueventLen--

			if udevEvent {
				// The kernel always prepends this and udev expects it.
				kernelPrefix := fmt.Sprintf("%s@%s", props["ACTION"], props["DEVPATH"])
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

				if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", props["INTERFACE"])) {
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

func deviceTaskBalance(s *state.State) {
	min := func(x, y int) int {
		if x < y {
			return x
		}
		return y
	}

	// Don't bother running when CGroup support isn't there
	if !s.OS.CGInfo.Supports(cgroup.CPUSet, nil) {
		return
	}

	// Get effective cpus list - those are all guaranteed to be online
	cg, err := cgroup.NewFileReadWriter(1, true)
	if err != nil {
		logger.Errorf("Unable to load cgroup writer: %v", err)
		return
	}

	effectiveCpus, err := cg.GetEffectiveCpuset()
	if err != nil {
		// Older kernel - use cpuset.cpus
		effectiveCpus, err = cg.GetCpuset()
		if err != nil {
			logger.Errorf("Error reading host's cpuset.cpus")
			return
		}
	}

	effectiveCpusInt, err := resources.ParseCpuset(effectiveCpus)
	if err != nil {
		logger.Errorf("Error parsing effective CPU set")
		return
	}

	isolatedCpusInt := resources.GetCPUIsolated()
	effectiveCpusSlice := []string{}
	for _, id := range effectiveCpusInt {
		if shared.Int64InSlice(id, isolatedCpusInt) {
			continue
		}

		effectiveCpusSlice = append(effectiveCpusSlice, fmt.Sprintf("%d", id))
	}

	effectiveCpus = strings.Join(effectiveCpusSlice, ",")
	cpus, err := resources.ParseCpuset(effectiveCpus)
	if err != nil {
		logger.Error("Error parsing host's cpu set", logger.Ctx{"cpuset": effectiveCpus, "err": err})
		return
	}

	// Iterate through the instances
	instances, err := instance.LoadNodeAll(s, instancetype.Container)
	if err != nil {
		logger.Error("Problem loading instances list", logger.Ctx{"err": err})
		return
	}

	fixedInstances := map[int64][]instance.Instance{}
	balancedInstances := map[instance.Instance]int{}
	for _, c := range instances {
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
			balancedInstances[c] = count
		} else {
			// Pinned
			containerCpus, err := resources.ParseCpuset(cpulimit)
			if err != nil {
				return
			}
			for _, nr := range containerCpus {
				if !shared.Int64InSlice(nr, cpus) {
					continue
				}

				_, ok := fixedInstances[nr]
				if ok {
					fixedInstances[nr] = append(fixedInstances[nr], c)
				} else {
					fixedInstances[nr] = []instance.Instance{c}
				}
			}
		}
	}

	// Balance things
	pinning := map[instance.Instance][]string{}
	usage := map[int64]deviceTaskCPU{}

	for _, id := range cpus {
		cpu := deviceTaskCPU{}
		cpu.id = id
		cpu.strId = fmt.Sprintf("%d", id)
		count := 0
		cpu.count = &count

		usage[id] = cpu
	}

	for cpu, ctns := range fixedInstances {
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

	for ctn, count := range balancedInstances {
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
		cg, err := ctn.CGroup()
		if err != nil {
			logger.Error("balance: Unable to get cgroup struct", logger.Ctx{"name": ctn.Name(), "err": err, "value": strings.Join(set, ",")})
			continue
		}

		err = cg.SetCpuset(strings.Join(set, ","))
		if err != nil {
			logger.Error("balance: Unable to set cpuset", logger.Ctx{"name": ctn.Name(), "err": err, "value": strings.Join(set, ",")})
		}
	}
}

func deviceNetworkPriority(s *state.State, netif string) {
	// Don't bother running when CGroup support isn't there
	if !s.OS.CGInfo.Supports(cgroup.NetPrio, nil) {
		return
	}

	instances, err := instance.LoadNodeAll(s, instancetype.Container)
	if err != nil {
		return
	}

	for _, c := range instances {
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
		cg, err := c.CGroup()
		if err != nil {
			continue
		}

		_ = cg.SetNetIfPrio(fmt.Sprintf("%s %d", netif, networkInt))
	}
}

func deviceEventListener(s *state.State) {
	chNetlinkCPU, chNetlinkNetwork, chUSB, chUnix, err := deviceNetlinkListener()
	if err != nil {
		logger.Errorf("scheduler: Couldn't setup netlink listener: %v", err)
		return
	}

	for {
		select {
		case e := <-chNetlinkCPU:
			if len(e) != 2 {
				logger.Errorf("Scheduler: received an invalid cpu hotplug event")
				continue
			}

			if !s.OS.CGInfo.Supports(cgroup.CPUSet, nil) {
				continue
			}

			logger.Debugf("Scheduler: cpu: %s is now %s: re-balancing", e[0], e[1])
			deviceTaskBalance(s)
		case e := <-chNetlinkNetwork:
			if len(e) != 2 {
				logger.Errorf("Scheduler: received an invalid network hotplug event")
				continue
			}

			if !s.OS.CGInfo.Supports(cgroup.NetPrio, nil) {
				continue
			}

			logger.Debugf("Scheduler: network: %s has been added: updating network priorities", e[0])
			deviceNetworkPriority(s, e[0])
			err = networkAutoAttach(s.DB.Cluster, e[0])
			if err != nil {
				logger.Warn("Failed to auto-attach network", logger.Ctx{"err": err})
			}
		case e := <-chUSB:
			device.USBRunHandlers(s, &e)
		case e := <-chUnix:
			device.UnixHotplugRunHandlers(s, &e)
		case e := <-cgroup.DeviceSchedRebalance:
			if len(e) != 3 {
				logger.Errorf("Scheduler: received an invalid rebalance event")
				continue
			}

			if !s.OS.CGInfo.Supports(cgroup.CPUSet, nil) {
				continue
			}

			logger.Debugf("Scheduler: %s %s %s: re-balancing", e[0], e[1], e[2])
			deviceTaskBalance(s)
		}
	}
}

// devicesRegister calls the Register() function on all supported devices so they receive events.
// This also has the effect of actively reconnecting to any running VM monitor sockets.
func devicesRegister(s *state.State) {
	logger.Debug("Registering running instances")

	instances, err := instance.LoadNodeAll(s, instancetype.Any)
	if err != nil {
		logger.Error("Problem loading instances list", logger.Ctx{"err": err})
		return
	}

	for _, inst := range instances {
		if !inst.IsRunning() {
			continue
		}

		inst.RegisterDevices()
	}
}

func getHidrawDevInfo(fd int) (string, string, error) {
	info := C.struct_hidraw_devinfo{}
	ret, err := C.get_hidraw_devinfo(C.int(fd), &info)
	if ret != 0 {
		return "", "", err
	}

	return fmt.Sprintf("%04x", info.vendor), fmt.Sprintf("%04x", info.product), nil
}

func ueventParseVendorProduct(props map[string]string, subsystem string, devname string) (string, string, bool) {
	vendor, vendorOk := props["ID_VENDOR_ID"]
	product, productOk := props["ID_MODEL_ID"]

	if vendorOk && productOk {
		return vendor, product, true
	}

	if subsystem != "hidraw" {
		return "", "", false
	}

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
		logger.Debugf("Failed to retrieve device info from hidraw device \"%s\"", devname)
		return "", "", false
	}

	return vendor, product, true
}
