package main

import (
	"bufio"
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
)

func cmdInit() error {
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
	runningInUserns = shared.RunningInUserNS()

	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

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

	reader := bufio.NewReader(os.Stdin)

	askBool := func(question string, default_ string) bool {
		for {
			fmt.Printf(question)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSuffix(input, "\n")
			if input == "" {
				input = default_
			}
			if shared.StringInSlice(strings.ToLower(input), []string{"yes", "y"}) {
				return true
			} else if shared.StringInSlice(strings.ToLower(input), []string{"no", "n"}) {
				return false
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	askChoice := func(question string, choices []string, default_ string) string {
		for {
			fmt.Printf(question)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSuffix(input, "\n")
			if input == "" {
				input = default_
			}
			if shared.StringInSlice(input, choices) {
				return input
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	askInt := func(question string, min int64, max int64, default_ string) int64 {
		for {
			fmt.Printf(question)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSuffix(input, "\n")
			if input == "" {
				input = default_
			}
			intInput, err := strconv.ParseInt(input, 10, 64)

			if err == nil && (min == -1 || intInput >= min) && (max == -1 || intInput <= max) {
				return intInput
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	askString := func(question string, default_ string, validate func(string) error) string {
		for {
			fmt.Printf(question)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSuffix(input, "\n")
			if input == "" {
				input = default_
			}
			if validate != nil {
				result := validate(input)
				if result != nil {
					fmt.Printf("Invalid input: %s\n\n", result)
					continue
				}
			}
			if len(input) != 0 {
				return input
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	askPassword := func(question string) string {
		for {
			fmt.Printf(question)
			pwd, _ := terminal.ReadPassword(0)
			fmt.Printf("\n")
			inFirst := string(pwd)
			inFirst = strings.TrimSuffix(inFirst, "\n")

			fmt.Printf("Again: ")
			pwd, _ = terminal.ReadPassword(0)
			fmt.Printf("\n")
			inSecond := string(pwd)
			inSecond = strings.TrimSuffix(inSecond, "\n")

			if inFirst == inSecond {
				return inFirst
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	// Connect to LXD
	c, err := lxd.ConnectLXDUnix("", nil)
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

	if *argAuto {
		if *argStorageBackend == "" {
			*argStorageBackend = "dir"
		}

		// Do a bunch of sanity checks
		if !shared.StringInSlice(*argStorageBackend, backendsSupported) {
			return fmt.Errorf("The requested backend '%s' isn't supported by lxd init.", *argStorageBackend)
		}

		if !shared.StringInSlice(*argStorageBackend, backendsAvailable) {
			return fmt.Errorf("The requested backend '%s' isn't available on your system (missing tools).", *argStorageBackend)
		}

		if *argStorageBackend == "dir" {
			if *argStorageCreateLoop != -1 || *argStorageCreateDevice != "" || *argStoragePool != "" {
				return fmt.Errorf("None of --storage-pool, --storage-create-device or --storage-create-loop may be used with the 'dir' backend.")
			}
		}

		if *argStorageBackend == "zfs" {
			if *argStorageCreateLoop != -1 && *argStorageCreateDevice != "" {
				return fmt.Errorf("Only one of --storage-create-device or --storage-create-loop can be specified with the 'zfs' backend.")
			}

			if *argStoragePool == "" {
				return fmt.Errorf("--storage-pool must be specified with the 'zfs' backend.")
			}
		}

		if *argNetworkAddress == "" {
			if *argNetworkPort != -1 {
				return fmt.Errorf("--network-port cannot be used without --network-address.")
			}
			if *argTrustPassword != "" {
				return fmt.Errorf("--trust-password cannot be used without --network-address.")
			}
		}

		// Set the local variables
		if *argStorageCreateDevice != "" {
			storageMode = "device"
		} else if *argStorageCreateLoop != -1 {
			storageMode = "loop"
		} else {
			storageMode = "existing"
		}

		storageBackend = *argStorageBackend
		storageLoopSize = *argStorageCreateLoop
		storageDevice = *argStorageCreateDevice
		storagePool = *argStoragePool
		networkAddress = *argNetworkAddress
		networkPort = *argNetworkPort
		trustPassword = *argTrustPassword
	} else {
		if *argStorageBackend != "" || *argStorageCreateDevice != "" || *argStorageCreateLoop != -1 || *argStoragePool != "" || *argNetworkAddress != "" || *argNetworkPort != -1 || *argTrustPassword != "" {
			return fmt.Errorf("Init configuration is only valid with --auto")
		}

		defaultStorage := "dir"
		if shared.StringInSlice("zfs", backendsAvailable) {
			defaultStorage = "zfs"
		}

		storageBackend = askChoice(fmt.Sprintf("Name of the storage backend to use (dir or zfs) [default=%s]: ", defaultStorage), backendsSupported, defaultStorage)

		if !shared.StringInSlice(storageBackend, backendsSupported) {
			return fmt.Errorf("The requested backend '%s' isn't supported by lxd init.", storageBackend)
		}

		if !shared.StringInSlice(storageBackend, backendsAvailable) {
			return fmt.Errorf("The requested backend '%s' isn't available on your system (missing tools).", storageBackend)
		}

		if storageBackend == "zfs" {
			if askBool("Create a new ZFS pool (yes/no) [default=yes]? ", "yes") {
				storagePool = askString("Name of the new ZFS pool [default=lxd]: ", "lxd", nil)
				if askBool("Would you like to use an existing block device (yes/no) [default=no]? ", "no") {
					deviceExists := func(path string) error {
						if !shared.IsBlockdevPath(path) {
							return fmt.Errorf("'%s' is not a block device", path)
						}
						return nil
					}
					storageDevice = askString("Path to the existing block device: ", "", deviceExists)
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
					storageLoopSize = askInt(q, 1, -1, fmt.Sprintf("%d", def))
					storageMode = "loop"
				}
			} else {
				storagePool = askString("Name of the existing ZFS pool or dataset: ", "", nil)
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
			if askBool("Would you like to have your containers share their parent's allocation (yes/no) [default=yes]? ", "yes") {
				defaultPrivileged = 1
			} else {
				defaultPrivileged = 0
			}
		}

		if askBool("Would you like LXD to be available over the network (yes/no) [default=no]? ", "no") {
			isIPAddress := func(s string) error {
				if s != "all" && net.ParseIP(s) == nil {
					return fmt.Errorf("'%s' is not an IP address", s)
				}
				return nil
			}

			networkAddress = askString("Address to bind LXD to (not including port) [default=all]: ", "all", isIPAddress)
			if networkAddress == "all" {
				networkAddress = "::"
			}

			if net.ParseIP(networkAddress).To4() == nil {
				networkAddress = fmt.Sprintf("[%s]", networkAddress)
			}
			networkPort = askInt("Port to bind LXD to [default=8443]: ", 1, 65535, "8443")
			trustPassword = askPassword("Trust password for new clients: ")
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
