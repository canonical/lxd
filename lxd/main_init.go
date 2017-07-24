package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh/terminal"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/logger"
)

// CmdInitArgs holds command line arguments for the "lxd init" command.
type CmdInitArgs struct {
	Auto                bool
	Preseed             bool
	StorageBackend      string
	StorageCreateDevice string
	StorageCreateLoop   int64
	StorageDataset      string
	NetworkPort         int64
	NetworkAddress      string
	TrustPassword       string
}

// CmdInit implements the "lxd init" command line.
type CmdInit struct {
	Context         *cmd.Context
	Args            *CmdInitArgs
	RunningInUserns bool
	SocketPath      string
	PasswordReader  func(int) ([]byte, error)
}

// Run triggers the execution of the init command.
func (cmd *CmdInit) Run() error {
	// Check that command line arguments don't conflict with each other
	err := cmd.validateArgs()
	if err != nil {
		return err
	}

	// Connect to LXD
	client, err := lxd.ConnectLXDUnix(cmd.SocketPath, nil)
	if err != nil {
		return fmt.Errorf("Unable to talk to LXD: %s", err)
	}

	existingPools, err := client.GetStoragePoolNames()
	if err != nil {
		// We should consider this fatal since this means
		// something's wrong with the daemon.
		return err
	}

	data := &cmdInitData{}

	// Kick off the appropriate way to fill the data (either
	// preseed, auto or interactive).
	if cmd.Args.Preseed {
		err = cmd.fillDataPreseed(data, client)
	} else {
		// Copy the data from the current default profile, if it exists.
		cmd.fillDataWithCurrentServerConfig(data, client)

		// Copy the data from the current server config.
		cmd.fillDataWithCurrentDefaultProfile(data, client)

		// Figure what storage drivers among the supported ones are actually
		// available on this system.
		backendsAvailable := cmd.availableStoragePoolsDrivers()

		if cmd.Args.Auto {
			err = cmd.fillDataAuto(data, client, backendsAvailable, existingPools)
		} else {
			err = cmd.fillDataInteractive(data, client, backendsAvailable, existingPools)
		}
	}
	if err != nil {
		return err
	}

	// Apply the desired configuration.
	err = cmd.apply(client, data)
	if err != nil {
		return err
	}

	cmd.Context.Output("LXD has been successfully configured.\n")

	return nil
}

// Fill the given configuration data with parameters collected from
// the --auto command line.
func (cmd *CmdInit) fillDataAuto(data *cmdInitData, client lxd.ContainerServer, backendsAvailable []string, existingPools []string) error {
	if cmd.Args.StorageBackend == "" {
		cmd.Args.StorageBackend = "dir"
	}
	err := cmd.validateArgsAuto(backendsAvailable)
	if err != nil {
		return err
	}

	if cmd.Args.NetworkAddress != "" {
		// If no port was provided, use the default one
		if cmd.Args.NetworkPort == -1 {
			cmd.Args.NetworkPort = 8443
		}
		networking := &cmdInitNetworkingParams{
			Address:       cmd.Args.NetworkAddress,
			Port:          cmd.Args.NetworkPort,
			TrustPassword: cmd.Args.TrustPassword,
		}
		cmd.fillDataWithNetworking(data, networking)
	}

	if len(existingPools) == 0 {
		storage := &cmdInitStorageParams{
			Backend:  cmd.Args.StorageBackend,
			LoopSize: cmd.Args.StorageCreateLoop,
			Device:   cmd.Args.StorageCreateDevice,
			Dataset:  cmd.Args.StorageDataset,
			Pool:     "default",
		}
		err = cmd.fillDataWithStorage(data, storage, existingPools)
		if err != nil {
			return err
		}
	}
	return nil
}

// Fill the given configuration data with parameters collected with
// interactive questions.
func (cmd *CmdInit) fillDataInteractive(data *cmdInitData, client lxd.ContainerServer, backendsAvailable []string, existingPools []string) error {
	storage, err := cmd.askStorage(client, existingPools, backendsAvailable)
	if err != nil {
		return err
	}
	defaultPrivileged := cmd.askDefaultPrivileged()
	networking := cmd.askNetworking()
	imagesAutoUpdate := cmd.askImages()
	bridge := cmd.askBridge(client)

	err = cmd.fillDataWithStorage(data, storage, existingPools)
	if err != nil {
		return err
	}

	err = cmd.fillDataWithDefaultPrivileged(data, defaultPrivileged)
	if err != nil {
		return err
	}

	cmd.fillDataWithNetworking(data, networking)

	cmd.fillDataWithImages(data, imagesAutoUpdate)

	err = cmd.fillDataWithBridge(data, bridge)
	if err != nil {
		return err
	}

	return nil
}

