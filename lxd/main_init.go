package main

import (
	"fmt"
	"io/ioutil"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

type cmdInitData struct {
	Node    initDataNode     `yaml:",inline"`
	Cluster *initDataCluster `json:"cluster" yaml:"cluster"`
}

type cmdInit struct {
	global *cmdGlobal

	flagAuto    bool
	flagPreseed bool
	flagDump    bool

	flagNetworkAddress  string
	flagNetworkPort     int
	flagStorageBackend  string
	flagStorageDevice   string
	flagStorageLoopSize int
	flagStoragePool     string
	flagTrustPassword   string
}

func (c *cmdInit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "init"
	cmd.Short = "Configure the LXD daemon"
	cmd.Long = `Description:
  Configure the LXD daemon
`
	cmd.Example = `  init --preseed
  init --auto [--network-address=IP] [--network-port=8443] [--storage-backend=dir]
              [--storage-create-device=DEVICE] [--storage-create-loop=SIZE]
              [--storage-pool=POOL] [--trust-password=PASSWORD]
  init --dump
`
	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagAuto, "auto", false, "Automatic (non-interactive) mode")
	cmd.Flags().BoolVar(&c.flagPreseed, "preseed", false, "Pre-seed mode, expects YAML config from stdin")
	cmd.Flags().BoolVar(&c.flagDump, "dump", false, "Dump YAML config to stdout")

	cmd.Flags().StringVar(&c.flagNetworkAddress, "network-address", "", "Address to bind LXD to (default: none)"+"``")
	cmd.Flags().IntVar(&c.flagNetworkPort, "network-port", -1, fmt.Sprintf("Port to bind LXD to (default: %d)"+"``", shared.DefaultPort))
	cmd.Flags().StringVar(&c.flagStorageBackend, "storage-backend", "", "Storage backend to use (btrfs, dir, lvm or zfs, default: dir)"+"``")
	cmd.Flags().StringVar(&c.flagStorageDevice, "storage-create-device", "", "Setup device based storage using DEVICE"+"``")
	cmd.Flags().IntVar(&c.flagStorageLoopSize, "storage-create-loop", -1, "Setup loop based storage with SIZE in GB"+"``")
	cmd.Flags().StringVar(&c.flagStoragePool, "storage-pool", "", "Storage pool to use or create"+"``")
	cmd.Flags().StringVar(&c.flagTrustPassword, "trust-password", "", "Password required to add new clients"+"``")

	return cmd
}

func (c *cmdInit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if c.flagAuto && c.flagPreseed {
		return fmt.Errorf("Can't use --auto and --preseed together")
	}

	if !c.flagAuto && (c.flagNetworkAddress != "" || c.flagNetworkPort != -1 ||
		c.flagStorageBackend != "" || c.flagStorageDevice != "" ||
		c.flagStorageLoopSize != -1 || c.flagStoragePool != "" ||
		c.flagTrustPassword != "") {
		return fmt.Errorf("Configuration flags require --auto")
	}

	if c.flagDump && (c.flagAuto || c.flagPreseed || c.flagNetworkAddress != "" ||
		c.flagNetworkPort != -1 || c.flagStorageBackend != "" ||
		c.flagStorageDevice != "" || c.flagStorageLoopSize != -1 ||
		c.flagStoragePool != "" || c.flagTrustPassword != "") {
		return fmt.Errorf("Can't use --dump with other flags")
	}

	// Connect to LXD
	d, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return errors.Wrap(err, "Failed to connect to local LXD")
	}

	// Dump mode
	if c.flagDump {
		err := c.RunDump(d)
		if err != nil {
			return err
		}

		return nil
	}

	// Prepare the input data
	var config *cmdInitData

	// Preseed mode
	if c.flagPreseed {
		config, err = c.RunPreseed(cmd, args, d)
		if err != nil {
			return err
		}
	}

	// Auto mode
	if c.flagAuto {
		config, err = c.RunAuto(cmd, args, d)
		if err != nil {
			return err
		}
	}

	// Interactive mode
	if !c.flagAuto && !c.flagPreseed {
		config, err = c.RunInteractive(cmd, args, d)
		if err != nil {
			return err
		}
	}

	// Check if the path to the cluster certificate is set
	// If yes then read cluster certificate from file
	if config.Cluster != nil && config.Cluster.ClusterCertificatePath != "" {
		if !shared.PathExists(config.Cluster.ClusterCertificatePath) {
			return fmt.Errorf("Path %s doesn't exist", config.Cluster.ClusterCertificatePath)
		}
		content, err := ioutil.ReadFile(config.Cluster.ClusterCertificatePath)
		if err != nil {
			return err
		}
		config.Cluster.ClusterCertificate = string(content)
	}

	// If clustering is enabled, and no cluster.https_address network address
	// was specified, we fallback to core.https_address.
	if config.Cluster != nil &&
		config.Node.Config["core.https_address"] != nil &&
		config.Node.Config["cluster.https_address"] == nil {
		config.Node.Config["cluster.https_address"] = config.Node.Config["core.https_address"]
	}

	// Detect if the user has chosen to join a cluster using the new
	// cluster join API format, and use the dedicated API if so.
	if config.Cluster != nil && config.Cluster.ClusterAddress != "" && config.Cluster.ServerAddress != "" {
		op, err := d.UpdateCluster(config.Cluster.ClusterPut, "")
		if err != nil {
			return errors.Wrap(err, "Failed to join cluster")
		}
		err = op.Wait()
		if err != nil {
			return errors.Wrap(err, "Failed to join cluster")
		}
		return nil
	}

	revert := revert.New()
	defer revert.Fail()

	localRevert, err := initDataNodeApply(d, config.Node)
	if err != nil {
		return err
	}
	revert.Add(localRevert)

	err = initDataClusterApply(d, config.Cluster)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

func (c *cmdInit) availableStorageDrivers(poolType string) []string {
	backingFs, err := util.FilesystemDetect(shared.VarPath())
	if err != nil {
		backingFs = "dir"
	}

	// Get info for supported drivers.
	s := state.NewState(nil, nil, nil, nil, sys.DefaultOS(), nil, nil, nil, nil, nil, nil, func() {})
	supportedDrivers := storageDrivers.SupportedDrivers(s)

	drivers := make([]string, 0, len(supportedDrivers))

	// Check available backends.
	for _, driver := range supportedDrivers {
		if poolType == "remote" && !shared.StringInSlice(driver.Name, []string{"ceph", "cephfs"}) {
			continue
		}

		if poolType == "local" && shared.StringInSlice(driver.Name, []string{"ceph", "cephfs"}) {
			continue
		}

		if poolType == "all" && driver.Name == "cephfs" {
			continue
		}

		if driver.Name == "dir" {
			drivers = append(drivers, driver.Name)
			continue
		}

		// btrfs can work in user namespaces too. (If source=/some/path/on/btrfs is used.)
		if shared.RunningInUserNS() && (backingFs != "btrfs" || driver.Name != "btrfs") {
			continue
		}

		drivers = append(drivers, driver.Name)
	}

	return drivers
}
