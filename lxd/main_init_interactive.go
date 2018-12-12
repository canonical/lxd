package main

import (
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/idmap"
)

func (c *cmdInit) RunInteractive(cmd *cobra.Command, args []string, d lxd.ContainerServer) (*cmdInitData, error) {
	// Initialize config
	config := cmdInitData{}
	config.Node.Config = map[string]interface{}{}
	config.Node.Networks = []api.NetworksPost{}
	config.Node.StoragePools = []api.StoragePoolsPost{}
	config.Node.Profiles = []api.ProfilesPost{
		{
			Name: "default",
			ProfilePut: api.ProfilePut{
				Config:  map[string]string{},
				Devices: map[string]map[string]string{},
			},
		},
	}

	// Clustering
	err := c.askClustering(&config, d)
	if err != nil {
		return nil, err
	}

	// Ask all the other questions
	if config.Cluster == nil || config.Cluster.ClusterAddress == "" {
		// Storage
		err = c.askStorage(&config, d)
		if err != nil {
			return nil, err
		}

		// MAAS
		err = c.askMAAS(&config, d)
		if err != nil {
			return nil, err
		}

		// Networking
		err = c.askNetworking(&config, d)
		if err != nil {
			return nil, err
		}

		// Daemon config
		err = c.askDaemon(&config, d)
		if err != nil {
			return nil, err
		}
	}

	// Print the YAML
	if cli.AskBool("Would you like a YAML \"lxd init\" preseed to be printed? (yes/no) [default=no]: ", "no") {
		var object cmdInitData

		// If the user has chosen to join an existing cluster, print
		// only YAML for the cluster section, which is the only
		// relevant one. Otherwise print the regular config.
		if config.Cluster != nil && config.Cluster.ClusterAddress != "" {
			object = cmdInitData{}
			object.Cluster = config.Cluster
		} else {
			object = config
		}

		out, err := yaml.Marshal(object)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to render the config")
		}

		fmt.Printf("%s\n", out)
	}

	return &config, nil
}

func (c *cmdInit) askClustering(config *cmdInitData, d lxd.ContainerServer) error {
	if cli.AskBool("Would you like to use LXD clustering? (yes/no) [default=no]: ", "no") {
		config.Cluster = &initDataCluster{}
		config.Cluster.Enabled = true

		// Cluster server name
		serverName, err := os.Hostname()
		if err != nil {
			serverName = "lxd"
		}

		config.Cluster.ServerName = cli.AskString(
			fmt.Sprintf("What name should be used to identify this node in the cluster? [default=%s]: ", serverName), serverName, nil)

		// Cluster server address
		address := util.NetworkInterfaceAddress()
		serverAddress := util.CanonicalNetworkAddress(cli.AskString(
			fmt.Sprintf("What IP address or DNS name should be used to reach this node? [default=%s]: ", address), address, nil))
		config.Node.Config["core.https_address"] = serverAddress

		if cli.AskBool("Are you joining an existing cluster? (yes/no) [default=no]: ", "no") {
			// Existing cluster
			config.Cluster.ServerAddress = serverAddress
			for {
				// Cluster URL
				clusterAddress := cli.AskString("IP address or FQDN of an existing cluster node: ", "", nil)
				_, _, err := net.SplitHostPort(clusterAddress)
				if err != nil {
					clusterAddress = fmt.Sprintf("%s:8443", clusterAddress)
				}
				config.Cluster.ClusterAddress = clusterAddress

				// Cluster certificate
				cert, err := shared.GetRemoteCertificate(fmt.Sprintf("https://%s", config.Cluster.ClusterAddress))
				if err != nil {
					fmt.Printf("Error connecting to existing cluster node: %v\n", err)
					continue
				}

				certDigest := shared.CertFingerprint(cert)
				fmt.Printf("Cluster fingerprint: %s\n", certDigest)
				fmt.Printf("You can validate this fingerprint by running \"lxc info\" locally on an existing node.\n")
				if !cli.AskBool("Is this the correct fingerprint? (yes/no) [default=no]: ", "no") {
					return fmt.Errorf("User aborted configuration")
				}
				config.Cluster.ClusterCertificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))

				// Cluster password
				config.Cluster.ClusterPassword = cli.AskPasswordOnce("Cluster trust password: ")
				break
			}

			// Root is required to access the certificate files
			if os.Geteuid() != 0 {
				return fmt.Errorf("Joining an existing cluster requires root privileges")
			}

			// Confirm wiping
			if !cli.AskBool("All existing data is lost when joining a cluster, continue? (yes/no) [default=no] ", "no") {
				return fmt.Errorf("User aborted configuration")
			}

			// Connect to existing cluster
			cert, err := util.LoadCert(shared.VarPath(""))
			if err != nil {
				return err
			}

			err = cluster.SetupTrust(string(cert.PublicKey()),
				config.Cluster.ClusterAddress,
				string(config.Cluster.ClusterCertificate), config.Cluster.ClusterPassword)
			if err != nil {
				return errors.Wrap(err, "Failed to setup trust relationship with cluster")
			}

			// Client parameters to connect to the target cluster node.
			args := &lxd.ConnectionArgs{
				TLSClientCert: string(cert.PublicKey()),
				TLSClientKey:  string(cert.PrivateKey()),
				TLSServerCert: string(config.Cluster.ClusterCertificate),
			}

			client, err := lxd.ConnectLXD(fmt.Sprintf("https://%s", config.Cluster.ClusterAddress), args)
			if err != nil {
				return err
			}

			// Get the list of required member config keys.
			cluster, _, err := client.GetCluster()
			if err != nil {
				return errors.Wrap(err, "Failed to retrieve cluster information")
			}

			validator := func(string) error { return nil }
			for i, config := range cluster.MemberConfig {
				question := fmt.Sprintf("Choose %s: ", config.Description)
				cluster.MemberConfig[i].Value = cli.AskString(question, "", validator)
			}

			config.Cluster.MemberConfig = cluster.MemberConfig
		} else {
			// Password authentication
			if cli.AskBool("Setup password authentication on the cluster? (yes/no) [default=yes]: ", "yes") {
				config.Node.Config["core.trust_password"] = cli.AskPassword("Trust password for new clients: ")
			}
		}
	}

	return nil
}

