package util

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

type thread struct {
	ID             uint64
	vendor         string
	name           string
	coreID         uint64
	socketID       uint64
	frequency      uint64
	frequencyTurbo uint64
}

func parseCpuinfo() ([]thread, error) {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	threads := []thread{}
	scanner := bufio.NewScanner(f)
	var t *thread
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "processor") {
			i := strings.Index(line, ":")
			if i < 0 {
				return nil, err
			}
			i++

			line = line[i:]
			line = strings.TrimSpace(line)

			id, err := strconv.Atoi(line)
			if err != nil {
				return nil, err
			}

			t = &thread{}
			t.ID = uint64(id)

			path := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/topology/core_id", t.ID)
			coreID, err := shared.ParseNumberFromFile(path)
			if err != nil {
				return nil, err
			}

			t.coreID = uint64(coreID)

			path = fmt.Sprintf("/sys/devices/system/cpu/cpu%d/topology/physical_package_id", t.ID)
			sockID, err := shared.ParseNumberFromFile(path)
			if err != nil {
				return nil, err
			}

			t.socketID = uint64(sockID)

			path = fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/scaling_cur_freq", t.ID)
			freq, err := shared.ParseNumberFromFile(path)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				t.frequency = uint64(freq / 1000)
			}

			path = fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/cpuinfo_max_freq", t.ID)
			freq, err = shared.ParseNumberFromFile(path)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				t.frequencyTurbo = uint64(freq / 1000)
			}

			threads = append(threads, *t)
		} else if strings.HasPrefix(line, "vendor_id") {
			i := strings.Index(line, ":")
			if i < 0 {
				return nil, err
			}
			i++

			line = line[i:]
			line = strings.TrimSpace(line)

			if t != nil {
				threads[len(threads)-1].name = line
			}
		} else if strings.HasPrefix(line, "model name") {
			i := strings.Index(line, ":")
			if i < 0 {
				return nil, err
			}
			i++

			line = line[i:]
			line = strings.TrimSpace(line)

			if t != nil {
				threads[len(threads)-1].vendor = line
			}
		} else if t != nil && t.frequency == 0 && strings.HasPrefix(line, "cpu MHz") {
			i := strings.Index(line, ":")
			if i < 0 {
				return nil, err
			}
			i++

			line = line[i:]
			line = strings.TrimSpace(line)

			if t != nil {
				freqFloat, err := strconv.ParseFloat(line, 64)
				if err != nil {
					return nil, err
				}

				threads[len(threads)-1].frequency = uint64(freqFloat)
			}
		}
	}

	if len(threads) == 0 {
		return nil, os.ErrNotExist
	}

	return threads, err
}

func parseSysDevSystemCPU() ([]thread, error) {
	ents, err := ioutil.ReadDir("/sys/devices/system/cpu/")
	if err != nil {
		return nil, err
	}

	threads := []thread{}
	for _, ent := range ents {
		entName := ent.Name()
		if !strings.HasPrefix(entName, "cpu") {
			continue
		}

		entName = entName[len("cpu"):]
		idx, err := strconv.Atoi(entName)
		if err != nil {
			continue
		}

		t := thread{}
		t.ID = uint64(idx)
		path := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/topology/core_id", t.ID)
		coreID, err := shared.ParseNumberFromFile(path)
		if err != nil {
			return nil, err
		}

		t.coreID = uint64(coreID)

		path = fmt.Sprintf("/sys/devices/system/cpu/cpu%d/topology/physical_package_id", t.ID)
		sockID, err := shared.ParseNumberFromFile(path)
		if err != nil {
			return nil, err
		}

		t.socketID = uint64(sockID)

		path = fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/scaling_cur_freq", t.ID)
		freq, err := shared.ParseNumberFromFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
		} else {
			t.frequency = uint64(freq / 1000)
		}

		path = fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/cpuinfo_max_freq", t.ID)
		freq, err = shared.ParseNumberFromFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
		} else {
			t.frequencyTurbo = uint64(freq / 1000)
		}

		threads = append(threads, t)
	}

	if len(threads) == 0 {
		return nil, os.ErrNotExist
	}

	return threads, err
}

func getThreads() ([]thread, error) {
	threads, err := parseCpuinfo()
	if err == nil {
		return threads, nil
	}

	threads, err = parseSysDevSystemCPU()
	if err != nil {
		return nil, err
	}

	return threads, nil
}

// CPUResource returns the system CPU information
func CPUResource() (*api.ResourcesCPU, error) {
	c := api.ResourcesCPU{}

	threads, err := getThreads()
	if err != nil {
		return nil, err
	}

	var cur *api.ResourcesCPUSocket
	c.Total = uint64(len(threads))
	c.Sockets = append(c.Sockets, api.ResourcesCPUSocket{})
	for _, v := range threads {
		if uint64(len(c.Sockets)) <= v.socketID {
			c.Sockets = append(c.Sockets, api.ResourcesCPUSocket{})
			cur = &c.Sockets[v.socketID]
		} else {
			cur = &c.Sockets[v.socketID]
		}

		if v.coreID+1 > cur.Cores {
			cur.Cores++
		}

		cur.Threads++
		cur.Name = v.name
		cur.Vendor = v.vendor
		cur.Frequency = v.frequency
		cur.FrequencyTurbo = v.frequencyTurbo
	}

	return &c, nil
}

// MemoryResource returns the system memory information
func MemoryResource() (*api.ResourcesMemory, error) {
	var buffers uint64
	var cached uint64
	var free uint64
	var total uint64

	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cleanLine := func(l string) (string, error) {
		l = strings.TrimSpace(l)
		idx := strings.LastIndex(l, "kB")
		if idx < 0 {
			return "", fmt.Errorf(`Failed to detect "kB" suffix`)
		}

		return strings.TrimSpace(l[:idx]), nil
	}

	mem := api.ResourcesMemory{}
	scanner := bufio.NewScanner(f)
	found := 0
	for scanner.Scan() {
		var err error
		line := scanner.Text()

		if strings.HasPrefix(line, "MemTotal:") {
			line, err = cleanLine(line[len("MemTotal:"):])
			if err != nil {
				return nil, err
			}

			total, err = strconv.ParseUint(line, 10, 64)
			if err != nil {
				return nil, err
			}

			found++
		} else if strings.HasPrefix(line, "MemFree:") {
			line, err = cleanLine(line[len("MemFree:"):])
			if err != nil {
				return nil, err
			}

			free, err = strconv.ParseUint(line, 10, 64)
			if err != nil {
				return nil, err
			}

			found++
		} else if strings.HasPrefix(line, "Cached:") {
			line, err = cleanLine(line[len("Cached:"):])
			if err != nil {
				return nil, err
			}

			cached, err = strconv.ParseUint(line, 10, 64)
			if err != nil {
				return nil, err
			}

			found++
		} else if strings.HasPrefix(line, "Buffers:") {
			line, err = cleanLine(line[len("Buffers:"):])
			if err != nil {
				return nil, err
			}

			buffers, err = strconv.ParseUint(line, 10, 64)
			if err != nil {
				return nil, err
			}

			found++
		}

		if found == 4 {
			break
		}
	}

	mem.Total = total * 1024
	mem.Used = (total - free - cached - buffers) * 1024

	return &mem, err
}
