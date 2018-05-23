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
	flagRsyncArgs  string
	flagNoProfiles bool
}

func (c *cmdMigrate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "lxd-p2c <target URL> <container name> <filesystem root> [<filesystem mounts>...]"
	cmd.Short = "Physical to container migration tool"
	cmd.Long = `Description:
  Physical to container migration tool

  This tool lets you turn any Linux filesystem (including your current one)
  into a LXD container on a remote LXD host.

  It will setup a clean mount tree made of the root filesystem and any
  additional mount you list, then transfer this through LXD's migration
  API to create a new container from it.

  The same set of options as ` + "`lxc launch`" + ` are also supported.
`
	cmd.RunE = c.Run
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, "Configuration key and value to set on the container"+"``")
	cmd.Flags().StringVarP(&c.flagNetwork, "network", "n", "", "Network to use for the container"+"``")
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, "Profile to apply to the container"+"``")
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", "Storage pool to use for the container"+"``")
	cmd.Flags().StringVarP(&c.flagType, "type", "t", "", "Instance type to use for the container"+"``")
	cmd.Flags().StringVar(&c.flagRsyncArgs, "rsync-args", "", "Extra arguments to pass to rsync"+"``")
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, "Create the container with no profiles applied")

	return cmd
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

	URL, err := parseURL(args[0])
	if err != nil {
		return err
	}

	// Connect to the target
	dst, err := connectTarget(URL)
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

	// Check if the container already exists
	_, _, err = dst.GetContainer(apiArgs.Name)
	if err == nil {
		return fmt.Errorf("Container '%s' already exists", apiArgs.Name)
	}

	// Create the container
	success := false
	op, err := dst.CreateContainer(apiArgs)
	if err != nil {
		return err
	}

	defer func() {
		if !success {
			dst.DeleteContainer(apiArgs.Name)
		}
	}()

	progress := utils.ProgressRenderer{Format: "Transferring container: %s"}
	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	err = transferRootfs(dst, op, fullPath, c.flagRsyncArgs)
	if err != nil {
		return err
	}

	progress.Done(fmt.Sprintf("Container %s successfully created", apiArgs.Name))
	success = true

	return nil
}