func (c *cmdInit) askMAAS(config *cmdInitData, d lxd.ContainerServer) error {
	if !cli.AskBool("Would you like to connect to a MAAS server? (yes/no) [default=no]: ", "no") {
		return nil
	}

	serverName, err := os.Hostname()
	if err != nil {
		serverName = "lxd"
	}

	maasHostname := cli.AskString(fmt.Sprintf("What's the name of this host in MAAS? [default=%s]: ", serverName), serverName, nil)
	if maasHostname != serverName {
		config.Node.Config["maas.machine"] = maasHostname
	}

	config.Node.Config["maas.api.url"] = cli.AskString("URL of your MAAS server (e.g. http://1.2.3.4:5240/MAAS): ", "", nil)
	config.Node.Config["maas.api.key"] = cli.AskString("API key for your MAAS server: ", "", nil)

	return nil
}

func (c *cmdInit) askNetworking(config *cmdInitData, d lxd.ContainerServer) error {
	if config.Cluster != nil || !cli.AskBool("Would you like to create a new local network bridge? (yes/no) [default=yes]: ", "yes") {
		// At this time, only the Ubuntu kernel supports the Fan, detect it
		fanKernel := false
		if shared.PathExists("/proc/sys/kernel/version") {
			content, _ := ioutil.ReadFile("/proc/sys/kernel/version")
			if content != nil && strings.Contains(string(content), "Ubuntu") {
				fanKernel = true
			}
		}

		if cli.AskBool("Would you like to configure LXD to use an existing bridge or host interface? (yes/no) [default=no]: ", "no") {
			for {
				name := cli.AskString("Name of the existing bridge or host interface: ", "", nil)

				if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", name)) {
					fmt.Println("The requested interface doesn't exist. Please choose another one.")
					continue
				}

				// Add to the default profile
				config.Node.Profiles[0].Devices["eth0"] = map[string]string{
					"type":    "nic",
					"nictype": "macvlan",
					"name":    "eth0",
					"parent":  name,
				}

				if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", name)) {
					config.Node.Profiles[0].Devices["eth0"]["nictype"] = "bridged"
				}

				if config.Node.Config["maas.api.url"] != nil && cli.AskBool("Is this interface connected to your MAAS server? (yes/no) [default=yes]: ", "yes") {
					maasSubnetV4 := cli.AskString("MAAS IPv4 subnet name for this interface (empty for no subnet): ", "",
						func(input string) error { return nil })

					if maasSubnetV4 != "" {
						config.Node.Profiles[0].Devices["eth0"]["maas.subnet.ipv4"] = maasSubnetV4
					}

					maasSubnetV6 := cli.AskString("MAAS IPv6 subnet name for this interface (empty for no subnet): ", "",
						func(input string) error { return nil })

					if maasSubnetV6 != "" {
						config.Node.Profiles[0].Devices["eth0"]["maas.subnet.ipv6"] = maasSubnetV6
					}
				}

				break
			}
		} else if config.Cluster != nil && fanKernel && cli.AskBool("Would you like to create a new Fan overlay network? (yes/no) [default=yes]: ", "yes") {
			// Define the network
			network := api.NetworksPost{}
			network.Name = "lxdfan0"
			network.Config = map[string]string{
				"bridge.mode": "fan",
			}

			// Select the underlay
			network.Config["fan.underlay_subnet"] = cli.AskString("What subnet should be used as the Fan underlay? [default=auto]: ", "auto", func(value string) error {
				var err error
				var subnet *net.IPNet

				// Handle auto
				if value == "auto" {
					subnet, _, err = networkDefaultGatewaySubnetV4()
					if err != nil {
						return err
					}
				} else {
					_, subnet, err = net.ParseCIDR(value)
					if err != nil {
						return err
					}
				}

				size, _ := subnet.Mask.Size()
				if size != 16 && size != 24 {
					return fmt.Errorf("The underlay subnet must be a /16 or a /24")
				}

				return nil
			})

			// Add the new network
			config.Node.Networks = append(config.Node.Networks, network)

			// Add to the default profile
			config.Node.Profiles[0].Devices["eth0"] = map[string]string{
				"type":    "nic",
				"nictype": "bridged",
				"name":    "eth0",
				"parent":  "lxdfan0",
			}
		}

		return nil
	}

	for {
		// Define the network
		network := api.NetworksPost{}
		network.Config = map[string]string{}

		// Network name
		network.Name = cli.AskString("What should the new bridge be called? [default=lxdbr0]: ", "lxdbr0", networkValidName)
		_, _, err := d.GetNetwork(network.Name)
		if err == nil {
			fmt.Printf("The requested network bridge \"%s\" already exists. Please choose another name.\n", network.Name)
			continue
		}

		// Add to the default profile
		config.Node.Profiles[0].Devices["eth0"] = map[string]string{
			"type":    "nic",
			"nictype": "bridged",
			"name":    "eth0",
			"parent":  network.Name,
		}

		// IPv4
		network.Config["ipv4.address"] = cli.AskString("What IPv4 address should be used? (CIDR subnet notation, “auto” or “none”) [default=auto]: ", "auto", func(value string) error {
			if shared.StringInSlice(value, []string{"auto", "none"}) {
				return nil
			}

			return networkValidAddressCIDRV4(value)
		})

		if !shared.StringInSlice(network.Config["ipv4.address"], []string{"auto", "none"}) {
			network.Config["ipv4.nat"] = fmt.Sprintf("%v",
				cli.AskBool("Would you like LXD to NAT IPv4 traffic on your bridge? [default=yes]: ", "yes"))
		}

		// IPv6
		network.Config["ipv6.address"] = cli.AskString("What IPv6 address should be used? (CIDR subnet notation, “auto” or “none”) [default=auto]: ", "auto", func(value string) error {
			if shared.StringInSlice(value, []string{"auto", "none"}) {
				return nil
			}

			return networkValidAddressCIDRV6(value)
		})

		if !shared.StringInSlice(network.Config["ipv6.address"], []string{"auto", "none"}) {
			network.Config["ipv6.nat"] = fmt.Sprintf("%v",
				cli.AskBool("Would you like LXD to NAT IPv6 traffic on your bridge? [default=yes]: ", "yes"))
		}

		// Add the new network
		config.Node.Networks = append(config.Node.Networks, network)
		break
	}

	return nil
}

