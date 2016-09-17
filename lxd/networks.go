package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
)

// Helper functions
func networkGetInterfaces(d *Daemon) ([]string, error) {
	networks, err := dbNetworks(d.db)
	if err != nil {
		return nil, err
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range ifaces {
		if !shared.StringInSlice(iface.Name, networks) {
			networks = append(networks, iface.Name)
		}
	}

	return networks, nil
}

func networkIsInUse(c container, name string) bool {
	for _, d := range c.ExpandedDevices() {
		if d["type"] != "nic" {
			continue
		}

		if !shared.StringInSlice(d["nictype"], []string{"bridged", "macvlan"}) {
			continue
		}

		if d["parent"] == "" {
			continue
		}

		if d["parent"] == name {
			return true
		}
	}

	return false
}

// API endpoints
func networksGet(d *Daemon, r *http.Request) Response {
	recursionStr := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	ifs, err := networkGetInterfaces(d)
	if err != nil {
		return InternalError(err)
	}

	resultString := []string{}
	resultMap := []shared.NetworkConfig{}
	for _, iface := range ifs {
		if recursion == 0 {
			resultString = append(resultString, fmt.Sprintf("/%s/networks/%s", shared.APIVersion, iface))
		} else {
			net, err := doNetworkGet(d, iface)
			if err != nil {
				continue
			}
			resultMap = append(resultMap, net)
		}
	}

	if recursion == 0 {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

func networksPost(d *Daemon, r *http.Request) Response {
	req := shared.NetworkConfig{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	err = networkValidName(req.Name)
	if err != nil {
		return BadRequest(err)
	}

	if req.Type != "" && req.Type != "bridge" {
		return BadRequest(fmt.Errorf("Only 'bridge' type networks can be created"))
	}

	networks, err := networkGetInterfaces(d)
	if err != nil {
		return InternalError(err)
	}

	if shared.StringInSlice(req.Name, networks) {
		return BadRequest(fmt.Errorf("The network already exists"))
	}

	err = networkValidateConfig(req.Config)
	if err != nil {
		return BadRequest(err)
	}

	// Set some default values where needed
	if req.Config["bridge.mode"] == "fan" {
		if req.Config["fan.underlay_subnet"] == "" {
			req.Config["fan.underlay_subnet"] = "auto"
		}
	} else {
		if req.Config["ipv4.address"] == "" {
			req.Config["ipv4.address"] = "auto"
			if req.Config["ipv4.nat"] == "" {
				req.Config["ipv4.nat"] = "true"
			}
		}

		if req.Config["ipv6.address"] == "" {
			content, err := ioutil.ReadFile("/proc/sys/net/ipv6/conf/default/disable_ipv6")
			if err == nil && string(content) == "0\n" {
				req.Config["ipv6.address"] = "auto"
				if req.Config["ipv6.nat"] == "" {
					req.Config["ipv6.nat"] = "true"
				}
			}
		}
	}

	// Replace "auto" by actual values
	err = networkFillAuto(req.Config)
	if err != nil {
		return InternalError(err)
	}

	// Create the database entry
	_, err = dbNetworkCreate(d.db, req.Name, req.Config)
	if err != nil {
		return InternalError(
			fmt.Errorf("Error inserting %s into database: %s", req.Name, err))
	}

	// Start the network
	n, err := networkLoadByName(d, req.Name)
	if err != nil {
		return InternalError(err)
	}

	err = n.Start()
	if err != nil {
		n.Delete()
		return InternalError(err)
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/networks/%s", shared.APIVersion, req.Name))
}

var networksCmd = Command{name: "networks", get: networksGet, post: networksPost}

func networkGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	n, err := doNetworkGet(d, name)
	if err != nil {
		return SmartError(err)
	}

	etag := []interface{}{n.Name, n.Managed, n.Type, n.Config}

	return SyncResponseETag(true, &n, etag)
}

func doNetworkGet(d *Daemon, name string) (shared.NetworkConfig, error) {
	// Get some information
	osInfo, _ := net.InterfaceByName(name)
	_, dbInfo, _ := dbNetworkGet(d.db, name)

	// Sanity check
	if osInfo == nil && dbInfo == nil {
		return shared.NetworkConfig{}, os.ErrNotExist
	}

	// Prepare the response
	n := shared.NetworkConfig{}
	n.Name = name
	n.UsedBy = []string{}
	n.Config = map[string]string{}

	// Look for containers using the interface
	cts, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return shared.NetworkConfig{}, err
	}

	for _, ct := range cts {
		c, err := containerLoadByName(d, ct)
		if err != nil {
			return shared.NetworkConfig{}, err
		}

		if networkIsInUse(c, n.Name) {
			n.UsedBy = append(n.UsedBy, fmt.Sprintf("/%s/containers/%s", shared.APIVersion, ct))
		}
	}

	// Set the device type as needed
	if osInfo != nil && shared.IsLoopback(osInfo) {
		n.Type = "loopback"
	} else if dbInfo != nil || shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", n.Name)) {
		if dbInfo != nil {
			n.Managed = true
			n.Config = dbInfo.Config
		}

		n.Type = "bridge"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/device", n.Name)) {
		n.Type = "physical"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bonding", n.Name)) {
		n.Type = "bond"
	} else {
		_, err := exec.Command("ovs-vsctl", "br-exists", n.Name).CombinedOutput()
		if err == nil {
			n.Type = "bridge"
		} else {
			n.Type = "unknown"
		}
	}

	return n, nil
}

func networkDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Get the existing network
	n, err := networkLoadByName(d, name)
	if err != nil {
		return NotFound
	}

	// Attempt to delete the network
	err = n.Delete()
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func networkPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	req := shared.NetworkConfig{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Get the existing network
	n, err := networkLoadByName(d, name)
	if err != nil {
		return NotFound
	}

	// Sanity checks
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	err = networkValidName(req.Name)
	if err != nil {
		return BadRequest(err)
	}

	// Check that the name isn't already in use
	networks, err := networkGetInterfaces(d)
	if err != nil {
		return InternalError(err)
	}

	if shared.StringInSlice(req.Name, networks) {
		return Conflict
	}

	// Rename it
	err = n.Rename(req.Name)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/networks/%s", shared.APIVersion, req.Name))
}

func networkPut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Get the existing network
	_, dbInfo, _ := dbNetworkGet(d.db, name)
	if dbInfo == nil {
		return NotFound
	}

	// Validate the ETag
	etag := []interface{}{dbInfo.Name, dbInfo.Managed, dbInfo.Type, dbInfo.Config}

	err := etagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := shared.NetworkConfig{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	return doNetworkUpdate(d, name, dbInfo.Config, req.Config)
}

func networkPatch(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Get the existing network
	_, dbInfo, _ := dbNetworkGet(d.db, name)
	if dbInfo == nil {
		return NotFound
	}

	// Validate the ETag
	etag := []interface{}{dbInfo.Name, dbInfo.Managed, dbInfo.Type, dbInfo.Config}

	err := etagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := shared.NetworkConfig{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Config stacking
	if req.Config == nil {
		req.Config = map[string]string{}
	}

	for k, v := range dbInfo.Config {
		_, ok := req.Config[k]
		if !ok {
			req.Config[k] = v
		}
	}

	return doNetworkUpdate(d, name, dbInfo.Config, req.Config)
}

func doNetworkUpdate(d *Daemon, name string, oldConfig map[string]string, newConfig map[string]string) Response {
	// Validate the configuration
	err := networkValidateConfig(newConfig)
	if err != nil {
		return BadRequest(err)
	}

	// When switching to a fan bridge, auto-detect the underlay
	if newConfig["bridge.mode"] == "fan" {
		if newConfig["fan.underlay_subnet"] == "" {
			newConfig["fan.underlay_subnet"] = "auto"
		}
	}

	// Load the network
	n, err := networkLoadByName(d, name)
	if err != nil {
		return NotFound
	}

	err = n.Update(shared.NetworkConfig{Config: newConfig})
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

var networkCmd = Command{name: "networks/{name}", get: networkGet, delete: networkDelete, post: networkPost, put: networkPut, patch: networkPatch}

// The network structs and functions
func networkLoadByName(d *Daemon, name string) (*network, error) {
	id, dbInfo, err := dbNetworkGet(d.db, name)
	if err != nil {
		return nil, err
	}

	n := network{d: d, id: id, name: name, config: dbInfo.Config}

	return &n, nil
}

func networkStartup(d *Daemon) error {
	// Get a list of managed networks
	networks, err := dbNetworks(d.db)
	if err != nil {
		return err
	}

	// Bring them all up
	for _, name := range networks {
		n, err := networkLoadByName(d, name)
		if err != nil {
			return err
		}

		err = n.Start()
		if err != nil {
			return err
		}
	}

	return nil
}

type network struct {
	// Properties
	d    *Daemon
	id   int64
	name string

	// config
	config map[string]string
}

func (n *network) Config() map[string]string {
	return n.config
}

func (n *network) IsRunning() bool {
	return shared.PathExists(fmt.Sprintf("/sys/class/net/%s", n.name))
}

func (n *network) IsUsed() bool {
	// Look for containers using the interface
	cts, err := dbContainersList(n.d.db, cTypeRegular)
	if err != nil {
		return true
	}

	for _, ct := range cts {
		c, err := containerLoadByName(n.d, ct)
		if err != nil {
			return true
		}

		if networkIsInUse(c, n.name) {
			return true
		}
	}

	return false
}

func (n *network) Delete() error {
	// Sanity checks
	if n.IsUsed() {
		return fmt.Errorf("The network is currently in use")
	}

	// Bring the network down
	if n.IsRunning() {
		err := n.Stop()
		if err != nil {
			return err
		}
	}

	// Remove the network from the database
	err := dbNetworkDelete(n.d.db, n.name)
	if err != nil {
		return err
	}

	return nil
}

func (n *network) Rename(name string) error {
	// Sanity checks
	if n.IsUsed() {
		return fmt.Errorf("The network is currently in use")
	}

	// Bring the network down
	if n.IsRunning() {
		err := n.Stop()
		if err != nil {
			return err
		}
	}

	// Rename the database entry
	err := dbNetworkRename(n.d.db, n.name, name)
	if err != nil {
		return err
	}

	// Bring the network up
	err = n.Start()
	if err != nil {
		return err
	}

	return nil
}

func (n *network) Start() error {
	if n.IsRunning() {
		return fmt.Errorf("The network is already running")
	}

	return nil
}

func (n *network) Stop() error {
	if !n.IsRunning() {
		return fmt.Errorf("The network is already stopped")
	}

	return nil
}

func (n *network) Update(newNetwork shared.NetworkConfig) error {
	err := networkFillAuto(newNetwork.Config)
	if err != nil {
		return err
	}
	newConfig := newNetwork.Config

	// Backup the current state
	oldConfig := map[string]string{}
	err = shared.DeepCopy(&n.config, &oldConfig)
	if err != nil {
		return err
	}

	// Define a function which reverts everything.  Defer this function
	// so that it doesn't need to be explicitly called in every failing
	// return path.  Track whether or not we want to undo the changes
	// using a closure.
	undoChanges := true
	defer func() {
		if undoChanges {
			n.config = oldConfig
		}
	}()

	// Diff the configurations
	changedConfig := []string{}
	userOnly := true
	for key, _ := range oldConfig {
		if oldConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key, _ := range newConfig {
		if oldConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Skip on no change
	if len(changedConfig) == 0 {
		return nil
	}

	// Update the network
	if !userOnly {
		if shared.StringInSlice("bridge.driver", changedConfig) && n.IsRunning() {
			err = n.Stop()
			if err != nil {
				return err
			}
		}
	}

	// Apply the new configuration
	n.config = newConfig

	// Update the database
	err = dbNetworkUpdate(n.d.db, n.name, n.config)
	if err != nil {
		return err
	}

	// Restart the network
	if !userOnly {
		err = n.Start()
		if err != nil {
			return err
		}
	}

	// Success, update the closure to mark that the changes should be kept.
	undoChanges = false

	return nil
}