// Fill the given configuration data from the preseed YAML text stream.
func (cmd *CmdInit) fillDataPreseed(data *cmdInitData, client lxd.ContainerServer) error {
	err := cmd.Context.InputYAML(data)
	if err != nil {
		return fmt.Errorf("Invalid preseed YAML content")
	}

	return nil
}

// Fill the given data with the current server configuration.
func (cmd *CmdInit) fillDataWithCurrentServerConfig(data *cmdInitData, client lxd.ContainerServer) error {
	server, _, err := client.GetServer()
	if err != nil {
		return err
	}
	data.ServerPut = server.Writable()
	return nil
}

// Fill the given data with the current default profile, if it exists.
func (cmd *CmdInit) fillDataWithCurrentDefaultProfile(data *cmdInitData, client lxd.ContainerServer) {
	defaultProfile, _, err := client.GetProfile("default")
	if err == nil {
		// Copy the default profile configuration (that we have
		// possibly modified above).
		data.Profiles = []api.ProfilesPost{{Name: "default"}}
		data.Profiles[0].ProfilePut = defaultProfile.ProfilePut
	}
}

// Fill the given init data with a new storage pool structure matching the
// given storage parameters.
func (cmd *CmdInit) fillDataWithStorage(data *cmdInitData, storage *cmdInitStorageParams, existingPools []string) error {
	if storage == nil {
		return nil
	}

	// Pool configuration
	storagePoolConfig := map[string]string{}
	if storage.Config != nil {
		storagePoolConfig = storage.Config
	}

	if storage.Device != "" {
		storagePoolConfig["source"] = storage.Device
		if storage.Dataset != "" {
			storage.Pool = storage.Dataset
		}
	} else if storage.LoopSize != -1 {
		if storage.Dataset != "" {
			storage.Pool = storage.Dataset
		}
	} else {
		storagePoolConfig["source"] = storage.Dataset
	}

	if storage.LoopSize > 0 {
		storagePoolConfig["size"] = strconv.FormatInt(storage.LoopSize, 10) + "GB"
	}

	// Create the requested storage pool.
	storageStruct := api.StoragePoolsPost{
		Name:   storage.Pool,
		Driver: storage.Backend,
	}
	storageStruct.Config = storagePoolConfig

	data.Pools = []api.StoragePoolsPost{storageStruct}

	// When lxd init is rerun and there are already storage pools
	// configured, do not try to set a root disk device in the
	// default profile again. Let the user figure this out.
	if len(existingPools) == 0 {
		if len(data.Profiles) != 0 {
			defaultProfile := data.Profiles[0]
			foundRootDiskDevice := false
			for k, v := range defaultProfile.Devices {
				if v["path"] == "/" && v["source"] == "" {
					foundRootDiskDevice = true

					// Unconditionally overwrite because if the user ends up
					// with a clean LXD but with a pool property key existing in
					// the default profile it must be empty otherwise it would
					// not have been possible to delete the storage pool in
					// the first place.
					defaultProfile.Devices[k]["pool"] = storage.Pool
					logger.Debugf("Set pool property of existing root disk device \"%s\" in profile \"default\" to \"%s\".", storage.Pool)

					break
				}
			}

			if !foundRootDiskDevice {
				err := cmd.profileDeviceAlreadyExists(&defaultProfile, "root")
				if err != nil {
					return err
				}

				defaultProfile.Devices["root"] = map[string]string{
					"type": "disk",
					"path": "/",
					"pool": storage.Pool,
				}
			}
		} else {
			logger.Warnf("Did not find profile \"default\" so no default storage pool will be set. Manual intervention needed.")
		}
	}

	return nil
}

