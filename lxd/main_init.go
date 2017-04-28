package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/crypto/ssh/terminal"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/cmd"
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

	backendsAvailable := []string{"dir"}
	backendsSupported := []string{"dir", "zfs"}

	// Detect zfs
	out, err := exec.LookPath("zfs")
	if err == nil && len(out) != 0 && !runningInUserns {
		_ = loadModule("zfs")

		_, err := shared.RunCommand("zpool", "list")
		if err == nil {
			backendsAvailable = append(backendsAvailable, "zfs")
		}
	}

	// Connect to LXD
	c, err := lxd.ConnectLXDUnix(cmd.SocketPath, nil)
	if err != nil {
		return fmt.Errorf("Unable to talk to LXD: %s", err)
	}

	setServerConfig := func(key string, value string) error {
		server, etag, err := c.GetServer()
		if err != nil {
			return err
		}

		if server.Config == nil {
			server.Config = map[string]interface{}{}
		}

		server.Config[key] = value

		err = c.UpdateServer(server.Writable(), etag)
		if err != nil {
			return err
		}

		return nil
	}

	setProfileConfigItem := func(profileName string, key string, value string) error {
		profile, etag, err := c.GetProfile(profileName)
		if err != nil {
			return err
		}

		if profile.Config == nil {
			profile.Config = map[string]string{}
		}

		profile.Config[key] = value

		err = c.UpdateProfile(profileName, profile.Writable(), etag)
		if err != nil {
			return err
		}

		return nil
	}

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

		// Do a bunch of sanity checks
		if !shared.StringInSlice(cmd.Args.StorageBackend, backendsSupported) {
			return fmt.Errorf("The requested backend '%s' isn't supported by lxd init.", cmd.Args.StorageBackend)
		}

		if !shared.StringInSlice(cmd.Args.StorageBackend, backendsAvailable) {
			return fmt.Errorf("The requested backend '%s' isn't available on your system (missing tools).", cmd.Args.StorageBackend)
		}

		if cmd.Args.StorageBackend == "dir" {
			if cmd.Args.StorageCreateLoop != -1 || cmd.Args.StorageCreateDevice != "" || cmd.Args.StoragePool != "" {
				return fmt.Errorf("None of --storage-pool, --storage-create-device or --storage-create-loop may be used with the 'dir' backend.")
			}
		}

		if cmd.Args.StorageBackend == "zfs" {
			if cmd.Args.StorageCreateLoop != -1 && cmd.Args.StorageCreateDevice != "" {
				return fmt.Errorf("Only one of --storage-create-device or --storage-create-loop can be specified with the 'zfs' backend.")
			}

			if cmd.Args.StoragePool == "" {
				return fmt.Errorf("--storage-pool must be specified with the 'zfs' backend.")
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
		if cmd.Args.StorageBackend != "" || cmd.Args.StorageCreateDevice != "" || cmd.Args.StorageCreateLoop != -1 || cmd.Args.StoragePool != "" || cmd.Args.NetworkAddress != "" || cmd.Args.NetworkPort != -1 || cmd.Args.TrustPassword != "" {
			return fmt.Errorf("Init configuration is only valid with --auto")
		}

		defaultStorage := "dir"
		if shared.StringInSlice("zfs", backendsAvailable) {
			defaultStorage = "zfs"
		}

		storageBackend = cmd.Context.AskChoice(fmt.Sprintf("Name of the storage backend to use (dir or zfs) [default=%s]: ", defaultStorage), backendsSupported, defaultStorage)

		if !shared.StringInSlice(storageBackend, backendsSupported) {
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

	// Unset all storage keys, core.https_address and core.trust_password
	for _, key := range []string{"storage.zfs_pool_name", "core.https_address", "core.trust_password"} {
		err = setServerConfig(key, "")
		if err != nil {
			return err
		}
	}

	// Destroy any existing loop device
	for _, file := range []string{"zfs.img"} {
		os.Remove(shared.VarPath(file))
	}

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

		// Configure LXD to use the pool
		err = setServerConfig("storage.zfs_pool_name", storagePool)
		if err != nil {
			return err
		}
	}

	if defaultPrivileged == 0 {
		err = setProfileConfigItem("default", "security.privileged", "")
		if err != nil {
			return err
		}
	} else if defaultPrivileged == 1 {
		err = setProfileConfigItem("default", "security.privileged", "true")
		if err != nil {
		}
	}

	if networkAddress != "" {
		if networkPort == -1 {
			networkPort = 8443
		}

		err = setServerConfig("core.https_address", fmt.Sprintf("%s:%d", networkAddress, networkPort))
		if err != nil {
			return err
		}

		if trustPassword != "" {
			err = setServerConfig("core.trust_password", trustPassword)
			if err != nil {
				return err
			}
		}
	}

	fmt.Printf("LXD has been successfully configured.\n")
	return nil
}

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
