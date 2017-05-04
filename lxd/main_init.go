package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
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
	StorageBackend      string
	StorageCreateDevice string
	StorageCreateLoop   int64
	StoragePool         string
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
	// Figure what storage drivers among the supported ones are actually
	// available on this system.
	availableStoragePoolsDrivers := cmd.availableStoragePoolsDrivers()

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

	err = cmd.runAutoOrInteractive(client, availableStoragePoolsDrivers)

	if err == nil {
		cmd.Context.Output("LXD has been successfully configured.\n")
	}

	return err
}

// Run the logic for auto or interactive mode.
//
// XXX: this logic is going to be refactored into two separate runAuto
// and runInteractive methods, sharing relevant logic with
// runPreseed. The idea being that both runAuto and runInteractive
// will end up populating the same low-level cmdInitData structure
// passed to the common run() method.
func (cmd *CmdInit) runAutoOrInteractive(c lxd.ContainerServer, backendsAvailable []string) error {
	var defaultPrivileged int // controls whether we set security.privileged=true
	var storageBackend string // dir or zfs
	var storageMode string    // existing, loop or device
	var storageLoopSize int64 // Size in GB
	var storageDevice string  // Path
	var storagePool string    // pool name
	var networkAddress string // Address
	var networkPort int64     // Port
	var trustPassword string  // Trust password

	// Detect userns
	defaultPrivileged = -1
	runningInUserns = cmd.RunningInUserns

	// Check that we have no containers or images in the store
	containers, err := c.GetContainerNames()
	if err != nil {
		return fmt.Errorf("Unable to list the LXD containers: %s", err)
	}

	images, err := c.GetImageFingerprints()
	if err != nil {
		return fmt.Errorf("Unable to list the LXD images: %s", err)
	}

	if len(containers) > 0 || len(images) > 0 {
		return fmt.Errorf("You have existing containers or images. lxd init requires an empty LXD.")
	}

	if cmd.Args.Auto {
		if cmd.Args.StorageBackend == "" {
			cmd.Args.StorageBackend = "dir"
		}

		err = cmd.validateArgsAuto(backendsAvailable)
		if err != nil {
			return err
		}

		// Set the local variables
		if cmd.Args.StorageCreateDevice != "" {
			storageMode = "device"
		} else if cmd.Args.StorageCreateLoop != -1 {
			storageMode = "loop"
		} else {
			storageMode = "existing"
		}

		storageBackend = cmd.Args.StorageBackend
		storageLoopSize = cmd.Args.StorageCreateLoop
		storageDevice = cmd.Args.StorageCreateDevice
		storagePool = cmd.Args.StoragePool
		networkAddress = cmd.Args.NetworkAddress
		networkPort = cmd.Args.NetworkPort
		trustPassword = cmd.Args.TrustPassword
	} else {
		defaultStorage := "dir"
		if shared.StringInSlice("zfs", backendsAvailable) {
			defaultStorage = "zfs"
		}

		storageBackend = cmd.Context.AskChoice(fmt.Sprintf("Name of the storage backend to use (dir or zfs) [default=%s]: ", defaultStorage), supportedStoragePoolDrivers, defaultStorage)

		if !shared.StringInSlice(storageBackend, supportedStoragePoolDrivers) {
			return fmt.Errorf("The requested backend '%s' isn't supported by lxd init.", storageBackend)
		}

		if !shared.StringInSlice(storageBackend, backendsAvailable) {
			return fmt.Errorf("The requested backend '%s' isn't available on your system (missing tools).", storageBackend)
		}

		if storageBackend == "zfs" {
			if cmd.Context.AskBool("Create a new ZFS pool (yes/no) [default=yes]? ", "yes") {
				storagePool = cmd.Context.AskString("Name of the new ZFS pool [default=lxd]: ", "lxd", nil)
				if cmd.Context.AskBool("Would you like to use an existing block device (yes/no) [default=no]? ", "no") {
					deviceExists := func(path string) error {
						if !shared.IsBlockdevPath(path) {
							return fmt.Errorf("'%s' is not a block device", path)
						}
						return nil
					}
					storageDevice = cmd.Context.AskString("Path to the existing block device: ", "", deviceExists)
					storageMode = "device"
				} else {
					st := syscall.Statfs_t{}
					err := syscall.Statfs(shared.VarPath(), &st)
					if err != nil {
						return fmt.Errorf("couldn't statfs %s: %s", shared.VarPath(), err)
					}

					/* choose 15 GB < x < 100GB, where x is 20% of the disk size */
					def := uint64(st.Frsize) * st.Blocks / (1024 * 1024 * 1024) / 5
					if def > 100 {
						def = 100
					}
					if def < 15 {
						def = 15
					}

					q := fmt.Sprintf("Size in GB of the new loop device (1GB minimum) [default=%d]: ", def)
					storageLoopSize = cmd.Context.AskInt(q, 1, -1, fmt.Sprintf("%d", def))
					storageMode = "loop"
				}
			} else {
				storagePool = cmd.Context.AskString("Name of the existing ZFS pool or dataset: ", "", nil)
				storageMode = "existing"
			}
		}

		// Detect lack of uid/gid
		needPrivileged := false
		idmapset, err := shared.DefaultIdmapSet()
		if err != nil || len(idmapset.Idmap) == 0 || idmapset.Usable() != nil {
			needPrivileged = true
		}

		if runningInUserns && needPrivileged {
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

		if cmd.Context.AskBool("Would you like LXD to be available over the network (yes/no) [default=no]? ", "no") {
			isIPAddress := func(s string) error {
				if s != "all" && net.ParseIP(s) == nil {
					return fmt.Errorf("'%s' is not an IP address", s)
				}
				return nil
			}

			networkAddress = cmd.Context.AskString("Address to bind LXD to (not including port) [default=all]: ", "all", isIPAddress)
			if networkAddress == "all" {
				networkAddress = "::"
			}

			if net.ParseIP(networkAddress).To4() == nil {
				networkAddress = fmt.Sprintf("[%s]", networkAddress)
			}
			networkPort = cmd.Context.AskInt("Port to bind LXD to [default=8443]: ", 1, 65535, "8443")
			trustPassword = cmd.Context.AskPassword("Trust password for new clients: ", cmd.PasswordReader)
		}
	}

	// Destroy any existing loop device
	for _, file := range []string{"zfs.img"} {
		os.Remove(shared.VarPath(file))
	}

	server, _, err := c.GetServer()
	if err != nil {
		return err
	}

	data := &cmdInitData{}
	data.ServerPut = server.Writable()

	// If there's a default profile, and certain conditions are
	// met we'll update its root disk device and/or eth0 network
	// device, as well as its privileged mode (see below).
	defaultProfile, _, getDefaultProfileErr := c.GetProfile("default")

	if storageBackend == "zfs" {
		if storageMode == "loop" {
			storageDevice = shared.VarPath("zfs.img")
			f, err := os.Create(storageDevice)
			if err != nil {
				return fmt.Errorf("Failed to open %s: %s", storageDevice, err)
			}

			err = f.Chmod(0600)
			if err != nil {
				return fmt.Errorf("Failed to chmod %s: %s", storageDevice, err)
			}

			err = f.Truncate(int64(storageLoopSize * 1024 * 1024 * 1024))
			if err != nil {
				return fmt.Errorf("Failed to create sparse file %s: %s", storageDevice, err)
			}

			err = f.Close()
			if err != nil {
				return fmt.Errorf("Failed to close %s: %s", storageDevice, err)
			}
		}

		if shared.StringInSlice(storageMode, []string{"loop", "device"}) {
			output, err := shared.RunCommand(
				"zpool",
				"create", storagePool, storageDevice,
				"-f", "-m", "none", "-O", "compression=on")
			if err != nil {
				return fmt.Errorf("Failed to create the ZFS pool: %s", output)
			}
		}

		data.Config["storage.zfs_pool_name"] = storagePool

	}

	if defaultPrivileged != -1 && getDefaultProfileErr != nil {
		return getDefaultProfileErr
	} else if defaultPrivileged == 0 {
		defaultProfile.Config["security.privileged"] = ""
	} else if defaultPrivileged == 1 {
		defaultProfile.Config["security.privileged"] = "true"
	}

	if networkAddress != "" {
		if networkPort == -1 {
			networkPort = 8443
		}

		data.Config["core.https_address"] = fmt.Sprintf("%s:%d", networkAddress, networkPort)
		if trustPassword != "" {
			data.Config["core.trust_password"] = trustPassword
		}
	}

	if getDefaultProfileErr == nil {
		// Copy the default profile configuration (that we have
		// possibly modified above).
		data.Profiles = []api.ProfilesPost{{Name: "default"}}
		data.Profiles[0].ProfilePut = defaultProfile.ProfilePut
	}

	err = cmd.run(c, data)
	if err != nil {
		return nil
	}

	return nil
}

// Apply the configuration specified in the given init data.
func (cmd *CmdInit) run(client lxd.ContainerServer, data *cmdInitData) error {
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

	// Updating the server was sucessful, so return the reverter function
	// in case it's needed later.
	return reverter, nil
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
	if !cmd.Args.Auto {
		if cmd.Args.StorageBackend != "" || cmd.Args.StorageCreateDevice != "" || cmd.Args.StorageCreateLoop != -1 || cmd.Args.StoragePool != "" || cmd.Args.NetworkAddress != "" || cmd.Args.NetworkPort != -1 || cmd.Args.TrustPassword != "" {
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
		if cmd.Args.StorageCreateLoop != -1 || cmd.Args.StorageCreateDevice != "" || cmd.Args.StoragePool != "" {
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

	// Detect zfs
	out, err := exec.LookPath("zfs")
	if err == nil && len(out) != 0 && !cmd.RunningInUserns {
		_ = loadModule("zfs")

		_, err := shared.RunCommand("zpool", "list")
		if err == nil {
			drivers = append(drivers, "zfs")
		}
	}
	return drivers
}

// Return an error if the given profile has already a device with the
// given name.
func (cmd *CmdInit) profileDeviceAlreadyExists(profile *api.Profile, deviceName string) error {
	_, ok := profile.Devices[deviceName]
	if ok {
		return fmt.Errorf("Device already exists: %s", deviceName)
	}
	return nil
}

// Defines the schema for all possible configuration knobs supported by the
// lxd init command, either directly fed via --preseed or populated by
// the auto/interactive modes.
type cmdInitData struct {
	api.ServerPut `yaml:",inline"`
	Profiles      []api.ProfilesPost
}

// Shortcut for closure/anonymous functions that are meant to revert
// some change, and that are passed around as parameters.
type reverter func() error

func cmdInit() error {
	context := cmd.NewContext(os.Stdin, os.Stdout, os.Stderr)
	args := &CmdInitArgs{
		Auto:                *argAuto,
		StorageBackend:      *argStorageBackend,
		StorageCreateDevice: *argStorageCreateDevice,
		StorageCreateLoop:   *argStorageCreateLoop,
		StoragePool:         *argStoragePool,
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

var supportedStoragePoolDrivers = []string{"dir", "zfs"}
