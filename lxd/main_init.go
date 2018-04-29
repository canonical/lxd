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
	"github.com/lxc/lxd/lxd/util"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
)

// CmdInit implements the "lxd init" command line.
type CmdInit struct {
	Context         *cmd.Context
	Args            *Args
	RunningInUserns bool
	SocketPath      string
	PasswordReader  func(int) ([]byte, error)
}

// Run triggers the execution of the init command.
func (cmd *CmdInit) Run() error {
	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

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

	// Check that we have no containers or images in the store
	containers, err := client.GetContainerNames()
	if err != nil {
		return fmt.Errorf("Unable to list the LXD containers: %s", err)
	}

	images, err := client.GetImageFingerprints()
	if err != nil {
		return fmt.Errorf("Unable to list the LXD images: %s", err)
	}

	if len(containers) > 0 || len(images) > 0 {
		return fmt.Errorf("You have existing containers or images. lxd init requires an empty LXD.")
	}

	data := &cmdInitData{}

	// Copy the data from the current default profile, if it exists.
	cmd.fillDataWithCurrentServerConfig(data, client)

	// Copy the data from the current server config.
	cmd.fillDataWithCurrentDefaultProfile(data, client)

	// Figure what storage drivers among the supported ones are actually
	// available on this system.
	backendsAvailable := cmd.availableStoragePoolsDrivers()

	if cmd.Args.Auto {
		err = cmd.fillDataAuto(data, client, backendsAvailable)
	} else {
		err = cmd.fillDataInteractive(data, client, backendsAvailable)
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
func (cmd *CmdInit) fillDataAuto(data *cmdInitData, client lxd.ContainerServer, backendsAvailable []string) error {
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

	if cmd.Args.StorageBackend == "zfs" {
		storage := &cmdInitStorageParams{
			Backend:  cmd.Args.StorageBackend,
			LoopSize: cmd.Args.StorageCreateLoop,
			Device:   cmd.Args.StorageCreateDevice,
			Pool:     cmd.Args.StorageDataset,
		}

		if cmd.Args.StorageCreateDevice != "" {
			storage.Mode = "device"
		} else if cmd.Args.StorageCreateLoop != -1 {
			storage.Mode = "loop"
		} else {
			storage.Mode = "existing"
		}
		err = cmd.fillDataWithStorage(data, storage)
		if err != nil {
			return err
		}
	}
	return nil
}

// Fill the given configuration data with parameters collected with
// interactive questions.
func (cmd *CmdInit) fillDataInteractive(data *cmdInitData, client lxd.ContainerServer, backendsAvailable []string) error {
	storage, err := cmd.askStorage(client, backendsAvailable)
	if err != nil {
		return err
	}
	defaultPrivileged := cmd.askDefaultPrivileged()
	networking := cmd.askNetworking()

	if storage != nil && storage.Backend == "zfs" {
		err = cmd.fillDataWithStorage(data, storage)
		if err != nil {
			return err
		}
	}

	err = cmd.fillDataWithDefaultPrivileged(data, defaultPrivileged)
	if err != nil {
		return err
	}

	cmd.fillDataWithNetworking(data, networking)

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
func (cmd *CmdInit) fillDataWithStorage(data *cmdInitData, storage *cmdInitStorageParams) error {
	// Destroy any existing loop device
	for _, file := range []string{"zfs.img"} {
		os.Remove(shared.VarPath(file))
	}

	if storage == nil {
		return nil
	}

	if storage.Mode == "loop" {
		storage.Device = shared.VarPath("zfs.img")
		f, err := os.Create(storage.Device)
		if err != nil {
			return fmt.Errorf("Failed to open %s: %s", storage.Device, err)
		}

		err = f.Chmod(0600)
		if err != nil {
			return fmt.Errorf("Failed to chmod %s: %s", storage.Device, err)
		}

		err = f.Truncate(int64(storage.LoopSize * 1024 * 1024 * 1024))
		if err != nil {
			return fmt.Errorf("Failed to create sparse file %s: %s", storage.Device, err)
		}

		err = f.Close()
		if err != nil {
			return fmt.Errorf("Failed to close %s: %s", storage.Device, err)
		}
	}

	if shared.StringInSlice(storage.Mode, []string{"loop", "device"}) {
		output, err := shared.RunCommand(
			"zpool",
			"create", "-f", "-m", "none", "-O", "compression=on",
			storage.Pool, storage.Device)
		if err != nil {
			return fmt.Errorf("Failed to create the ZFS pool: %s", output)
		}
	}

	data.Config["storage.zfs_pool_name"] = storage.Pool

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

	// Detect zfs
	out, err := exec.LookPath("zfs")
	if err == nil && len(out) != 0 && !cmd.RunningInUserns {
		_ = util.LoadModule("zfs")

		_, err := shared.RunCommand("zpool", "list")
		if err == nil {
			drivers = append(drivers, "zfs")
		}
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
func (cmd *CmdInit) askStorage(client lxd.ContainerServer, availableBackends []string) (*cmdInitStorageParams, error) {
	if !cmd.Context.AskBool("Do you want to configure a new storage pool (yes/no) [default=yes]? ", "yes") {
		return nil, nil
	}
	storage := &cmdInitStorageParams{}
	defaultStorage := "dir"
	if shared.StringInSlice("zfs", availableBackends) {
		defaultStorage = "zfs"
	}
	for {
		storage.Backend = cmd.Context.AskChoice(fmt.Sprintf("Name of the storage backend to use (dir or zfs) [default=%s]: ", defaultStorage), availableBackends, defaultStorage)

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
			if storage.Backend == "zfs" {
				question := fmt.Sprintf("Name of the new %s pool or dataset [default=lxd]: ", strings.ToUpper(storage.Backend))
				storage.Pool = cmd.Context.AskString(question, "lxd", nil)
			}

			if cmd.Context.AskBool("Would you like to use an existing block device (yes/no) [default=no]? ", "no") {
				deviceExists := func(path string) error {
					if !shared.IsBlockdevPath(path) {
						return fmt.Errorf("'%s' is not a block device", path)
					}
					return nil
				}
				storage.Device = cmd.Context.AskString("Path to the existing block device: ", "", deviceExists)
				storage.Mode = "device"
			} else {
				st := syscall.Statfs_t{}
				err := syscall.Statfs(shared.VarPath(), &st)
				if err != nil {
					return nil, fmt.Errorf("couldn't statfs %s: %s", shared.VarPath(), err)
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
				storage.LoopSize = cmd.Context.AskInt(q, 1, -1, fmt.Sprintf("%d", def))
				storage.Mode = "loop"
			}
		} else {
			question := fmt.Sprintf("Name of the existing %s pool or dataset: ", strings.ToUpper(storage.Backend))
			storage.Pool = cmd.Context.AskString(question, "", nil)
			storage.Mode = "existing"
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
	idmapset, err := idmap.DefaultIdmapSet("")
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

// Defines the schema for all possible configuration knobs supported by the
// lxd init command, either directly fed via --preseed or populated by
// the auto/interactive modes.
type cmdInitData struct {
	api.ServerPut `yaml:",inline"`
	Profiles      []api.ProfilesPost
}

// Parameters needed when creating a storage pool in interactive or auto
// mode.
type cmdInitStorageParams struct {
	Backend  string // == supportedStoragePoolDrivers
	LoopSize int64  // Size in GB
	Device   string // Path
	Pool     string // pool name
	Mode     string
}

// Parameters needed when configuring the LXD server networking options in interactive
// mode or auto mode.
type cmdInitNetworkingParams struct {
	Address       string // Address
	Port          int64  // Port
	TrustPassword string // Trust password
}

// Shortcut for closure/anonymous functions that are meant to revert
// some change, and that are passed around as parameters.
type reverter func() error

func cmdInit(args *Args) error {
	context := cmd.NewContext(os.Stdin, os.Stdout, os.Stderr)
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
