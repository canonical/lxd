package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	log "gopkg.in/inconshreveable/log15.v2"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/ip"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

// A variation of the standard tls.Listener that supports atomically swapping
// the underlying TLS configuration. Requests served before the swap will
// continue using the old configuration.
type networkListener struct {
	net.Listener
	mu     sync.RWMutex
	config *tls.Config
}

func networkTLSListener(inner net.Listener, config *tls.Config) *networkListener {
	listener := &networkListener{
		Listener: inner,
		config:   config,
	}

	return listener
}

// Accept waits for and returns the next incoming TLS connection then use the
// current TLS configuration to handle it.
func (l *networkListener) Accept() (net.Conn, error) {
	var c net.Conn
	var err error

	// Accept() is non-blocking in go < 1.12 hence the loop and error check.
	for {
		c, err = l.Listener.Accept()
		if err == nil {
			break
		}

		if err.(net.Error).Temporary() {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		return nil, err
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	return tls.Server(c, l.config), nil
}

func serverTLSConfig() (*tls.Config, error) {
	certInfo, err := shared.KeyPairAndCA(".", "agent", shared.CertServer, false)
	if err != nil {
		return nil, err
	}

	tlsConfig := util.ServerTLSConfig(certInfo)
	return tlsConfig, nil
}

// reconfigureNetworkInterfaces checks for the existence of files under NICConfigDir in the config share.
// Each file is named <device>.json and contains the Device Name, NIC Name, MTU and MAC address.
func reconfigureNetworkInterfaces() {
	nicDirEntries, err := ioutil.ReadDir(deviceConfig.NICConfigDir)
	if err != nil {
		// Abort if configuration folder does not exist (nothing to do), otherwise log and return.
		if os.IsNotExist(err) {
			return
		}

		logger.Error("Could not read network interface configuration directory", log.Ctx{"err": err})
		return
	}

	// nicData is a map of MAC address to NICConfig.
	nicData := make(map[string]deviceConfig.NICConfig, len(nicDirEntries))

	for _, f := range nicDirEntries {
		nicBytes, err := ioutil.ReadFile(filepath.Join(deviceConfig.NICConfigDir, f.Name()))
		if err != nil {
			logger.Error("Could not read network interface configuration file", log.Ctx{"err": err})
		}

		var conf deviceConfig.NICConfig
		err = json.Unmarshal(nicBytes, &conf)
		if err != nil {
			logger.Error("Could not parse network interface configuration file", log.Ctx{"err": err})
			return
		}

		if conf.MACAddress != "" {
			nicData[conf.MACAddress] = conf
		}
	}

	// configureNIC applies any config specified for the interface based on its current MAC address.
	configureNIC := func(currentNIC net.Interface) error {
		reverter := revert.New()
		defer reverter.Fail()

		// Look for a NIC config entry for this interface based on its MAC address.
		nic, ok := nicData[currentNIC.HardwareAddr.String()]
		if !ok {
			return nil
		}

		var changeName, changeMTU bool
		if nic.NICName != "" && currentNIC.Name != nic.NICName {
			changeName = true
		}

		if nic.MTU > 0 && currentNIC.MTU != int(nic.MTU) {
			changeMTU = true
		}

		if !changeName && !changeMTU {
			return nil // Nothing to do.
		}

		link := ip.Link{
			Name: currentNIC.Name,
			MTU:  fmt.Sprintf("%d", currentNIC.MTU),
		}

		err := link.SetDown()
		if err != nil {
			return err
		}
		reverter.Add(func() {
			link.SetUp()
		})

		// Apply the name from the NIC config if needed.
		if changeName {
			err = link.SetName(nic.NICName)
			if err != nil {
				return err
			}
			reverter.Add(func() {
				link.SetName(currentNIC.Name)
				link.Name = currentNIC.Name
			})

			link.Name = nic.NICName
		}

		// Apply the MTU from the NIC config if needed.
		if changeMTU {
			newMTU := fmt.Sprintf("%d", nic.MTU)
			err = link.SetMTU(newMTU)
			if err != nil {
				return err
			}
			reverter.Add(func() {
				currentMTU := fmt.Sprintf("%d", currentNIC.MTU)
				link.SetMTU(currentMTU)
				link.MTU = currentMTU
			})

			link.MTU = newMTU
		}

		err = link.SetUp()
		if err != nil {
			return err
		}

		reverter.Success()
		return nil
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		logger.Error("Unable to read network interfaces", log.Ctx{"err": err})
	}

	for _, iface := range ifaces {
		err = configureNIC(iface)
		if err != nil {
			logger.Error("Unable to reconfigure network interface", log.Ctx{"interface": iface.Name, "err": err})
		}
	}

	return
}
