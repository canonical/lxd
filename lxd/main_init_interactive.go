package main

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/idmap"
	"github.com/canonical/lxd/lxd/network"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/validate"
	"github.com/canonical/lxd/shared/version"
)

// RunInteractive executes the command to interactively configure the LXD daemon.
func (c *cmdInit) RunInteractive(cmd *cobra.Command, args []string, d lxd.InstanceServer, server *api.Server) (*api.InitPreseed, error) {
	// Initialize config
	config := api.InitPreseed{}
	config.Node.Config = map[string]any{}
	config.Node.Networks = []api.InitNetworksProjectPost{}
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
	err := c.askClustering(&config, server)
	if err != nil {
		return nil, err
	}

	// Ask all the other questions
	if config.Cluster == nil || config.Cluster.ClusterAddress == "" {
		// Storage
		err = c.askStorage(&config, d, server)
		if err != nil {
			return nil, err
		}

		// MAAS
		err = c.askMAAS(&config)
		if err != nil {
			return nil, err
		}

		// Networking
		err = c.askNetworking(&config, d)
		if err != nil {
			return nil, err
		}

		// Daemon config
		err = c.askDaemon(&config, server)
		if err != nil {
			return nil, err
		}
	}

	// Print the YAML
	preSeedPrint, err := c.global.asker.AskBool("Would you like a YAML \"lxd init\" preseed to be printed? (yes/no) [default=no]: ", "no")
	if err != nil {
		return nil, err
	}

	if preSeedPrint {
		var object api.InitPreseed

		// If the user has chosen to join an existing cluster, print
		// only YAML for the cluster section, which is the only
		// relevant one. Otherwise print the regular config.
		if config.Cluster != nil && config.Cluster.ClusterAddress != "" {
			object = api.InitPreseed{}
			object.Cluster = config.Cluster
		} else {
			object = config
		}

		out, err := yaml.Marshal(object)
		if err != nil {
			return nil, fmt.Errorf("Failed to render the config: %w", err)
		}

		fmt.Printf("%s\n", out)
	}

	return &config, nil
}