func (c *cmdInit) askStorage(config *cmdInitData, d lxd.ContainerServer) error {
	if config.Cluster != nil {
		if cli.AskBool("Do you want to configure a new local storage pool? (yes/no) [default=yes]: ", "yes") {
			err := c.askStoragePool(config, d, "local")
			if err != nil {
				return err
			}
		}

		if cli.AskBool("Do you want to configure a new remote storage pool? (yes/no) [default=no]: ", "no") {
			err := c.askStoragePool(config, d, "remote")
			if err != nil {
				return err
			}
		}

		return nil
	}

	if !cli.AskBool("Do you want to configure a new storage pool? (yes/no) [default=yes]: ", "yes") {
		return nil
	}

	return c.askStoragePool(config, d, "all")
}

func (c *cmdInit) askStoragePool(config *cmdInitData, d lxd.ContainerServer, poolType string) error {
	// Figure out the preferred storage driver
	availableBackends := c.availableStorageDrivers(poolType)

	if len(availableBackends) == 0 {
		return fmt.Errorf("No %s storage backends available", poolType)
	}

	backingFs, err := util.FilesystemDetect(shared.VarPath())
	if err != nil {
		backingFs = "dir"
	}

	defaultStorage := "dir"
	if backingFs == "btrfs" && shared.StringInSlice("btrfs", availableBackends) {
		defaultStorage = "btrfs"
	} else if shared.StringInSlice("zfs", availableBackends) {
		defaultStorage = "zfs"
	} else if shared.StringInSlice("btrfs", availableBackends) {
		defaultStorage = "btrfs"
	}

	for {
		// Define the pool
		pool := api.StoragePoolsPost{}
		pool.Config = map[string]string{}

		if poolType == "all" {
			pool.Name = cli.AskString("Name of the new storage pool [default=default]: ", "default", nil)
		} else {
			pool.Name = poolType
		}

		_, _, err := d.GetStoragePool(pool.Name)
		if err == nil {
			if poolType == "all" {
				fmt.Printf("The requested storage pool \"%s\" already exists. Please choose another name.\n", pool.Name)
				continue
			}

			return fmt.Errorf("The %s storage pool already exists", poolType)
		}

		// Add to the default profile
		config.Node.Profiles[0].Devices["root"] = map[string]string{
			"type": "disk",
			"path": "/",
			"pool": pool.Name,
		}

		// Storage backend
		if len(availableBackends) > 1 {
			pool.Driver = cli.AskChoice(
				fmt.Sprintf("Name of the storage backend to use (%s) [default=%s]: ", strings.Join(availableBackends, ", "), defaultStorage), availableBackends, defaultStorage)
		} else {
			pool.Driver = availableBackends[0]
		}

		// Optimization for dir
		if pool.Driver == "dir" {
			config.Node.StoragePools = append(config.Node.StoragePools, pool)
			break
		}

		// Optimization for btrfs on btrfs
		if pool.Driver == "btrfs" && backingFs == "btrfs" {
			if cli.AskBool(fmt.Sprintf("Would you like to create a new btrfs subvolume under %s? (yes/no) [default=yes]: ", shared.VarPath("")), "yes") {
				pool.Config["source"] = shared.VarPath("storage-pools", pool.Name)
				config.Node.StoragePools = append(config.Node.StoragePools, pool)
				break
			}
		}

		if cli.AskBool(fmt.Sprintf("Create a new %s pool? (yes/no) [default=yes]: ", strings.ToUpper(pool.Driver)), "yes") {
			if pool.Driver == "zfs" && os.Geteuid() == 0 {
				poolVolumeExists, err := zfsPoolVolumeExists(pool.Name)
				if err == nil && poolVolumeExists {
					return fmt.Errorf("'%s' ZFS pool already exists", pool.Name)
				}
			}

			if pool.Driver == "ceph" {
				// Ask for the name of the cluster
				pool.Config["ceph.cluster_name"] = cli.AskString("Name of the existing CEPH cluster [default=ceph]: ", "ceph", nil)

				// Ask for the name of the osd pool
				pool.Config["ceph.osd.pool_name"] = cli.AskString("Name of the OSD storage pool [default=lxd]: ", "lxd", nil)

				// Ask for the number of placement groups
				pool.Config["ceph.osd.pg_num"] = cli.AskString("Number of placement groups [default=32]: ", "32", nil)
			} else if cli.AskBool("Would you like to use an existing block device? (yes/no) [default=no]: ", "no") {
				deviceExists := func(path string) error {
					if !shared.IsBlockdevPath(path) {
						return fmt.Errorf("'%s' is not a block device", path)
					}

					return nil
				}

				pool.Config["source"] = cli.AskString("Path to the existing block device: ", "", deviceExists)
			} else {
				st := syscall.Statfs_t{}
				err := syscall.Statfs(shared.VarPath(), &st)
				if err != nil {
					return errors.Wrapf(err, "Couldn't statfs %s", shared.VarPath())
				}

				/* choose 15 GB < x < 100GB, where x is 20% of the disk size */
				defaultSize := uint64(st.Frsize) * st.Blocks / (1024 * 1024 * 1024) / 5
				if defaultSize > 100 {
					defaultSize = 100
				}
				if defaultSize < 15 {
					defaultSize = 15
				}

				pool.Config["size"] = cli.AskString(
					fmt.Sprintf("Size in GB of the new loop device (1GB minimum) [default=%dGB]: ", defaultSize),
					fmt.Sprintf("%dGB", defaultSize),
					func(input string) error {
						input = strings.Split(input, "GB")[0]

						result, err := strconv.ParseInt(input, 10, 64)
						if err != nil {
							return err
						}

						if result < 1 {
							return fmt.Errorf("Minimum size is 1GB")
						}

						return nil
					})

				if !strings.HasSuffix(pool.Config["size"], "GB") {
					pool.Config["size"] = fmt.Sprintf("%sGB", pool.Config["size"])
				}
			}
		} else {
			if pool.Driver == "ceph" {
				// ask for the name of the cluster
				pool.Config["ceph.cluster_name"] = cli.AskString("Name of the existing CEPH cluster [default=ceph]: ", "ceph", nil)

				// ask for the name of the existing pool
				pool.Config["source"] = cli.AskString("Name of the existing OSD storage pool [default=lxd]: ", "lxd", nil)
				pool.Config["ceph.osd.pool_name"] = pool.Config["source"]
			} else {
				question := fmt.Sprintf("Name of the existing %s pool or dataset: ", strings.ToUpper(pool.Driver))
				pool.Config["source"] = cli.AskString(question, "", nil)
			}

			if pool.Driver == "zfs" && os.Geteuid() == 0 {
				poolVolumeExists, err := zfsPoolVolumeExists(pool.Config["source"])
				if err == nil && !poolVolumeExists {
					return fmt.Errorf("'%s' ZFS pool or dataset does not exist", pool.Config["source"])
				}
			}
		}

		if pool.Driver == "lvm" {
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
				if !cli.AskBool("Do you want to continue without thin provisioning? (yes/no) [default=yes]: ", "yes") {
					return fmt.Errorf("The LVM thin provisioning tools couldn't be found on the system")
				}

				pool.Config["lvm.use_thinpool"] = "false"
			}
		}

		config.Node.StoragePools = append(config.Node.StoragePools, pool)
		break
	}

	return nil
}