// Fill the default profile in the given init data with options about whether
// to run in privileged mode.
func (cmd *CmdInit) fillDataWithDefaultPrivileged(data *cmdInitData, defaultPrivileged int) error {
	if defaultPrivileged == -1 {
		return nil
	}
	if len(data.Profiles) == 0 {
		return fmt.Errorf("error: profile 'default' profile not found")
	}
	defaultProfile := data.Profiles[0]
	if defaultPrivileged == 0 {
		defaultProfile.Config["security.privileged"] = ""
	} else if defaultPrivileged == 1 {
		defaultProfile.Config["security.privileged"] = "true"
	}
	return nil
}

// Fill the given init data with server config details matching the
// given networking parameters.
func (cmd *CmdInit) fillDataWithNetworking(data *cmdInitData, networking *cmdInitNetworkingParams) {
	if networking == nil {
		return
	}
	data.Config["core.https_address"] = fmt.Sprintf("%s:%d", networking.Address, networking.Port)
	if networking.TrustPassword != "" {
		data.Config["core.trust_password"] = networking.TrustPassword
	}
}

// Fill the given init data with server config details matching the
// given images auto update choice.
func (cmd *CmdInit) fillDataWithImages(data *cmdInitData, imagesAutoUpdate bool) {
	if imagesAutoUpdate {
		if val, ok := data.Config["images.auto_update_interval"]; ok && val == "0" {
			data.Config["images.auto_update_interval"] = ""
		}
	} else {
		data.Config["images.auto_update_interval"] = "0"
	}
}

// Fill the given init data with a new bridge network device structure
// matching the given storage parameters.
func (cmd *CmdInit) fillDataWithBridge(data *cmdInitData, bridge *cmdInitBridgeParams) error {
	if bridge == nil {
		return nil
	}

	bridgeConfig := map[string]string{}
	bridgeConfig["ipv4.address"] = bridge.IPv4
	bridgeConfig["ipv6.address"] = bridge.IPv6

	if bridge.IPv4Nat {
		bridgeConfig["ipv4.nat"] = "true"
	}

	if bridge.IPv6Nat {
		bridgeConfig["ipv6.nat"] = "true"
	}

	network := api.NetworksPost{
		Name: bridge.Name}
	network.Config = bridgeConfig
	data.Networks = []api.NetworksPost{network}

	if len(data.Profiles) == 0 {
		return fmt.Errorf("error: profile 'default' profile not found")
	}

	// Attach the bridge as eth0 device of the default profile, if such
	// device doesn't exists yet.
	defaultProfile := data.Profiles[0]
	err := cmd.profileDeviceAlreadyExists(&defaultProfile, "eth0")
	if err != nil {
		return err
	}
	defaultProfile.Devices["eth0"] = map[string]string{
		"type":    "nic",
		"nictype": "bridged",
		"parent":  bridge.Name,
	}

	return nil

}

// Apply the configuration specified in the given init data.
func (cmd *CmdInit) apply(client lxd.ContainerServer, data *cmdInitData) error {
	// Functions that should be invoked to revert back to initial
	// state any change that was successfully applied, in case
	// anything goes wrong after that change.
	reverters := make([]reverter, 0)

	// Functions to apply the desired changes.
	changers := make([](func() (reverter, error)), 0)

	// Server config changer
	changers = append(changers, func() (reverter, error) {
		return cmd.initConfig(client, data.Config)
	})

	// Storage pool changers
	for i := range data.Pools {
		pool := data.Pools[i] // Local variable for the closure
		changers = append(changers, func() (reverter, error) {
			return cmd.initPool(client, pool)
		})
	}

	// Network changers
	for i := range data.Networks {
		network := data.Networks[i] // Local variable for the closure
		changers = append(changers, func() (reverter, error) {
			return cmd.initNetwork(client, network)
		})
	}

	// Profile changers
	for i := range data.Profiles {
		profile := data.Profiles[i] // Local variable for the closure
		changers = append(changers, func() (reverter, error) {
			return cmd.initProfile(client, profile)
		})
	}

	// Apply all changes. If anything goes wrong at any iteration
	// of the loop, we'll try to revert any change performed in
	// earlier iterations.
	for _, changer := range changers {
		reverter, err := changer()
		if err != nil {
			cmd.revert(reverters)
			return err
		}
		// Save the revert function for later.
		reverters = append(reverters, reverter)
	}

	return nil
}

// Try to revert the state to what it was before running the "lxd init" command.
func (cmd *CmdInit) revert(reverters []reverter) {
	for _, reverter := range reverters {
		err := reverter()
		if err != nil {
			logger.Warnf("Reverting to pre-init state failed: %s", err)
			break
		}
	}
}

