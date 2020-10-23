package dnsmasq

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/subprocess"
	"github.com/lxc/lxd/shared/version"
)

// DHCPAllocation represents an IP allocation from dnsmasq.
type DHCPAllocation struct {
	IP     net.IP
	Name   string
	MAC    net.HardwareAddr
	Static bool
}

// ConfigMutex used to coordinate access to the dnsmasq config files.
var ConfigMutex sync.Mutex

// UpdateStaticEntry writes a single dhcp-host line for a network/instance combination.
func UpdateStaticEntry(network string, projectName string, instanceName string, netConfig map[string]string, hwaddr string, ipv4Address string, ipv6Address string) error {
	hwaddr = strings.ToLower(hwaddr)
	line := hwaddr

	// Generate the dhcp-host line
	if ipv4Address != "" {
		line += fmt.Sprintf(",%s", ipv4Address)
	}

	if ipv6Address != "" {
		line += fmt.Sprintf(",[%s]", ipv6Address)
	}

	if netConfig["dns.mode"] == "" || netConfig["dns.mode"] == "managed" {
		line += fmt.Sprintf(",%s", project.DNS(projectName, instanceName))
	}

	if line == hwaddr {
		return nil
	}

	err := ioutil.WriteFile(shared.VarPath("networks", network, "dnsmasq.hosts", project.Instance(projectName, instanceName)), []byte(line+"\n"), 0644)
	if err != nil {
		return err
	}

	return nil
}