func (c *cmdInit) askDaemon(config *cmdInitData, d lxd.ContainerServer) error {
	// Detect lack of uid/gid
	idmapset, err := idmap.DefaultIdmapSet("", "")
	if (err != nil || len(idmapset.Idmap) == 0 || idmapset.Usable() != nil) && shared.RunningInUserNS() {
		fmt.Printf(`
We detected that you are running inside an unprivileged container.
This means that unless you manually configured your host otherwise,
you will not have enough uids and gids to allocate to your containers.

LXD can re-use your container's own allocation to avoid the problem.
Doing so makes your nested containers slightly less safe as they could
in theory attack their parent container and gain more privileges than
they otherwise would.

`)

		if cli.AskBool("Would you like to have your containers share their parent's allocation? (yes/no) [default=yes]: ", "yes") {
			config.Node.Profiles[0].Config["security.privileged"] = "true"
		}
	}

	// Network listener
	if config.Cluster == nil && cli.AskBool("Would you like LXD to be available over the network? (yes/no) [default=no]: ", "no") {
		isIPAddress := func(s string) error {
			if s != "all" && net.ParseIP(s) == nil {
				return fmt.Errorf("'%s' is not an IP address", s)
			}

			return nil
		}

		netAddr := cli.AskString("Address to bind LXD to (not including port) [default=all]: ", "all", isIPAddress)
		if netAddr == "all" {
			netAddr = "::"
		}

		if net.ParseIP(netAddr).To4() == nil {
			netAddr = fmt.Sprintf("[%s]", netAddr)
		}

		netPort := cli.AskInt("Port to bind LXD to [default=8443]: ", 1, 65535, "8443")
		config.Node.Config["core.https_address"] = fmt.Sprintf("%s:%d", netAddr, netPort)
		config.Node.Config["core.trust_password"] = cli.AskPassword("Trust password for new clients: ")
		if config.Node.Config["core.trust_password"] == "" {
			fmt.Printf("No password set, client certificates will have to be manually trusted.")
		}
	}

	// Ask if the user wants images to be automatically refreshed
	if !cli.AskBool("Would you like stale cached images to be updated automatically? (yes/no) [default=yes] ", "yes") {
		config.Node.Config["images.auto_update_interval"] = "0"
	}

	return nil
}