// Apply the server-level configuration in the given map.
func (cmd *CmdInit) initConfig(client lxd.ContainerServer, config map[string]interface{}) (reverter, error) {
	server, etag, err := client.GetServer()
	if err != nil {
		return nil, err
	}

	// Build a function that can be used to revert the config to
	// its original values.
	reverter := func() error {
		return client.UpdateServer(server.Writable(), "")
	}

	// The underlying code expects all values to be string, even if when
	// using preseed the yaml.v2 package unmarshals them as integers.
	for key, value := range config {
		if number, ok := value.(int); ok {
			value = strconv.Itoa(number)
		}
		config[key] = value
	}

	err = client.UpdateServer(api.ServerPut{Config: config}, etag)
	if err != nil {
		return nil, err
	}

	// Updating the server was successful, so return the reverter function
	// in case it's needed later.
	return reverter, nil
}

// Create or update a single pool, and return a revert function in case of success.
func (cmd *CmdInit) initPool(client lxd.ContainerServer, pool api.StoragePoolsPost) (reverter, error) {
	var reverter func() error
	currentPool, _, err := client.GetStoragePool(pool.Name)
	if err == nil {
		reverter, err = cmd.initPoolUpdate(client, pool, currentPool.Writable())
	} else {
		reverter, err = cmd.initPoolCreate(client, pool)
	}
	if err != nil {
		return nil, err
	}
	return reverter, nil
}

// Create a single new pool, and return a revert function to delete it.
func (cmd *CmdInit) initPoolCreate(client lxd.ContainerServer, pool api.StoragePoolsPost) (reverter, error) {
	reverter := func() error {
		return client.DeleteStoragePool(pool.Name)
	}
	err := client.CreateStoragePool(pool)
	return reverter, err
}

// Update a single pool, and return a function that can be used to
// revert it to its original state.
func (cmd *CmdInit) initPoolUpdate(client lxd.ContainerServer, pool api.StoragePoolsPost, currentPool api.StoragePoolPut) (reverter, error) {
	reverter := func() error {
		return client.UpdateStoragePool(pool.Name, currentPool, "")
	}
	err := client.UpdateStoragePool(pool.Name, api.StoragePoolPut{
		Config: pool.Config,
	}, "")
	return reverter, err
}

// Create or update a single network, and return a revert function in case of success.
func (cmd *CmdInit) initNetwork(client lxd.ContainerServer, network api.NetworksPost) (reverter, error) {
	var revert func() error
	currentNetwork, _, err := client.GetNetwork(network.Name)
	if err == nil {
		// Sanity check, make sure the network type being updated
		// is still "bridge", which is the only type the existing
		// network can have.
		if network.Type != "" && network.Type != "bridge" {
			return nil, fmt.Errorf("Only 'bridge' type networks are supported")
		}
		revert, err = cmd.initNetworkUpdate(client, network, currentNetwork.Writable())
	} else {
		revert, err = cmd.initNetworkCreate(client, network)
	}
	if err != nil {
		return nil, err
	}
	return revert, nil
}

// Create a single new network, and return a revert function to delete it.
func (cmd *CmdInit) initNetworkCreate(client lxd.ContainerServer, network api.NetworksPost) (reverter, error) {
	reverter := func() error {
		return client.DeleteNetwork(network.Name)
	}
	err := client.CreateNetwork(network)
	return reverter, err
}

// Update a single network, and return a function that can be used to
// revert it to its original state.
func (cmd *CmdInit) initNetworkUpdate(client lxd.ContainerServer, network api.NetworksPost, currentNetwork api.NetworkPut) (reverter, error) {
	reverter := func() error {
		return client.UpdateNetwork(network.Name, currentNetwork, "")
	}
	err := client.UpdateNetwork(network.Name, api.NetworkPut{
		Config: network.Config,
	}, "")
	return reverter, err
}

// Create or update a single profile, and return a revert function in case of success.
func (cmd *CmdInit) initProfile(client lxd.ContainerServer, profile api.ProfilesPost) (reverter, error) {
	var reverter func() error
	currentProfile, _, err := client.GetProfile(profile.Name)
	if err == nil {
		reverter, err = cmd.initProfileUpdate(client, profile, currentProfile.Writable())
	} else {
		reverter, err = cmd.initProfileCreate(client, profile)
	}
	if err != nil {
		return nil, err
	}
	return reverter, nil
}

