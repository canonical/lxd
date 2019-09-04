package device

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/iptables"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
)

type proxy struct {
	deviceCommon
}

type proxyProcInfo struct {
	listenPid      string
	connectPid     string
	connectAddr    string
	listenAddr     string
	listenAddrGID  string
	listenAddrUID  string
	listenAddrMode string
	securityUID    string
	securityGID    string
	proxyProtocol  string
}

// validateConfig checks the supplied config for correctness.
func (d *proxy) validateConfig() error {
	if d.instance.Type() != instance.TypeContainer {
		return ErrUnsupportedDevType
	}

	validateAddr := func(input string) error {
		_, err := ProxyParseAddr(input)
		return err
	}

	// Supported bind types are: "host" or "guest" (and "container", a legacy option equivalent to "guest").
	// If an empty value is supplied the default behavior is to assume "host" bind mode.
	validateBind := func(input string) error {
		if !shared.StringInSlice(d.config["bind"], []string{"", "host", "guest", "container"}) {
			return fmt.Errorf("Invalid binding side given. Must be \"host\" or \"guest\"")
		}

		return nil
	}

	rules := map[string]func(string) error{
		"listen":         validateAddr,
		"connect":        validateAddr,
		"bind":           validateBind,
		"mode":           unixValidOctalFileMode,
		"nat":            shared.IsBool,
		"gid":            unixValidUserID,
		"uid":            unixValidUserID,
		"security.uid":   unixValidUserID,
		"security.gid":   unixValidUserID,
		"proxy_protocol": shared.IsBool,
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	listenAddr, err := ProxyParseAddr(d.config["listen"])
	if err != nil {
		return err
	}

	connectAddr, err := ProxyParseAddr(d.config["connect"])
	if err != nil {
		return err
	}

	if len(connectAddr.Addr) > len(listenAddr.Addr) {
		// Cannot support single port -> multiple port
		return fmt.Errorf("Cannot map a single port to multiple ports")
	}

	if shared.IsTrue(d.config["proxy_protocol"]) && !strings.HasPrefix(d.config["connect"], "tcp") {
		return fmt.Errorf("The PROXY header can only be sent to tcp servers")
	}

	if (!strings.HasPrefix(d.config["listen"], "unix:") || strings.HasPrefix(d.config["listen"], "unix:@")) &&
		(d.config["uid"] != "" || d.config["gid"] != "" || d.config["mode"] != "") {
		return fmt.Errorf("Only proxy devices for non-abstract unix sockets can carry uid, gid, or mode properties")
	}

	if shared.IsTrue(d.config["nat"]) {
		if d.config["bind"] != "" && d.config["bind"] != "host" {
			return fmt.Errorf("Only host-bound proxies can use NAT")
		}

		// Support TCP <-> TCP and UDP <-> UDP
		if listenAddr.ConnType == "unix" || connectAddr.ConnType == "unix" ||
			listenAddr.ConnType != connectAddr.ConnType {
			return fmt.Errorf("Proxying %s <-> %s is not supported when using NAT",
				listenAddr.ConnType, connectAddr.ConnType)
		}
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *proxy) validateEnvironment() error {
	if d.name == "" {
		return fmt.Errorf("Device name cannot be empty")
	}

	return nil
}

// Start is run when the device is added to the container.
func (d *proxy) Start() (*RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Proxy devices have to be setup once the container is running.
	runConf := RunConfig{}
	runConf.PostHooks = []func() error{
		func() error {
			if shared.IsTrue(d.config["nat"]) {
				return d.setupNAT()
			}

			proxyValues, err := d.setupProxyProcInfo()
			if err != nil {
				return err
			}

			devFileName := fmt.Sprintf("proxy.%s", d.name)
			pidPath := filepath.Join(d.instance.DevicesPath(), devFileName)
			logFileName := fmt.Sprintf("proxy.%s.log", d.name)
			logPath := filepath.Join(d.instance.LogPath(), logFileName)

			_, err = shared.RunCommand(
				d.state.OS.ExecPath,
				"forkproxy",
				proxyValues.listenPid,
				proxyValues.listenAddr,
				proxyValues.connectPid,
				proxyValues.connectAddr,
				logPath,
				pidPath,
				proxyValues.listenAddrGID,
				proxyValues.listenAddrUID,
				proxyValues.listenAddrMode,
				proxyValues.securityGID,
				proxyValues.securityUID,
				proxyValues.proxyProtocol,
			)
			if err != nil {
				return fmt.Errorf("Error occurred when starting proxy device: %s", err)
			}

			// Poll log file a few times until we see "Started" to indicate successful start.
			for i := 0; i < 10; i++ {
				started, err := d.checkProcStarted(logPath)

				if err != nil {
					return fmt.Errorf("Error occurred when starting proxy device: %s", err)
				}

				if started {
					return nil
				}

				time.Sleep(time.Second)
			}

			return fmt.Errorf("Error occurred when starting proxy device, please look in %s", logPath)
		},
	}

	return &runConf, nil
}

// checkProcStarted checks for the "Started" line in the log file. Returns true if found, false
// if not, and error if any other error occurs.
func (d *proxy) checkProcStarted(logPath string) (bool, error) {
	file, err := os.Open(logPath)
	if err != nil {
		return false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "Status: Started" {
			return true, nil
		}

		if strings.HasPrefix(line, "Error:") {
			return false, fmt.Errorf("%s", line)
		}
	}

	err = scanner.Err()
	if err != nil {
		return false, err
	}

	return false, nil
}

// Stop is run when the device is removed from the instance.
func (d *proxy) Stop() (*RunConfig, error) {
	// Remove possible iptables entries
	iptables.ContainerClear("ipv4", fmt.Sprintf("%s (%s)", d.instance.Name(), d.name), "nat")
	iptables.ContainerClear("ipv6", fmt.Sprintf("%s (%s)", d.instance.Name(), d.name), "nat")

	devFileName := fmt.Sprintf("proxy.%s", d.name)
	devPath := filepath.Join(d.instance.DevicesPath(), devFileName)

	if !shared.PathExists(devPath) {
		// There's no proxy process if NAT is enabled
		return nil, nil
	}

	err := d.killProxyProc(devPath)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (d *proxy) setupNAT() error {
	listenAddr, err := ProxyParseAddr(d.config["listen"])
	if err != nil {
		return err
	}

	connectAddr, err := ProxyParseAddr(d.config["connect"])
	if err != nil {
		return err
	}

	address, _, err := net.SplitHostPort(connectAddr.Addr[0])
	if err != nil {
		return err
	}

	var IPv4Addr string
	var IPv6Addr string

	instanceConfig, err := InstanceLoadByProjectAndName(d.state, d.instance.Project(), d.instance.Name())
	if err != nil {
		return err
	}

	for _, devConfig := range instanceConfig.ExpandedDevices() {
		if devConfig["type"] != "nic" || (devConfig["type"] == "nic" && devConfig["nictype"] != "bridged") {
			continue
		}

		// Check whether the NIC has a static IP.
		ip := devConfig["ipv4.address"]
		// Ensure that the provided IP address matches the container's IP
		// address otherwise we could mess with other containers.
		if ip != "" && IPv4Addr == "" && (address == ip || address == "0.0.0.0") {
			IPv4Addr = ip
		}

		ip = devConfig["ipv6.address"]
		if ip != "" && IPv6Addr == "" && (address == ip || address == "::") {
			IPv6Addr = ip
		}
	}

	if IPv4Addr == "" && IPv6Addr == "" {
		return fmt.Errorf("NIC IP doesn't match proxy target IP")
	}

	iptablesComment := fmt.Sprintf("%s (%s)", d.instance.Name(), d.name)

	revert := true
	defer func() {
		if revert {
			if IPv4Addr != "" {
				iptables.ContainerClear("ipv4", iptablesComment, "nat")
			}

			if IPv6Addr != "" {
				iptables.ContainerClear("ipv6", iptablesComment, "nat")
			}
		}
	}()

	for i, lAddr := range listenAddr.Addr {
		address, port, err := net.SplitHostPort(lAddr)
		if err != nil {
			return err
		}
		var cPort string
		if len(connectAddr.Addr) == 1 {
			_, cPort, _ = net.SplitHostPort(connectAddr.Addr[0])
		} else {
			_, cPort, _ = net.SplitHostPort(connectAddr.Addr[i])
		}

		if IPv4Addr != "" {
			// outbound <-> container
			err := iptables.ContainerPrepend("ipv4", iptablesComment, "nat",
				"PREROUTING", "-p", listenAddr.ConnType, "--destination",
				address, "--dport", port, "-j", "DNAT",
				"--to-destination", fmt.Sprintf("%s:%s", IPv4Addr, cPort))
			if err != nil {
				return err
			}

			// host <-> container
			err = iptables.ContainerPrepend("ipv4", iptablesComment, "nat",
				"OUTPUT", "-p", listenAddr.ConnType, "--destination",
				address, "--dport", port, "-j", "DNAT",
				"--to-destination", fmt.Sprintf("%s:%s", IPv4Addr, cPort))
			if err != nil {
				return err
			}
		}

		if IPv6Addr != "" {
			// outbound <-> container
			err := iptables.ContainerPrepend("ipv6", iptablesComment, "nat",
				"PREROUTING", "-p", listenAddr.ConnType, "--destination",
				address, "--dport", port, "-j", "DNAT",
				"--to-destination", fmt.Sprintf("[%s]:%s", IPv6Addr, cPort))
			if err != nil {
				return err
			}

			// host <-> container
			err = iptables.ContainerPrepend("ipv6", iptablesComment, "nat",
				"OUTPUT", "-p", listenAddr.ConnType, "--destination",
				address, "--dport", port, "-j", "DNAT",
				"--to-destination", fmt.Sprintf("[%s]:%s", IPv6Addr, cPort))
			if err != nil {
				return err
			}
		}
	}

	revert = false
	return nil
}

func (d *proxy) rewriteHostAddr(addr string) string {
	fields := strings.SplitN(addr, ":", 2)
	proto := fields[0]
	addr = fields[1]
	if proto == "unix" && !strings.HasPrefix(addr, "@") {
		// Unix non-abstract sockets need to be addressed to the host
		// filesystem, not be scoped inside the LXD snap.
		addr = shared.HostPath(addr)
	}
	return fmt.Sprintf("%s:%s", proto, addr)
}

func (d *proxy) setupProxyProcInfo() (*proxyProcInfo, error) {
	cname := project.Prefix(d.instance.Project(), d.instance.Name())
	cc, err := lxc.NewContainer(cname, d.state.OS.LxcPath)
	if err != nil {
		return nil, err
	}
	defer cc.Release()

	containerPid := strconv.Itoa(cc.InitPid())
	lxdPid := strconv.Itoa(os.Getpid())

	var listenPid, connectPid string

	connectAddr := d.config["connect"]
	listenAddr := d.config["listen"]

	switch d.config["bind"] {
	case "host", "":
		listenPid = lxdPid
		connectPid = containerPid
		listenAddr = d.rewriteHostAddr(listenAddr)
	case "guest", "container":
		listenPid = containerPid
		connectPid = lxdPid
		connectAddr = d.rewriteHostAddr(connectAddr)
	default:
		return nil, fmt.Errorf("Invalid binding side given. Must be \"host\" or \"guest\"")
	}

	listenAddrMode := "0644"
	if d.config["mode"] != "" {
		listenAddrMode = d.config["mode"]
	}

	p := &proxyProcInfo{
		listenPid:      listenPid,
		connectPid:     connectPid,
		connectAddr:    connectAddr,
		listenAddr:     listenAddr,
		listenAddrGID:  d.config["gid"],
		listenAddrUID:  d.config["uid"],
		listenAddrMode: listenAddrMode,
		securityGID:    d.config["security.gid"],
		securityUID:    d.config["security.uid"],
		proxyProtocol:  d.config["proxy_protocol"],
	}

	return p, nil
}

func (d *proxy) killProxyProc(pidPath string) error {
	// Get the contents of the pid file
	contents, err := ioutil.ReadFile(pidPath)
	if err != nil {
		return err
	}
	pidString := strings.TrimSpace(string(contents))

	// Check if the process still exists
	if !shared.PathExists(fmt.Sprintf("/proc/%s", pidString)) {
		os.Remove(pidPath)
		return nil
	}

	// Check if it's forkdns
	cmdArgs, err := ioutil.ReadFile(fmt.Sprintf("/proc/%s/cmdline", pidString))
	if err != nil {
		os.Remove(pidPath)
		return nil
	}

	cmdFields := strings.Split(string(bytes.TrimRight(cmdArgs, string("\x00"))), string(byte(0)))
	if len(cmdFields) < 5 || cmdFields[1] != "forkproxy" {
		os.Remove(pidPath)
		return nil
	}

	// Parse the pid
	pidInt, err := strconv.Atoi(pidString)
	if err != nil {
		return err
	}

	// Actually kill the process
	err = unix.Kill(pidInt, unix.SIGKILL)
	if err != nil {
		return err
	}

	// Cleanup
	os.Remove(pidPath)
	return nil
}