// RemoveStaticEntry removes a single dhcp-host line for a network/instance combination.
func RemoveStaticEntry(network string, projectName string, instanceName string) error {
	err := os.Remove(shared.VarPath("networks", network, "dnsmasq.hosts", project.Instance(projectName, instanceName)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// Kill kills dnsmasq for a particular network (or optionally reloads it).
func Kill(name string, reload bool) error {
	pidPath := shared.VarPath("networks", name, "dnsmasq.pid")

	// If the pid file doesn't exist, there is no process to kill.
	if !shared.PathExists(pidPath) {
		return nil
	}

	// Import saved subprocess details
	p, err := subprocess.ImportProcess(pidPath)
	if err != nil {
		return fmt.Errorf("Could not read pid file: %s", err)
	}

	if reload {
		err = p.Reload()
		if err != nil && err != subprocess.ErrNotRunning {
			return fmt.Errorf("Could not reload dnsmasq: %s", err)
		}

		return nil
	}

	err = p.Stop()
	if err != nil && err != subprocess.ErrNotRunning {
		return fmt.Errorf("Unable to kill dnsmasq: %s", err)
	}

	time.Sleep(100 * time.Millisecond) // Give OS time to release sockets.

	return nil
}

// GetVersion returns the version of dnsmasq.
func GetVersion() (*version.DottedVersion, error) {
	output, err := shared.RunCommandCLocale("dnsmasq", "--version")
	if err != nil {
		return nil, fmt.Errorf("Failed to check dnsmasq version: %v", err)
	}

	lines := strings.Split(string(output), " ")
	return version.Parse(lines[2])
}

// DHCPStaticAllocation retrieves the dnsmasq statically allocated MAC and IPs for an instance.
// Returns MAC, IPv4 and IPv6 DHCPAllocation structs respectively.
func DHCPStaticAllocation(network, projectName, instanceName string) (net.HardwareAddr, DHCPAllocation, DHCPAllocation, error) {
	var IPv4, IPv6 DHCPAllocation
	var mac net.HardwareAddr

	file, err := os.Open(shared.VarPath("networks", network, "dnsmasq.hosts", project.Instance(projectName, instanceName)))
	if err != nil {
		return nil, IPv4, IPv6, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.SplitN(scanner.Text(), ",", -1)
		for _, field := range fields {
			// Check if field is IPv4 or IPv6 address.
			if strings.Count(field, ".") == 3 {
				IP := net.ParseIP(field)
				if IP.To4() == nil {
					return nil, IPv4, IPv6, fmt.Errorf("Error parsing IP address %q", field)
				}
				IPv4 = DHCPAllocation{Name: instanceName, Static: true, IP: IP.To4(), MAC: mac}

			} else if strings.HasPrefix(field, "[") && strings.HasSuffix(field, "]") {
				IP := net.ParseIP(field[1 : len(field)-1])
				if IP == nil {
					return nil, IPv4, IPv6, fmt.Errorf("Error parsing IP address %q", field)
				}
				IPv6 = DHCPAllocation{Name: instanceName, Static: true, IP: IP, MAC: mac}
			} else if strings.Count(field, ":") == 5 {
				// This field is expected to come first, so that mac variable can be used with
				// populating the DHCPAllocation structs too.
				mac, err = net.ParseMAC(field)
				if err != nil {
					return nil, IPv4, IPv6, fmt.Errorf("Error parsing MAC address %q", field)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, IPv4, IPv6, err
	}

	return mac, IPv4, IPv6, nil
}

// DHCPAllAllocations returns a map of IPs currently allocated (statically and dynamically)
// in dnsmasq for a specific network. The returned map is keyed by a 16 byte array representing
// the net.IP format. The value of each map item is a DHCPAllocation struct containing at least
// whether the allocation was static or dynamic and optionally instance name or MAC address.
// MAC addresses are only included for dynamic IPv4 allocations (where name is not reliable).
// Static allocations are not overridden by dynamic allocations, allowing for instance name to be
// included for static IPv6 allocations. IPv6 addresses that are dynamically assigned cannot be
// reliably linked to instances using either name or MAC because dnsmasq does not record the MAC
// address for these records, and the recorded host name can be set by the instance if the dns.mode
// for the network is set to "dynamic" and so cannot be trusted, so in this case we do not return
// any identifying info.
func DHCPAllAllocations(network string) (map[[4]byte]DHCPAllocation, map[[16]byte]DHCPAllocation, error) {
	IPv4s := make(map[[4]byte]DHCPAllocation)
	IPv6s := make(map[[16]byte]DHCPAllocation)

	// First read all statically allocated IPs.
	files, err := ioutil.ReadDir(shared.VarPath("networks", network, "dnsmasq.hosts"))
	if err != nil && os.IsNotExist(err) {
		return nil, nil, err
	}

	for _, entry := range files {
		projectName, instanceName := project.InstanceParts(entry.Name())
		_, IPv4, IPv6, err := DHCPStaticAllocation(network, projectName, instanceName)
		if err != nil {
			return nil, nil, err
		}

		if IPv4.IP != nil {
			var IPKey [4]byte
			copy(IPKey[:], IPv4.IP.To4())
			IPv4s[IPKey] = IPv4
		}

		if IPv6.IP != nil {
			var IPKey [16]byte
			copy(IPKey[:], IPv6.IP.To16())
			IPv6s[IPKey] = IPv6
		}
	}

	// Next read all dynamic allocated IPs.
	file, err := os.Open(shared.VarPath("networks", network, "dnsmasq.leases"))
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 5 {
			IP := net.ParseIP(fields[2])
			if IP == nil {
				return nil, nil, fmt.Errorf("Error parsing IP address: %v", fields[2])
			}

			// Handle IPv6 addresses.
			if IP.To4() == nil {
				var IPKey [16]byte
				copy(IPKey[:], IP.To16())

				// Don't replace IPs from static config as more reliable.
				if IPv6s[IPKey].Name != "" {
					continue
				}

				IPv6s[IPKey] = DHCPAllocation{
					Static: false,
					IP:     IP.To16(),
				}
			} else {
				// MAC only available in IPv4 leases.
				MAC, err := net.ParseMAC(fields[1])
				if err != nil {
					return nil, nil, err
				}

				var IPKey [4]byte
				copy(IPKey[:], IP.To4())

				// Don't replace IPs from static config as more reliable.
				if IPv4s[IPKey].Name != "" {
					continue
				}

				IPv4s[IPKey] = DHCPAllocation{
					MAC:    MAC,
					Static: false,
					IP:     IP.To4(),
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	return IPv4s, IPv6s, nil
}