// Create a single new profile, and return a revert function to delete it.
func (cmd *CmdInit) initProfileCreate(client lxd.ContainerServer, profile api.ProfilesPost) (reverter, error) {
	reverter := func() error {
		return client.DeleteProfile(profile.Name)
	}
	err := client.CreateProfile(profile)
	return reverter, err
}

// Update a single profile, and return a function that can be used to
// revert it to its original state.
func (cmd *CmdInit) initProfileUpdate(client lxd.ContainerServer, profile api.ProfilesPost, currentProfile api.ProfilePut) (reverter, error) {
	reverter := func() error {
		return client.UpdateProfile(profile.Name, currentProfile, "")
	}
	err := client.UpdateProfile(profile.Name, api.ProfilePut{
		Config:      profile.Config,
		Description: profile.Description,
		Devices:     profile.Devices,
	}, "")
	return reverter, err
}

// Check that the arguments passed via command line are consistent,
// and no invalid combination is provided.
func (cmd *CmdInit) validateArgs() error {
	if cmd.Args.Auto && cmd.Args.Preseed {
		return fmt.Errorf("Non-interactive mode supported by only one of --auto or --preseed")
	}
	if !cmd.Args.Auto {
		if cmd.Args.StorageBackend != "" || cmd.Args.StorageCreateDevice != "" || cmd.Args.StorageCreateLoop != -1 || cmd.Args.StorageDataset != "" || cmd.Args.NetworkAddress != "" || cmd.Args.NetworkPort != -1 || cmd.Args.TrustPassword != "" {
			return fmt.Errorf("Init configuration is only valid with --auto")
		}
	}
	return nil
}

// Check that the arguments passed along with --auto are valid and consistent.
// and no invalid combination is provided.
func (cmd *CmdInit) validateArgsAuto(availableStoragePoolsDrivers []string) error {
	if !shared.StringInSlice(cmd.Args.StorageBackend, supportedStoragePoolDrivers) {
		return fmt.Errorf("The requested backend '%s' isn't supported by lxd init.", cmd.Args.StorageBackend)
	}
	if !shared.StringInSlice(cmd.Args.StorageBackend, availableStoragePoolsDrivers) {
		return fmt.Errorf("The requested backend '%s' isn't available on your system (missing tools).", cmd.Args.StorageBackend)
	}

	if cmd.Args.StorageBackend == "dir" {
		if cmd.Args.StorageCreateLoop != -1 || cmd.Args.StorageCreateDevice != "" || cmd.Args.StorageDataset != "" {
			return fmt.Errorf("None of --storage-pool, --storage-create-device or --storage-create-loop may be used with the 'dir' backend.")
		}
	} else {
		if cmd.Args.StorageCreateLoop != -1 && cmd.Args.StorageCreateDevice != "" {
			return fmt.Errorf("Only one of --storage-create-device or --storage-create-loop can be specified.")
		}
	}

	if cmd.Args.NetworkAddress == "" {
		if cmd.Args.NetworkPort != -1 {
			return fmt.Errorf("--network-port cannot be used without --network-address.")
		}
		if cmd.Args.TrustPassword != "" {
			return fmt.Errorf("--trust-password cannot be used without --network-address.")
		}
	}

	return nil
}

// Return the available storage pools drivers (depending on installed tools).
func (cmd *CmdInit) availableStoragePoolsDrivers() []string {
	drivers := []string{"dir"}

	// Check available backends
	for _, driver := range supportedStoragePoolDrivers {
		if driver == "dir" {
			continue
		}

		// btrfs can work in user namespaces too. (If
		// source=/some/path/on/btrfs is used.)
		if cmd.RunningInUserns && driver != "btrfs" {
			continue
		}

		// Initialize a core storage interface for the given driver.
		_, err := storageCoreInit(driver)
		if err != nil {
			continue
		}

		drivers = append(drivers, driver)
	}
	return drivers
}

// Return an error if the given profile has already a device with the
// given name.
func (cmd *CmdInit) profileDeviceAlreadyExists(profile *api.ProfilesPost, deviceName string) error {
	_, ok := profile.Devices[deviceName]
	if ok {
		return fmt.Errorf("Device already exists: %s", deviceName)
	}
	return nil
}

