package device

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	log "gopkg.in/inconshreveable/log15.v2"
	liblxc "gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/lxd/apparmor"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/device/nictype"
	firewallDrivers "github.com/lxc/lxd/lxd/firewall/drivers"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/ip"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/warnings"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/subprocess"
	"github.com/lxc/lxd/shared/validate"
)

type proxy struct {
	deviceCommon
}

type proxyProcInfo struct {
	listenPid      string
	listenPidFd    string
	connectPid     string
	connectPidFd   string
	connectAddr    string
	listenAddr     string
	listenAddrGID  string
	listenAddrUID  string
	listenAddrMode string
	securityUID    string
	securityGID    string
	proxyProtocol  string
	inheritFds     []*os.File
}

// validateConfig checks the supplied config for correctness.
func (d *proxy) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	validateAddr := func(input string) error {
		_, err := ProxyParseAddr(input)
		return err
	}

	// Supported bind types are: "host" or "instance" (or "guest" or "container", legacy options equivalent to "instance").
	// If an empty value is supplied the default behavior is to assume "host" bind mode.
	validateBind := func(input string) error {
		if !shared.StringInSlice(d.config["bind"], []string{"host", "instance", "guest", "container"}) {
			return fmt.Errorf("Invalid binding side given. Must be \"host\" or \"instance\"")
		}

		return nil
	}

	rules := map[string]func(string) error{
		"listen":         validate.Required(validateAddr),
		"connect":        validate.Required(validateAddr),
		"bind":           validate.Optional(validateBind),
		"mode":           validate.Optional(unixValidOctalFileMode),
		"nat":            validate.Optional(validate.IsBool),
		"gid":            validate.Optional(unixValidUserID),
		"uid":            validate.Optional(unixValidUserID),
		"security.uid":   validate.Optional(unixValidUserID),
		"security.gid":   validate.Optional(unixValidUserID),
		"proxy_protocol": validate.Optional(validate.IsBool),
	}

	err := d.config.Validate(rules)
	if err != nil {
		return err
	}

	if instConf.Type() == instancetype.VM && !shared.IsTrue(d.config["nat"]) {
		return fmt.Errorf("Only NAT mode is supported for proxies on VM instances")
	}

	listenAddr, err := ProxyParseAddr(d.config["listen"])
	if err != nil {
		return err
	}

	connectAddr, err := ProxyParseAddr(d.config["connect"])
	if err != nil {
		return err
	}

	if (listenAddr.ConnType != "unix" && len(connectAddr.Ports) > len(listenAddr.Ports)) || (listenAddr.ConnType == "unix" && len(connectAddr.Ports) > 1) {
		// Cannot support single address (or port) -> multiple port.
		return fmt.Errorf("Mismatch between listen port(s) and connect port(s) count")
	}

	if shared.IsTrue(d.config["proxy_protocol"]) && (!strings.HasPrefix(d.config["connect"], "tcp") || shared.IsTrue(d.config["nat"])) {
		return fmt.Errorf("The PROXY header can only be sent to tcp servers in non-nat mode")
	}

	if (!strings.HasPrefix(d.config["listen"], "unix:") || strings.HasPrefix(d.config["listen"], "unix:@")) &&
		(d.config["uid"] != "" || d.config["gid"] != "" || d.config["mode"] != "") {
		return fmt.Errorf("Only proxy devices for non-abstract unix sockets can carry uid, gid, or mode properties")
	}

	if shared.IsTrue(d.config["nat"]) {
		if d.inst != nil {
			// Default project always has networks feature so don't bother loading the project config
			// in that case.
			projectName := d.inst.Project()
			if projectName != project.Default {
				// Prevent use of NAT mode on non-default projects with networks feature.
				// This is because OVN networks don't allow the host to communicate directly with
				// instance NICs and so DNAT rules on the host won't work.
				var p *db.Project
				var config map[string]string
				err := d.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
					p, err = tx.GetProject(projectName)
					if err != nil {
						return err
					}

					config, err = tx.GetProjectConfig(p.ID)
					if err != nil {
						return err
					}

					return nil
				})
				if err != nil {
					return fmt.Errorf("Failed loading project %q: %w", projectName, err)
				}

				if shared.IsTrue(config["features.networks"]) {
					return fmt.Errorf("NAT mode cannot be used in projects that have the networks feature")
				}
			}
		}

		if d.config["bind"] != "" && d.config["bind"] != "host" {
			return fmt.Errorf("Only host-bound proxies can use NAT")
		}

		// Support TCP <-> TCP and UDP <-> UDP only.
		if listenAddr.ConnType == "unix" || connectAddr.ConnType == "unix" || listenAddr.ConnType != connectAddr.ConnType {
			return fmt.Errorf("Proxying %s <-> %s is not supported when using NAT", listenAddr.ConnType, connectAddr.ConnType)
		}

		listenAddress := net.ParseIP(listenAddr.Address)

		if listenAddress.Equal(net.IPv4zero) || listenAddress.Equal(net.IPv6zero) {
			return fmt.Errorf("Cannot listen on wildcard address %q when in nat mode", listenAddress.String())
		}

		// Records which listen address IP version, as these cannot be mixed in NAT mode.
		listenIPVersion := uint(4)
		if listenAddress.To4() == nil {
			listenIPVersion = 6
		}

		// Check connect address against the listen address IP version and check they match.
		connectAddress := net.ParseIP(connectAddr.Address)
		connectIPVersion := uint(4)
		if connectAddress.To4() == nil {
			connectIPVersion = 6
		}

		if listenIPVersion != connectIPVersion {
			return fmt.Errorf("Cannot mix IP versions between listen and connect in nat mode")
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

// Start is run when the device is added to the instance.
func (d *proxy) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Proxy devices have to be setup once the instance is running.
	runConf := deviceConfig.RunConfig{}
	runConf.PostHooks = []func() error{
		func() error {
			if shared.IsTrue(d.config["nat"]) {
				err = d.setupNAT()
				if err != nil {
					return fmt.Errorf("Failed to start device %q: %w", d.name, err)
				}

				return nil // Don't proceed with forkproxy setup.
			}

			proxyValues, err := d.setupProxyProcInfo()
			if err != nil {
				return err
			}

			devFileName := fmt.Sprintf("proxy.%s", d.name)
			pidPath := filepath.Join(d.inst.DevicesPath(), devFileName)
			logFileName := fmt.Sprintf("proxy.%s.log", d.name)
			logPath := filepath.Join(d.inst.LogPath(), logFileName)

			// Load the apparmor profile
			err = apparmor.ForkproxyLoad(d.state, d.inst, d)
			if err != nil {
				return fmt.Errorf("Failed to start device %q: %w", d.name, err)
			}

			// Spawn the daemon using subprocess
			command := d.state.OS.ExecPath
			forkproxyargs := []string{"forkproxy",
				"--",
				proxyValues.listenPid,
				proxyValues.listenPidFd,
				proxyValues.listenAddr,
				proxyValues.connectPid,
				proxyValues.connectPidFd,
				proxyValues.connectAddr,
				proxyValues.listenAddrGID,
				proxyValues.listenAddrUID,
				proxyValues.listenAddrMode,
				proxyValues.securityGID,
				proxyValues.securityUID,
				proxyValues.proxyProtocol,
			}

			p, err := subprocess.NewProcess(command, forkproxyargs, logPath, logPath)
			if err != nil {
				return fmt.Errorf("Failed to start device %q: Failed to creating subprocess: %w", d.name, err)
			}

			p.SetApparmor(apparmor.ForkproxyProfileName(d.inst, d))

			err = p.StartWithFiles(proxyValues.inheritFds)
			if err != nil {
				return fmt.Errorf("Failed to start device %q: Failed running: %s %s: %w", d.name, command, strings.Join(forkproxyargs, " "), err)
			}

			for _, file := range proxyValues.inheritFds {
				file.Close()
			}

			// Poll log file a few times until we see "Started" to indicate successful start.
			for i := 0; i < 10; i++ {
				started, err := d.checkProcStarted(logPath)
				if err != nil {
					p.Stop()
					return fmt.Errorf("Error occurred when starting proxy device: %s", err)
				}

				if started {
					err = p.Save(pidPath)
					if err != nil {
						// Kill Process if started, but could not save the file
						err2 := p.Stop()
						if err != nil {
							return fmt.Errorf("Could not kill subprocess while handling saving error: %s: %s", err, err2)
						}

						return fmt.Errorf("Failed to start device %q: Failed saving subprocess details: %w", d.name, err)
					}

					return nil
				}

				time.Sleep(time.Second)
			}

			p.Stop()
			return fmt.Errorf("Failed to start device %q: Please look in %s", d.name, logPath)
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
func (d *proxy) Stop() (*deviceConfig.RunConfig, error) {
	// Remove possible iptables entries
	err := d.state.Firewall.InstanceClearProxyNAT(d.inst.Project(), d.inst.Name(), d.name)
	if err != nil {
		logger.Errorf("Failed to remove proxy NAT filters: %v", err)
	}

	devFileName := fmt.Sprintf("proxy.%s", d.name)
	devPath := filepath.Join(d.inst.DevicesPath(), devFileName)

	if !shared.PathExists(devPath) {
		// There's no proxy process if NAT is enabled
		return nil, nil
	}

	err = d.killProxyProc(devPath)
	if err != nil {
		return nil, err
	}

	// Unload apparmor profile.
	err = apparmor.ForkproxyUnload(d.state, d.inst, d)
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

	ipVersion := uint(4)
	if strings.Contains(listenAddr.Address, ":") {
		ipVersion = 6
	}

	var connectIP net.IP
	var hostName string

	for devName, devConfig := range d.inst.ExpandedDevices() {
		if devConfig["type"] != "nic" {
			continue
		}

		nicType, err := nictype.NICType(d.state, d.inst.Project(), devConfig)
		if err != nil {
			return err
		}

		if nicType != "bridged" {
			continue
		}

		// Ensure the connect IP matches one of the NIC's static IPs otherwise we could mess with other
		// instance's network traffic. If the wildcard address is supplied as the connect host then the
		// first bridged NIC which has a static IP address defined is selected as the connect host IP.
		if ipVersion == 4 && devConfig["ipv4.address"] != "" {
			if connectAddr.Address == devConfig["ipv4.address"] || connectAddr.Address == "0.0.0.0" {
				connectIP = net.ParseIP(devConfig["ipv4.address"])
			}
		} else if ipVersion == 6 && devConfig["ipv6.address"] != "" {
			if connectAddr.Address == devConfig["ipv6.address"] || connectAddr.Address == "::" {
				connectIP = net.ParseIP(devConfig["ipv6.address"])
			}
		}

		if connectIP != nil {
			// Get host_name of device so we can enable hairpin mode on bridge port.
			hostName = d.inst.ExpandedConfig()[fmt.Sprintf("volatile.%s.host_name", devName)]
			break // Found a match, stop searching.
		}
	}

	if connectIP == nil {
		if connectAddr.Address == "0.0.0.0" || connectAddr.Address == "::" {
			return fmt.Errorf("Instance has no static IPv%d address assigned to be used as the connect IP", ipVersion)
		}

		return fmt.Errorf("Connect IP %q must be one of the instance's static IPv%d addresses", connectAddr.Address, ipVersion)
	}

	// Override the host part of the connectAddr.Addr to the chosen connect IP.
	connectAddr.Address = connectIP.String()

	err = network.BridgeNetfilterEnabled(ipVersion)
	if err != nil {
		msg := fmt.Sprintf("IPv%d bridge netfilter not enabled. Instances using the bridge will not be able to connect to the proxy listen IP", ipVersion)
		d.logger.Warn(msg, log.Ctx{"err": err})
		err := d.state.Cluster.UpsertWarningLocalNode(d.inst.Project(), cluster.TypeInstance, d.inst.ID(), db.WarningProxyBridgeNetfilterNotEnabled, fmt.Sprintf("%s: %v", msg, err))
		if err != nil {
			logger.Warn("Failed to create warning", log.Ctx{"err": err})
		}
	} else {
		err = warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(d.state.Cluster, d.inst.Project(), db.WarningProxyBridgeNetfilterNotEnabled, cluster.TypeInstance, d.inst.ID())
		if err != nil {
			logger.Warn("Failed to resolve warning", log.Ctx{"err": err})
		}

		if hostName == "" {
			return fmt.Errorf("Proxy cannot find bridge port host_name to enable hairpin mode")
		}

		// br_netfilter is enabled, so we need to enable hairpin mode on instance's bridge port otherwise
		// the instances on the bridge will not be able to connect to the proxy device's listn IP and the
		// NAT rule added by the firewall below to allow instance <-> instance traffic will also not work.
		link := &ip.Link{Name: hostName}
		err = link.BridgeLinkSetHairpin(true)
		if err != nil {
			return errors.Wrapf(err, "Error enabling hairpin mode on bridge port %q", hostName)
		}
	}

	// Convert proxy listen & connect addresses for firewall AddressForward.
	addressForward := firewallDrivers.AddressForward{
		Protocol:      listenAddr.ConnType,
		ListenAddress: net.ParseIP(listenAddr.Address),
		ListenPorts:   listenAddr.Ports,
		TargetAddress: net.ParseIP(connectAddr.Address),
		TargetPorts:   connectAddr.Ports,
	}

	err = d.state.Firewall.InstanceSetupProxyNAT(d.inst.Project(), d.inst.Name(), d.name, &addressForward)
	if err != nil {
		return err
	}

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
	cname := project.Instance(d.inst.Project(), d.inst.Name())
	cc, err := liblxc.NewContainer(cname, d.state.OS.LxcPath)
	if err != nil {
		return nil, err
	}
	defer cc.Release()

	containerPid := strconv.Itoa(cc.InitPid())
	lxdPid := strconv.Itoa(os.Getpid())

	containerPidFd := -1
	lxdPidFd := -1
	var inheritFd []*os.File
	if d.state.OS.PidFds {
		cPidFd, err := cc.InitPidFd()
		if err == nil {
			dPidFd, err := shared.PidFdOpen(os.Getpid(), 0)
			if err == nil {
				inheritFd = []*os.File{cPidFd, dPidFd}
				containerPidFd = 3
				lxdPidFd = 4
			}
		}
	}

	var listenPid, listenPidFd, connectPid, connectPidFd string

	connectAddr := d.config["connect"]
	listenAddr := d.config["listen"]

	switch d.config["bind"] {
	case "host", "":
		listenPid = lxdPid
		listenPidFd = fmt.Sprintf("%d", lxdPidFd)

		connectPid = containerPid
		connectPidFd = fmt.Sprintf("%d", containerPidFd)

		listenAddr = d.rewriteHostAddr(listenAddr)
	case "instance", "guest", "container":
		listenPid = containerPid
		listenPidFd = fmt.Sprintf("%d", containerPidFd)

		connectPid = lxdPid
		connectPidFd = fmt.Sprintf("%d", lxdPidFd)

		connectAddr = d.rewriteHostAddr(connectAddr)
	default:
		return nil, fmt.Errorf("Invalid binding side given. Must be \"host\" or \"instance\"")
	}

	listenAddrMode := "0644"
	if d.config["mode"] != "" {
		listenAddrMode = d.config["mode"]
	}

	p := &proxyProcInfo{
		listenPid:      listenPid,
		listenPidFd:    listenPidFd,
		connectPid:     connectPid,
		connectPidFd:   connectPidFd,
		connectAddr:    connectAddr,
		listenAddr:     listenAddr,
		listenAddrGID:  d.config["gid"],
		listenAddrUID:  d.config["uid"],
		listenAddrMode: listenAddrMode,
		securityGID:    d.config["security.gid"],
		securityUID:    d.config["security.uid"],
		proxyProtocol:  d.config["proxy_protocol"],
		inheritFds:     inheritFd,
	}

	return p, nil
}

func (d *proxy) killProxyProc(pidPath string) error {
	// If the pid file doesn't exist, there is no process to kill.
	if !shared.PathExists(pidPath) {
		return nil
	}

	p, err := subprocess.ImportProcess(pidPath)
	if err != nil {
		return fmt.Errorf("Could not read pid file: %s", err)
	}

	err = p.Stop()
	if err != nil && err != subprocess.ErrNotRunning {
		return fmt.Errorf("Unable to kill forkproxy: %s", err)
	}

	os.Remove(pidPath)
	return nil
}

func (d *proxy) Remove() error {
	err := warnings.DeleteWarningsByLocalNodeAndProjectAndTypeAndEntity(d.state.Cluster, d.inst.Project(), db.WarningProxyBridgeNetfilterNotEnabled, cluster.TypeInstance, d.inst.ID())
	if err != nil {
		logger.Warn("Failed to delete warning", log.Ctx{"err": err})
	}

	// Delete apparmor profile.
	err = apparmor.ForkproxyDelete(d.state, d.inst, d)
	if err != nil {
		return err
	}

	return nil
}
