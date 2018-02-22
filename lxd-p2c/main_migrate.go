package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
)

type cmdMigrate struct {
	global *cmdGlobal

	flagConfig     []string
	flagNetwork    string
	flagProfile    []string
	flagStorage    string
	flagType       string
	flagNoProfiles bool
}

func (c *cmdMigrate) Run(cmd *cobra.Command, args []string) error {
	// Help and usage
	if len(args) == 0 {
		return cmd.Help()
	}

	// Sanity checks
	if os.Geteuid() != 0 {
		return fmt.Errorf("This tool must be run as root")
	}

	_, err := exec.LookPath("rsync")
	if err != nil {
		return err
	}

	if c.flagNoProfiles && len(c.flagProfile) != 0 {
		return fmt.Errorf("no-profiles can't be specified alongside profiles")
	}

	// Handle mandatory arguments
	if len(args) < 3 {
		cmd.Help()
		return fmt.Errorf("Missing required arguments")
	}

	// Get and sort the mounts
	mounts := args[2:]
	sort.Strings(mounts)

	// Create the temporary directory to be used for the mounts
	path, err := ioutil.TempDir("", "lxd-p2c_mount_")
	if err != nil {
		return err
	}

	// Automatically clean-up the temporary path on exit
	defer func(path string) {
		syscall.Unmount(path, syscall.MNT_DETACH)
		os.Remove(path)
	}(path)

	// Create the rootfs directory
	fullPath := fmt.Sprintf("%s/rootfs", path)
	err = os.Mkdir(fullPath, 0755)
	if err != nil {
		return err
	}

	// Setup the source (mounts)
	err = setupSource(fullPath, mounts)
	if err != nil {
		return fmt.Errorf("Failed to setup the source: %v", err)
	}

	// Connect to the target
	dst, err := connectTarget(args[0])
	if err != nil {
		return err
	}

	// Container creation request
	apiArgs := api.ContainersPost{}
	apiArgs.Name = args[1]
	apiArgs.Source = api.ContainerSource{
		Type: "migration",
		Mode: "push",
	}

	// System architecture
	architectureName, err := osarch.ArchitectureGetLocal()
	if err != nil {
		return err
	}
	apiArgs.Architecture = architectureName

	// Instance type
	apiArgs.InstanceType = c.flagType

	// Config overrides
	apiArgs.Config = map[string]string{}
	for _, entry := range c.flagConfig {
		if !strings.Contains(entry, "=") {
			return fmt.Errorf("Bad key=value configuration: %v", entry)
		}

		fields := strings.SplitN(entry, "=", 2)
		apiArgs.Config[fields[0]] = fields[1]
	}

	// Profiles
	if len(c.flagProfile) != 0 {
		apiArgs.Profiles = c.flagProfile
	}

	if c.flagNoProfiles {
		apiArgs.Profiles = []string{}
	}

	// Devices
	apiArgs.Devices = map[string]map[string]string{}

	network := c.flagNetwork
	if network != "" {
		apiArgs.Devices["eth0"] = map[string]string{
			"type":    "nic",
			"nictype": "bridged",
			"parent":  network,
			"name":    "eth0",
		}
	}

	storage := c.flagStorage
	if storage != "" {
		apiArgs.Devices["root"] = map[string]string{
			"type": "disk",
			"pool": storage,
			"path": "/",
		}
	}

	// Create the container
	op, err := dst.CreateContainer(apiArgs)
	if err != nil {
		return err
	}

	progress := utils.ProgressRenderer{Format: "Transferring container: %s"}
	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	err = transferRootfs(dst, op, fullPath)
	if err != nil {
		return err
	}

	progress.Done(fmt.Sprintf("Container %s successfully created", apiArgs.Name))

	return nil
}