// Ask if the user wants to create a new storage pool, and return
// the relevant parameters if so.
func (cmd *CmdInit) askStorage(client lxd.ContainerServer, existingPools []string, availableBackends []string) (*cmdInitStorageParams, error) {
	if !cmd.Context.AskBool("Do you want to configure a new storage pool (yes/no) [default=yes]? ", "yes") {
		return nil, nil
	}
	storage := &cmdInitStorageParams{
		Config: map[string]string{},
	}

	defaultStorage := "dir"
	if shared.StringInSlice("zfs", availableBackends) {
		defaultStorage = "zfs"
	}
	for {
		storage.Pool = cmd.Context.AskString("Name of the new storage pool [default=default]: ", "default", nil)
		if shared.StringInSlice(storage.Pool, existingPools) {
			fmt.Printf("The requested storage pool \"%s\" already exists. Please choose another name.\n", storage.Pool)
			// Ask the user again if hew wants to create a
			// storage pool.
			continue
		}

		storage.Backend = cmd.Context.AskChoice(fmt.Sprintf("Name of the storage backend to use (%s) [default=%s]: ", strings.Join(availableBackends, ", "), defaultStorage), supportedStoragePoolDrivers, defaultStorage)

		// XXX The following to checks don't make much sense, since
		// AskChoice will always re-ask the question if the answer
		// is not among supportedStoragePoolDrivers. It seems legacy
		// code that we should drop?
		if !shared.StringInSlice(storage.Backend, supportedStoragePoolDrivers) {
			return nil, fmt.Errorf("The requested backend '%s' isn't supported by lxd init.", storage.Backend)
		}

		// XXX Instead of manually checking if the provided choice is
		// among availableBackends, we could just pass to askChoice the
		// availableBackends list instead of supportedStoragePoolDrivers.
		if !shared.StringInSlice(storage.Backend, availableBackends) {
			return nil, fmt.Errorf("The requested backend '%s' isn't available on your system (missing tools).", storage.Backend)
		}

		if storage.Backend == "dir" {
			break
		}

		storage.LoopSize = -1
		question := fmt.Sprintf("Create a new %s pool (yes/no) [default=yes]? ", strings.ToUpper(storage.Backend))
		if cmd.Context.AskBool(question, "yes") {
			if storage.Backend == "ceph" {
				// Pool configuration
				if storage.Config != nil {
					storage.Config = map[string]string{}
				}

				// ask for the name of the cluster
				storage.Config["ceph.cluster_name"] = cmd.Context.AskString("Name of the existing CEPH cluster [default=ceph]: ", "ceph", nil)

				// ask for the name of the osd pool
				storage.Config["ceph.osd.pool_name"] = cmd.Context.AskString("Name of the OSD storage pool [default=lxd]: ", "lxd", nil)

				// ask for the number of placement groups
				storage.Config["ceph.osd.pg_num"] = cmd.Context.AskString("Number of placement groups [default=32]: ", "32", nil)
			} else if cmd.Context.AskBool("Would you like to use an existing block device (yes/no) [default=no]? ", "no") {
				deviceExists := func(path string) error {
					if !shared.IsBlockdevPath(path) {
						return fmt.Errorf("'%s' is not a block device", path)
					}
					return nil
				}
				storage.Device = cmd.Context.AskString("Path to the existing block device: ", "", deviceExists)
			} else {
				backingFs, err := filesystemDetect(shared.VarPath())
				if err == nil && storage.Backend == "btrfs" && backingFs == "btrfs" {
					if cmd.Context.AskBool("Would you like to create a new subvolume for the BTRFS storage pool (yes/no) [default=yes]: ", "yes") {
						storage.Dataset = shared.VarPath("storage-pools", storage.Pool)
					}
				} else {
					st := syscall.Statfs_t{}
					err := syscall.Statfs(shared.VarPath(), &st)
					if err != nil {
						return nil, fmt.Errorf("couldn't statfs %s: %s", shared.VarPath(), err)
					}

					/* choose 15 GB < x < 100GB, where x is 20% of the disk size */
					defaultSize := uint64(st.Frsize) * st.Blocks / (1024 * 1024 * 1024) / 5
					if defaultSize > 100 {
						defaultSize = 100
					}
					if defaultSize < 15 {
						defaultSize = 15
					}

					question := fmt.Sprintf("Size in GB of the new loop device (1GB minimum) [default=%dGB]: ", defaultSize)
					storage.LoopSize = cmd.Context.AskInt(question, 1, -1, fmt.Sprintf("%d", defaultSize))
				}
			}
		} else {
			if storage.Backend == "ceph" {
				// Pool configuration
				if storage.Config != nil {
					storage.Config = map[string]string{}
				}

				// ask for the name of the cluster
				storage.Config["ceph.cluster_name"] = cmd.Context.AskString("Name of the existing CEPH cluster [default=ceph]: ", "ceph", nil)

				// ask for the name of the existing pool
				storage.Config["source"] = cmd.Context.AskString("Name of the existing OSD storage pool [default=lxd]: ", "lxd", nil)
				storage.Config["ceph.osd.pool_name"] = storage.Config["source"]
			} else {
				question := fmt.Sprintf("Name of the existing %s pool or dataset: ", strings.ToUpper(storage.Backend))
				storage.Dataset = cmd.Context.AskString(question, "", nil)
			}
		}

		if storage.Backend == "lvm" {
			_, err := exec.LookPath("thin_check")
			if err != nil {
				fmt.Printf(`
The LVM thin provisioning tools couldn't be found. LVM can still be used
without thin provisioning but this will disable over-provisioning,
increase the space requirements and creation time of images, containers
and snapshots.

If you wish to use thin provisioning, abort now, install the tools from
your Linux distribution and run "lxd init" again afterwards.

`)
				if !cmd.Context.AskBool("Do you want to continue without thin provisioning? (yes/no) [default=yes]: ", "yes") {
					return nil, fmt.Errorf("The LVM thin provisioning tools couldn't be found on the system.")
				}

				storage.Config["lvm.use_thinpool"] = "false"
			}
		}

		break
	}
	return storage, nil
}