func (c *cmdInit) askClustering(config *api.InitPreseed, server *api.Server) error {
	clustering, err := c.global.asker.AskBool("Would you like to use LXD clustering? (yes/no) [default=no]: ", "no")
	if err != nil {
		return err
	}

	if clustering {
		config.Cluster = &api.InitClusterPreseed{}
		config.Cluster.Enabled = true

		askForServerName := func() error {
			config.Cluster.ServerName, err = c.global.asker.AskString(fmt.Sprintf("What member name should be used to identify this server in the cluster? [default=%s]: ", c.defaultHostname()), c.defaultHostname(), nil)
			if err != nil {
				return err
			}

			return nil
		}

		// Cluster server address
		address := util.NetworkInterfaceAddress()
		validateServerAddress := func(value string) error {
			address := util.CanonicalNetworkAddress(value, shared.HTTPSDefaultPort)

			host, _, _ := net.SplitHostPort(address)
			if slices.Contains([]string{"", "[::]", "0.0.0.0"}, host) {
				return errors.New("Invalid IP address or DNS name")
			}

			if err == nil {
				if server.Config["cluster.https_address"] == address || server.Config["core.https_address"] == address {
					// We already own the address, just move on.
					return nil
				}
			}

			listener, err := net.Listen("tcp", address)
			if err != nil {
				return fmt.Errorf("Can't bind address %q: %w", address, err)
			}

			_ = listener.Close()
			return nil
		}

		serverAddress, err := c.global.asker.AskString(fmt.Sprintf("What IP address or DNS name should be used to reach this server? [default=%s]: ", address), address, validateServerAddress)
		if err != nil {
			return err
		}

		serverAddress = util.CanonicalNetworkAddress(serverAddress, shared.HTTPSDefaultPort)
		config.Node.Config["core.https_address"] = serverAddress

		clusterJoin, err := c.global.asker.AskBool("Are you joining an existing cluster? (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}

		if clusterJoin {
			// Existing cluster
			config.Cluster.ServerAddress = serverAddress

			// Root is required to access the certificate files
			if os.Geteuid() != 0 {
				return errors.New("Joining an existing cluster requires root privileges")
			}

			var joinToken *api.ClusterMemberJoinToken

			validJoinToken := func(input string) error {
				j, err := shared.JoinTokenDecode(input)
				if err != nil {
					return fmt.Errorf("Invalid join token: %w", err)
				}

				joinToken = j // Store valid decoded join token
				return nil
			}

			clusterJoinToken, err := c.global.asker.AskString("Please provide join token: ", "", validJoinToken)
			if err != nil {
				return err
			}

			// Set server name from join token
			config.Cluster.ServerName = joinToken.ServerName

			// Attempt to find a working cluster member to use for joining by retrieving the
			// cluster certificate from each address in the join token until we succeed.
			for _, clusterAddress := range joinToken.Addresses {
				config.Cluster.ClusterAddress = util.CanonicalNetworkAddress(clusterAddress, shared.HTTPSDefaultPort)

				// Cluster certificate
				cert, err := shared.GetRemoteCertificate(context.Background(), "https://"+config.Cluster.ClusterAddress, version.UserAgent)
				if err != nil {
					fmt.Printf("Error connecting to existing cluster member %q: %v\n", clusterAddress, err)
					continue
				}

				certDigest := shared.CertFingerprint(cert)
				if joinToken.Fingerprint != certDigest {
					return fmt.Errorf("Certificate fingerprint mismatch between join token and cluster member %q", clusterAddress)
				}

				config.Cluster.ClusterCertificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))

				break // We've found a working cluster member.
			}

			if config.Cluster.ClusterCertificate == "" {
				return errors.New("Unable to connect to any of the cluster members specified in join token")
			}

			// Pass the raw join token.
			config.Cluster.ClusterToken = clusterJoinToken

			// Confirm wiping
			clusterWipeMember, err := c.global.asker.AskBool("All existing data is lost when joining a cluster, continue? (yes/no) [default=no] ", "no")
			if err != nil {
				return err
			}

			if !clusterWipeMember {
				return errors.New("User aborted configuration")
			}

			// Connect to existing cluster
			serverCert, err := util.LoadServerCert(shared.VarPath(""))
			if err != nil {
				return err
			}

			err = cluster.SetupTrust(serverCert, config.Cluster.ClusterPut)
			if err != nil {
				return fmt.Errorf("Failed to setup trust relationship with cluster: %w", err)
			}

			// Now we have setup trust, don't send to server, othwerwise it will try and setup trust
			// again and if using a one-time join token, will fail.
			config.Cluster.ClusterToken = ""

			// Client parameters to connect to the target cluster member.
			args := &lxd.ConnectionArgs{
				TLSClientCert: string(serverCert.PublicKey()),
				TLSClientKey:  string(serverCert.PrivateKey()),
				TLSServerCert: string(config.Cluster.ClusterCertificate),
				UserAgent:     version.UserAgent,
			}

			client, err := lxd.ConnectLXD("https://"+config.Cluster.ClusterAddress, args)
			if err != nil {
				return err
			}

			// Get the list of required member config keys.
			cluster, _, err := client.GetCluster()
			if err != nil {
				return fmt.Errorf("Failed to retrieve cluster information: %w", err)
			}

			for i, config := range cluster.MemberConfig {
				question := "Choose " + config.Description + ": "

				// Allow for empty values.
				configValue, err := c.global.asker.AskString(question, "", validate.Optional())
				if err != nil {
					return err
				}

				cluster.MemberConfig[i].Value = configValue
			}

			config.Cluster.MemberConfig = cluster.MemberConfig
		} else {
			// Ask for server name since no token is provided
			err = askForServerName()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *cmdInit) askMAAS(config *api.InitPreseed) error {
	maas, err := c.global.asker.AskBool("Would you like to connect to a MAAS server? (yes/no) [default=no]: ", "no")
	if err != nil {
		return err
	}

	if !maas {
		return nil
	}

	maasHostname, err := c.global.asker.AskString(fmt.Sprintf("What's the name of this host in MAAS? [default=%s]: ", c.defaultHostname()), c.defaultHostname(), nil)
	if err != nil {
		return err
	}

	if maasHostname != c.defaultHostname() {
		config.Node.Config["maas.machine"] = maasHostname
	}

	config.Node.Config["maas.api.url"], err = c.global.asker.AskString("URL of your MAAS server (e.g. http://1.2.3.4:5240/MAAS): ", "", nil)
	if err != nil {
		return err
	}

	config.Node.Config["maas.api.key"], err = c.global.asker.AskString("API key for your MAAS server: ", "", nil)
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdInit) askNetworking(config *api.InitPreseed, d lxd.InstanceServer) error {
	var err error
	localBridgeCreate := false

	if config.Cluster == nil {
		localBridgeCreate, err = c.global.asker.AskBool("Would you like to create a new local network bridge? (yes/no) [default=yes]: ", "yes")
		if err != nil {
			return err
		}
	}

	if !localBridgeCreate {
		// At this time, only the Ubuntu kernel supports the Fan, detect it
		fanKernel := false
		if shared.PathExists("/proc/sys/kernel/version") {
			content, _ := os.ReadFile("/proc/sys/kernel/version")
			if content != nil && strings.Contains(string(content), "Ubuntu") {
				fanKernel = true
			}
		}

		useExistingInterface, err := c.global.asker.AskBool("Would you like to configure LXD to use an existing bridge or host interface? (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}

		if useExistingInterface {
			networks, err := d.GetNetworks()
			if err != nil {
				return err
			}

			for {
				interfaceName, err := c.global.asker.AskString("Name of the existing bridge or host interface: ", "", nil)
				if err != nil {
					return err
				}

				// Try to find the existing network.
				var network *api.Network
				for _, n := range networks {
					if n.Name == interfaceName {
						network = &n
						break
					}
				}

				if network == nil {
					fmt.Println("The requested interface doesn't exist. Please choose another one.")
					continue
				}

				// Add to the default profile.
				if network.Managed {
					// Add managed network.
					config.Node.Profiles[0].Devices["eth0"] = map[string]string{
						"name":    "eth0",
						"type":    "nic",
						"network": network.Name,
					}
				} else {
					// Add unmanaged network.
					config.Node.Profiles[0].Devices["eth0"] = map[string]string{
						"name":    "eth0",
						"type":    "nic",
						"nictype": "macvlan",
						"parent":  network.Name,
					}

					if network.Type == "bridge" {
						config.Node.Profiles[0].Devices["eth0"]["nictype"] = "bridged"
					}
				}

				if config.Node.Config["maas.api.url"] != nil {
					maasConnect, err := c.global.asker.AskBool("Is this interface connected to your MAAS server? (yes/no) [default=yes]: ", "yes")
					if err != nil {
						return err
					}

					if maasConnect {
						maasSubnetV4, err := c.global.asker.AskString("MAAS IPv4 subnet name for this interface (empty for no subnet): ", "", validate.Optional())
						if err != nil {
							return err
						}

						if maasSubnetV4 != "" {
							config.Node.Profiles[0].Devices["eth0"]["maas.subnet.ipv4"] = maasSubnetV4
						}

						maasSubnetV6, err := c.global.asker.AskString("MAAS IPv6 subnet name for this interface (empty for no subnet): ", "", validate.Optional())
						if err != nil {
							return err
						}

						if maasSubnetV6 != "" {
							config.Node.Profiles[0].Devices["eth0"]["maas.subnet.ipv6"] = maasSubnetV6
						}
					}
				}

				break
			}
		} else if config.Cluster != nil && fanKernel {
			fan, err := c.global.asker.AskBool("Would you like to create a new Fan overlay network? (yes/no) [default=yes]: ", "yes")
			if err != nil {
				return err
			}

			if fan {
				// Define the network
				networkPost := api.InitNetworksProjectPost{}
				networkPost.Name = "lxdfan0"
				networkPost.Project = api.ProjectDefaultName
				networkPost.Config = map[string]string{
					"bridge.mode": "fan",
				}

				// Select the underlay
				networkPost.Config["fan.underlay_subnet"], err = c.global.asker.AskString("What subnet should be used as the Fan underlay? [default=auto]: ", "auto", func(value string) error {
					var err error
					var subnet *net.IPNet

					// Handle auto
					if value == "auto" {
						subnet, _, err = network.DefaultGatewaySubnetV4()
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
						if value == "auto" {
							return fmt.Errorf("The auto-detected underlay (%s) isn't a /16 or /24, please specify manually", subnet.String())
						}

						return errors.New("The underlay subnet must be a /16 or a /24")
					}

					return nil
				})
				if err != nil {
					return err
				}

				// Add the new network
				config.Node.Networks = append(config.Node.Networks, networkPost)

				// Add to the default profile
				config.Node.Profiles[0].Devices["eth0"] = map[string]string{
					"type":    "nic",
					"name":    "eth0",
					"network": "lxdfan0",
				}
			}
		}

		return nil
	}

	for {
		// Define the network
		net := api.InitNetworksProjectPost{}
		net.Config = map[string]string{}
		net.Project = api.ProjectDefaultName

		// Network name
		net.Name, err = c.global.asker.AskString("What should the new bridge be called? [default=lxdbr0]: ", "lxdbr0", func(netName string) error {
			netType, err := network.LoadByType("bridge")
			if err != nil {
				return err
			}

			return netType.ValidateName(netName)
		})
		if err != nil {
			return err
		}

		_, _, err = d.GetNetwork(net.Name)
		if err == nil {
			fmt.Printf("The requested network bridge \"%s\" already exists. Please choose another name.\n", net.Name)
			continue
		}

		// Add to the default profile
		config.Node.Profiles[0].Devices["eth0"] = map[string]string{
			"type":    "nic",
			"name":    "eth0",
			"network": net.Name,
		}

		// IPv4
		net.Config["ipv4.address"], err = c.global.asker.AskString("What IPv4 address should be used? (CIDR subnet notation, “auto” or “none”) [default=auto]: ", "auto", func(value string) error {
			if slices.Contains([]string{"auto", "none"}, value) {
				return nil
			}

			return validate.Optional(validate.IsNetworkAddressCIDRV4)(value)
		})
		if err != nil {
			return err
		}

		if !slices.Contains([]string{"auto", "none"}, net.Config["ipv4.address"]) {
			netIPv4UseNAT, err := c.global.asker.AskBool("Would you like LXD to NAT IPv4 traffic on your bridge? [default=yes]: ", "yes")
			if err != nil {
				return err
			}

			net.Config["ipv4.nat"] = strconv.FormatBool(netIPv4UseNAT)
		}

		// IPv6
		net.Config["ipv6.address"], err = c.global.asker.AskString("What IPv6 address should be used? (CIDR subnet notation, “auto” or “none”) [default=auto]: ", "auto", func(value string) error {
			if slices.Contains([]string{"auto", "none"}, value) {
				return nil
			}

			return validate.Optional(validate.IsNetworkAddressCIDRV6)(value)
		})
		if err != nil {
			return err
		}

		if !slices.Contains([]string{"auto", "none"}, net.Config["ipv6.address"]) {
			netIPv6UseNAT, err := c.global.asker.AskBool("Would you like LXD to NAT IPv6 traffic on your bridge? [default=yes]: ", "yes")
			if err != nil {
				return err
			}

			net.Config["ipv6.nat"] = strconv.FormatBool(netIPv6UseNAT)
		}

		// Add the new network
		config.Node.Networks = append(config.Node.Networks, net)
		break
	}

	return nil
}

func (c *cmdInit) askStorage(config *api.InitPreseed, d lxd.InstanceServer, server *api.Server) error {
	if config.Cluster != nil {
		localStoragePool, err := c.global.asker.AskBool("Do you want to configure a new local storage pool? (yes/no) [default=yes]: ", "yes")
		if err != nil {
			return err
		}

		if localStoragePool {
			err := c.askStoragePool(config, d, server, util.PoolTypeLocal)
			if err != nil {
				return err
			}
		}

		remoteStoragePool, err := c.global.asker.AskBool("Do you want to configure a new remote storage pool? (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}

		if remoteStoragePool {
			err := c.askStoragePool(config, d, server, util.PoolTypeRemote)
			if err != nil {
				return err
			}
		}

		return nil
	}

	storagePool, err := c.global.asker.AskBool("Do you want to configure a new storage pool? (yes/no) [default=yes]: ", "yes")
	if err != nil {
		return err
	}

	if !storagePool {
		return nil
	}

	return c.askStoragePool(config, d, server, util.PoolTypeAny)
}

func (c *cmdInit) askStoragePool(config *api.InitPreseed, d lxd.InstanceServer, server *api.Server, poolType util.PoolType) error {
	// Figure out the preferred storage driver
	availableBackends := util.AvailableStorageDrivers(server.Environment.StorageSupportedDrivers, poolType)

	if len(availableBackends) == 0 {
		if poolType != util.PoolTypeAny {
			return errors.New("No storage backends available")
		}

		return fmt.Errorf("No %s storage backends available", poolType)
	}

	backingFs, err := filesystem.Detect(shared.VarPath())
	if err != nil {
		backingFs = "dir"
	}

	defaultStorage := "dir"
	if backingFs == "btrfs" && slices.Contains(availableBackends, "btrfs") {
		defaultStorage = "btrfs"
	} else if slices.Contains(availableBackends, "zfs") {
		defaultStorage = "zfs"
	} else if slices.Contains(availableBackends, "btrfs") {
		defaultStorage = "btrfs"
	}

	for {
		// Define the pool
		pool := api.StoragePoolsPost{}
		pool.Config = map[string]string{}

		if poolType == util.PoolTypeAny {
			pool.Name, err = c.global.asker.AskString("Name of the new storage pool [default=default]: ", "default", nil)
			if err != nil {
				return err
			}
		} else {
			pool.Name = string(poolType)
		}

		_, _, err := d.GetStoragePool(pool.Name)
		if err == nil {
			if poolType == util.PoolTypeAny {
				fmt.Printf("The requested storage pool \"%s\" already exists. Please choose another name.\n", pool.Name)
				continue
			}

			return fmt.Errorf("The %s storage pool already exists", poolType)
		}

		// Add to the default profile
		if config.Node.Profiles[0].Devices["root"] == nil {
			config.Node.Profiles[0].Devices["root"] = map[string]string{
				"type": "disk",
				"path": "/",
				"pool": pool.Name,
			}
		}

		// Storage backend
		if len(availableBackends) > 1 {
			defaultBackend := defaultStorage
			if poolType == util.PoolTypeRemote {
				if slices.Contains(availableBackends, "ceph") {
					defaultBackend = "ceph"
				} else {
					defaultBackend = availableBackends[0] // Default to first remote driver.
				}
			}

			pool.Driver, err = c.global.asker.AskChoice(fmt.Sprintf("Name of the storage backend to use (%s) [default=%s]: ", strings.Join(availableBackends, ", "), defaultBackend), availableBackends, defaultBackend)
			if err != nil {
				return err
			}
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
			btrfsSubvolume, err := c.global.asker.AskBool(fmt.Sprintf("Would you like to create a new btrfs subvolume under %s? (yes/no) [default=yes]: ", shared.VarPath("")), "yes")
			if err != nil {
				return err
			}

			if btrfsSubvolume {
				pool.Config["source"] = shared.VarPath("storage-pools", pool.Name)
				config.Node.StoragePools = append(config.Node.StoragePools, pool)
				break
			}
		}

		// Optimization for zfs on zfs (when using Ubuntu's bpool/rpool)
		if pool.Driver == "zfs" && backingFs == "zfs" {
			poolName, _ := shared.RunCommandContext(context.TODO(), "zpool", "get", "-H", "-o", "value", "name", "rpool")
			if strings.TrimSpace(poolName) == "rpool" {
				zfsDataset, err := c.global.asker.AskBool("Would you like to create a new zfs dataset under rpool/lxd? (yes/no) [default=yes]: ", "yes")
				if err != nil {
					return err
				}

				if zfsDataset {
					pool.Config["source"] = "rpool/lxd"
					config.Node.StoragePools = append(config.Node.StoragePools, pool)
					break
				}
			}
		}

		poolCreate, err := c.global.asker.AskBool(fmt.Sprintf("Create a new %s pool? (yes/no) [default=yes]: ", strings.ToUpper(pool.Driver)), "yes")
		if err != nil {
			return err
		}

		if poolCreate {
			switch pool.Driver {
			case "ceph":
				// Ask for the name of the cluster
				pool.Config["ceph.cluster_name"], err = c.global.asker.AskString("Name of the existing CEPH cluster [default=ceph]: ", "ceph", nil)
				if err != nil {
					return err
				}

				// Ask for the name of the osd pool
				pool.Config["ceph.osd.pool_name"], err = c.global.asker.AskString("Name of the OSD storage pool [default=lxd]: ", "lxd", nil)
				if err != nil {
					return err
				}

				// Ask for the number of placement groups
				pool.Config["ceph.osd.pg_num"], err = c.global.asker.AskString("Number of placement groups [default=32]: ", "32", nil)
				if err != nil {
					return err
				}

			case "cephfs":
				// Ask for the name of the cluster
				pool.Config["cephfs.cluster_name"], err = c.global.asker.AskString("Name of the existing CEPHfs cluster [default=ceph]: ", "ceph", nil)
				if err != nil {
					return err
				}

				// Ask for the name of the cluster
				pool.Config["source"], err = c.global.asker.AskString("Name of the CEPHfs volume: ", "", nil)
				if err != nil {
					return err
				}

			default:
				useEmptyBlockDev, err := c.global.asker.AskBool("Would you like to use an existing empty block device (e.g. a disk or partition)? (yes/no) [default=no]: ", "no")
				if err != nil {
					return err
				}

				if useEmptyBlockDev {
					pool.Config["source"], err = c.global.asker.AskString("Path to the existing block device: ", "", func(path string) error {
						if !shared.IsBlockdevPath(path) {
							return fmt.Errorf("%q is not a block device", path)
						}

						return nil
					})
					if err != nil {
						return err
					}
				} else {
					st := unix.Statfs_t{}
					err := unix.Statfs(shared.VarPath(), &st)
					if err != nil {
						return fmt.Errorf("Couldn't statfs %s: %w", shared.VarPath(), err)
					}

					/* choose 5 GiB < x < 30GiB, where x is 20% of the disk size */
					defaultSize := max(min(uint64(st.Frsize)*st.Blocks/(1024*1024*1024)/5, 30), 5)

					pool.Config["size"], err = c.global.asker.AskString(
						fmt.Sprintf("Size in GiB of the new loop device (1GiB minimum) [default=%dGiB]: ", defaultSize),
						fmt.Sprintf("%dGiB", defaultSize),
						func(input string) error {
							input = strings.Split(input, "GiB")[0]

							result, err := strconv.ParseInt(input, 10, 64)
							if err != nil {
								return err
							}

							if result < 1 {
								return errors.New("Minimum size is 1GiB")
							}

							return nil
						},
					)
					if err != nil {
						return err
					}

					if !strings.HasSuffix(pool.Config["size"], "GiB") {
						pool.Config["size"] = pool.Config["size"] + "GiB"
					}
				}
			}
		} else {
			if pool.Driver == "ceph" {
				// ask for the name of the cluster
				pool.Config["ceph.cluster_name"], err = c.global.asker.AskString("Name of the existing CEPH cluster [default=ceph]: ", "ceph", nil)
				if err != nil {
					return err
				}

				// ask for the name of the existing pool
				pool.Config["source"], err = c.global.asker.AskString("Name of the existing OSD storage pool [default=lxd]: ", "lxd", nil)
				if err != nil {
					return err
				}

				pool.Config["ceph.osd.pool_name"] = pool.Config["source"]
			} else {
				question := "Name of the existing " + strings.ToUpper(pool.Driver) + " pool or dataset: "
				pool.Config["source"], err = c.global.asker.AskString(question, "", nil)
				if err != nil {
					return err
				}
			}
		}

		if pool.Driver == "lvm" {
			_, err := exec.LookPath("thin_check")
			if err != nil {
				fmt.Print(`
The LVM thin provisioning tools couldn't be found.
LVM can still be used without thin provisioning but this will disable over-provisioning,
increase the space requirements and creation time of images, instances and snapshots.

If you wish to use thin provisioning, abort now, install the tools from your Linux distribution
and make sure that your user can see and run the "thin_check" command before running "lxd init" again.

`)
				lvmContinueNoThin, err := c.global.asker.AskBool("Do you want to continue without thin provisioning? (yes/no) [default=yes]: ", "yes")
				if err != nil {
					return err
				}

				if !lvmContinueNoThin {
					return errors.New("The LVM thin provisioning tools couldn't be found on the system")
				}

				pool.Config["lvm.use_thinpool"] = "false"
			}
		}

		config.Node.StoragePools = append(config.Node.StoragePools, pool)
		break
	}

	return nil
}

func (c *cmdInit) askDaemon(config *api.InitPreseed, server *api.Server) error {
	// Detect lack of uid/gid
	idmapset, err := idmap.DefaultIdmapSet("", "")
	if (err != nil || len(idmapset.Idmap) == 0 || idmapset.Usable() != nil) && shared.RunningInUserNS() {
		fmt.Print(`
We detected that you are running inside an unprivileged container.
This means that unless you manually configured your host otherwise,
you will not have enough uids and gids to allocate to your containers.

LXD can re-use your container's own allocation to avoid the problem.
Doing so makes your nested containers slightly less safe as they could
in theory attack their parent container and gain more privileges than
they otherwise would.

`)

		shareParentAllocation, err := c.global.asker.AskBool("Would you like to have your containers share their parent's allocation? (yes/no) [default=yes]: ", "yes")
		if err != nil {
			return err
		}

		if shareParentAllocation {
			config.Node.Profiles[0].Config["security.privileged"] = "true"
		}
	}

	// Network listener
	if config.Cluster == nil {
		lxdOverNetwork, err := c.global.asker.AskBool("Would you like the LXD server to be available over the network? (yes/no) [default=no]: ", "no")
		if err != nil {
			return err
		}

		if lxdOverNetwork {
			isIPAddress := func(s string) error {
				if s != "all" && net.ParseIP(s) == nil {
					return fmt.Errorf("%q is not an IP address", s)
				}

				return nil
			}

			netAddr, err := c.global.asker.AskString("Address to bind LXD to (not including port) [default=all]: ", "all", isIPAddress)
			if err != nil {
				return err
			}

			if netAddr == "all" {
				netAddr = "::"
			}

			if net.ParseIP(netAddr).To4() == nil {
				netAddr = "[" + netAddr + "]"
			}

			netPort, err := c.global.asker.AskInt(fmt.Sprintf("Port to bind LXD to [default=%d]: ", shared.HTTPSDefaultPort), 1, 65535, strconv.Itoa(shared.HTTPSDefaultPort), func(netPort int64) error {
				address := util.CanonicalNetworkAddressFromAddressAndPort(netAddr, netPort, shared.HTTPSDefaultPort)

				if err == nil {
					if server.Config["cluster.https_address"] == address || server.Config["core.https_address"] == address {
						// We already own the address, just move on.
						return nil
					}
				}

				listener, err := net.Listen("tcp", address)
				if err != nil {
					return fmt.Errorf("Can't bind address %q: %w", address, err)
				}

				_ = listener.Close()
				return nil
			})
			if err != nil {
				return err
			}

			config.Node.Config["core.https_address"] = util.CanonicalNetworkAddressFromAddressAndPort(netAddr, netPort, shared.HTTPSDefaultPort)
		}
	}

	// Ask if the user wants images to be automatically refreshed
	imageStaleRefresh, err := c.global.asker.AskBool("Would you like stale cached images to be updated automatically? (yes/no) [default=yes]: ", "yes")
	if err != nil {
		return err
	}

	if !imageStaleRefresh {
		config.Node.Config["images.auto_update_interval"] = "0"
	}

	return nil
}
