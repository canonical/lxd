package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/inconshreveable/log15.v2"

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
	tlsConfig.CipherSuites = []uint16{
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	}

	return tlsConfig, nil
}

// reconfigureNetworkInterfaces checks for the existence of files under ./nics in the config share.
// Each file is named <device>.json and contains the MTU and MAC address.
func reconfigureNetworkInterfaces() {
	// Abort if configuration folder does not exist (nothing to do), otherwise log and return.
	nicDir, err := ioutil.ReadDir("nics")
	if os.IsNotExist(err) {
		return
	} else if err != nil {
		logger.Error("Could not read network interface configuration directory", log15.Ctx{"err": err})
		return
	}

	// nicData is a map of MAC address to new interface name and MTU.
	nicData := make(map[string]struct {
		Name string
		MTU  uint32
	})
	for _, f := range nicDir {
		nicBytes, err := ioutil.ReadFile(filepath.Join("nics", f.Name()))
		if err != nil {
			logger.Error("Could not read network interface configuration file", log15.Ctx{"err": err})
		}

		var conf deviceConfig.NICConfig
		err = json.Unmarshal(nicBytes, &conf)
		if err != nil {
			logger.Error("Could not parse network interface configuration file", log15.Ctx{"err": err})
			return
		}

		// The new interface name will be the device name (which is the file name sans ".json").
		newInterfaceName := strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
		nicData[conf.MACAddress] = struct {
			Name string
			MTU  uint32
		}{
			Name: newInterfaceName,
			MTU:  conf.MTU,
		}
	}

	configureNIC := func(currentInterfaceName, currentMACAddress, currentMTU string) error {
		reverter := revert.New()
		defer reverter.Fail()

		nic, ok := nicData[currentMACAddress]
		if !ok {
			return nil
		}

		link := ip.Link{
			Name: currentInterfaceName,
			MTU:  currentMTU,
		}
		err := link.SetDown()
		if err != nil {
			return err
		}
		reverter.Add(func() {
			link.SetUp()
		})

		err = link.SetName(nic.Name)
		if err != nil {
			return err
		}
		link.Name = nic.Name
		reverter.Add(func() {
			link.SetName(currentInterfaceName)
		})

		if nic.MTU != 0 {
			err = link.SetMTU(fmt.Sprintf("%d", nic.MTU))
			if err != nil {
				return err
			}
			reverter.Add(func() {
				link.SetMTU(currentMTU)
			})
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
		logger.Error("Unable to read network interfaces")
	}

	for _, iface := range ifaces {
		err = configureNIC(iface.Name, iface.HardwareAddr.String(), strconv.Itoa(iface.MTU))
		if err != nil {
			logger.Error("Unable to reconfigure network interface", log15.Ctx{"err": err})
		}
	}

	return
}