// If we detect that we are running inside an unprivileged container,
// ask if the user wants to the default profile to be a privileged
// one.
func (cmd *CmdInit) askDefaultPrivileged() int {
	// Detect lack of uid/gid
	defaultPrivileged := -1
	needPrivileged := false
	idmapset, err := shared.DefaultIdmapSet()
	if err != nil || len(idmapset.Idmap) == 0 || idmapset.Usable() != nil {
		needPrivileged = true
	}

	if cmd.RunningInUserns && needPrivileged {
		fmt.Printf(`
We detected that you are running inside an unprivileged container.
This means that unless you manually configured your host otherwise,
you will not have enough uid and gid to allocate to your containers.

LXD can re-use your container's own allocation to avoid the problem.
Doing so makes your nested containers slightly less safe as they could
in theory attack their parent container and gain more privileges than
they otherwise would.

`)

		if cmd.Context.AskBool("Would you like to have your containers share their parent's allocation (yes/no) [default=yes]? ", "yes") {
			defaultPrivileged = 1
		} else {
			defaultPrivileged = 0
		}
	}
	return defaultPrivileged
}

// Ask if the user wants to expose LXD over the network, and collect
// the relevant parameters if so.
func (cmd *CmdInit) askNetworking() *cmdInitNetworkingParams {
	if !cmd.Context.AskBool("Would you like LXD to be available over the network (yes/no) [default=no]? ", "no") {
		return nil
	}
	networking := &cmdInitNetworkingParams{}

	isIPAddress := func(s string) error {
		if s != "all" && net.ParseIP(s) == nil {
			return fmt.Errorf("'%s' is not an IP address", s)
		}
		return nil
	}

	networking.Address = cmd.Context.AskString("Address to bind LXD to (not including port) [default=all]: ", "all", isIPAddress)
	if networking.Address == "all" {
		networking.Address = "::"
	}

	if net.ParseIP(networking.Address).To4() == nil {
		networking.Address = fmt.Sprintf("[%s]", networking.Address)
	}
	networking.Port = cmd.Context.AskInt("Port to bind LXD to [default=8443]: ", 1, 65535, "8443")
	networking.TrustPassword = cmd.Context.AskPassword("Trust password for new clients: ", cmd.PasswordReader)

	return networking
}

// Ask if the user wants images to be automatically refreshed.
func (cmd *CmdInit) askImages() bool {
	return cmd.Context.AskBool("Would you like stale cached images to be updated automatically (yes/no) [default=yes]? ", "yes")
}

// Ask if the user wants to create a new network bridge, and return
// the relevant parameters if so.
func (cmd *CmdInit) askBridge(client lxd.ContainerServer) *cmdInitBridgeParams {
	if !cmd.Context.AskBool("Would you like to create a new network bridge (yes/no) [default=yes]? ", "yes") {
		return nil
	}
	bridge := &cmdInitBridgeParams{}
	for {
		bridge.Name = cmd.Context.AskString("What should the new bridge be called [default=lxdbr0]? ", "lxdbr0", networkValidName)
		_, _, err := client.GetNetwork(bridge.Name)
		if err == nil {
			fmt.Printf("The requested network bridge \"%s\" already exists. Please choose another name.\n", bridge.Name)
			// Ask the user again if hew wants to create a
			// storage pool.
			continue
		}
		bridge.IPv4 = cmd.Context.AskString("What IPv4 address should be used (CIDR subnet notation, “auto” or “none”) [default=auto]? ", "auto", func(value string) error {
			if shared.StringInSlice(value, []string{"auto", "none"}) {
				return nil
			}
			return networkValidAddressCIDRV4(value)
		})

		if !shared.StringInSlice(bridge.IPv4, []string{"auto", "none"}) {
			bridge.IPv4Nat = cmd.Context.AskBool("Would you like LXD to NAT IPv4 traffic on your bridge? [default=yes]? ", "yes")
		}

		bridge.IPv6 = cmd.Context.AskString("What IPv6 address should be used (CIDR subnet notation, “auto” or “none”) [default=auto]? ", "auto", func(value string) error {
			if shared.StringInSlice(value, []string{"auto", "none"}) {
				return nil
			}
			return networkValidAddressCIDRV6(value)
		})

		if !shared.StringInSlice(bridge.IPv6, []string{"auto", "none"}) {
			bridge.IPv6Nat = cmd.Context.AskBool("Would you like LXD to NAT IPv6 traffic on your bridge? [default=yes]? ", "yes")
		}
		break
	}
	return bridge
}

// Defines the schema for all possible configuration knobs supported by the
// lxd init command, either directly fed via --preseed or populated by
// the auto/interactive modes.
type cmdInitData struct {
	api.ServerPut `yaml:",inline"`
	Pools         []api.StoragePoolsPost `yaml:"storage_pools"`
	Networks      []api.NetworksPost
	Profiles      []api.ProfilesPost
}

// Parameters needed when creating a storage pool in interactive or auto
// mode.
type cmdInitStorageParams struct {
	Backend  string            // == supportedStoragePoolDrivers
	LoopSize int64             // Size in GB
	Device   string            // Path
	Pool     string            // pool name
	Dataset  string            // existing ZFS pool name
	Config   map[string]string // Additional pool configuration
}

// Parameters needed when configuring the LXD server networking options in interactive
// mode or auto mode.
type cmdInitNetworkingParams struct {
	Address       string // Address
	Port          int64  // Port
	TrustPassword string // Trust password
}

// Parameters needed when creating a bridge network device in interactive
// mode.
type cmdInitBridgeParams struct {
	Name    string // Bridge name
	IPv4    string // IPv4 address
	IPv4Nat bool   // IPv4 address
	IPv6    string // IPv6 address
	IPv6Nat bool   // IPv6 address
}

// Shortcut for closure/anonymous functions that are meant to revert
// some change, and that are passed around as parameters.
type reverter func() error

func cmdInit() error {
	context := cmd.NewContext(os.Stdin, os.Stdout, os.Stderr)
	args := &CmdInitArgs{
		Auto:                *argAuto,
		Preseed:             *argPreseed,
		StorageBackend:      *argStorageBackend,
		StorageCreateDevice: *argStorageCreateDevice,
		StorageCreateLoop:   *argStorageCreateLoop,
		StorageDataset:      *argStorageDataset,
		NetworkPort:         *argNetworkPort,
		NetworkAddress:      *argNetworkAddress,
		TrustPassword:       *argTrustPassword,
	}
	command := &CmdInit{
		Context:         context,
		Args:            args,
		RunningInUserns: shared.RunningInUserNS(),
		SocketPath:      "",
		PasswordReader:  terminal.ReadPassword,
	}
	return command.Run()
}
