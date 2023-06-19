package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func lxdIsConfigured(client lxd.InstanceServer) (bool, error) {
	// Look for networks.
	networks, err := client.GetNetworkNames()
	if err != nil {
		return false, fmt.Errorf("Failed to list networks: %w", err)
	}

	if !shared.StringInSlice("lxdbr0", networks) {
		// Couldn't find lxdbr0.
		return false, nil
	}

	// Look for storage pools.
	pools, err := client.GetStoragePoolNames()
	if err != nil {
		return false, fmt.Errorf("Failed to list storage pools: %w", err)
	}

	if !shared.StringInSlice("default", pools) {
		// No storage pool found.
		return false, nil
	}

	return true, nil
}

func lxdInitialConfiguration(client lxd.InstanceServer) error {
	// Load current server config.
	info, _, err := client.GetServer()
	if err != nil {
		return fmt.Errorf("Failed to get server info: %w", err)
	}

	availableBackends := util.AvailableStorageDrivers(info.Environment.StorageSupportedDrivers, util.PoolTypeLocal)

	// Load the default profile.
	profile, profileEtag, err := client.GetProfile("default")
	if err != nil {
		return fmt.Errorf("Failed to load default profile: %w", err)
	}

	// Look for storage pools.
	pools, err := client.GetStoragePools()
	if err != nil {
		return fmt.Errorf("Failed to list storage pools: %w", err)
	}

	if len(pools) == 0 {
		pool := api.StoragePoolsPost{}
		pool.Config = map[string]string{}
		pool.Name = "default"

		// Check if ZFS supported.
		if shared.StringInSlice("zfs", availableBackends) {
			pool.Driver = "zfs"

			// Check if zsys.
			poolName, _ := shared.RunCommand("zpool", "get", "-H", "-o", "value", "name", "rpool")
			if strings.TrimSpace(poolName) == "rpool" {
				pool.Config["source"] = "rpool/lxd"
			}
		} else {
			// Fallback to dir backend.
			pool.Driver = "dir"
		}

		// Create the storage pool.
		err := client.CreateStoragePool(pool)
		if err != nil {
			return fmt.Errorf("Failed to create storage pool: %w", err)
		}

		// Add to default profile in default project.
		profile.Devices["root"] = map[string]string{
			"type": "disk",
			"pool": "default",
			"path": "/",
		}
	}

	// Look for networks.
	networks, err := client.GetNetworks()
	if err != nil {
		return fmt.Errorf("Failed to list networks: %w", err)
	}

	found := false
	for _, network := range networks {
		if network.Managed {
			found = true
			break
		}
	}

	if !found {
		// Create lxdbr0.
		network := api.NetworksPost{}
		network.Config = map[string]string{}
		network.Type = "bridge"
		network.Name = "lxdbr0"

		err := client.CreateNetwork(network)
		if err != nil {
			return fmt.Errorf("Failed to create network: %w", err)
		}

		// Add to default profile in default project.
		profile.Devices["eth0"] = map[string]string{
			"type":    "nic",
			"network": "lxdbr0",
			"name":    "eth0",
		}
	}

	// Update the default profile.
	err = client.UpdateProfile("default", profile.Writable(), profileEtag)
	if err != nil {
		return fmt.Errorf("Failed to update default profile: %w", err)
	}

	return nil
}

func lxdSetupUser(uid uint32) error {
	projectName := fmt.Sprintf("user-%d", uid)
	networkName := fmt.Sprintf("lxdbr-%d", uid)
	userPath := filepath.Join("users", fmt.Sprintf("%d", uid))

	// User account.
	out, err := shared.RunCommand("getent", "passwd", fmt.Sprintf("%d", uid))
	if err != nil {
		return fmt.Errorf("Failed to retrieve user information: %w", err)
	}

	pw := strings.Split(out, ":")
	if len(pw) != 7 {
		return fmt.Errorf("Invalid user entry")
	}

	// Setup reverter.
	revert := revert.New()
	defer revert.Fail()

	// Create certificate directory.
	err = os.MkdirAll(userPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed to create user directory: %w", err)
	}

	revert.Add(func() { _ = os.RemoveAll(userPath) })

	// Generate certificate.
	err = shared.FindOrGenCert(filepath.Join(userPath, "client.crt"), filepath.Join(userPath, "client.key"), true, false)
	if err != nil {
		return fmt.Errorf("Failed to generate user certificate: %w", err)
	}

	// Connect to LXD.
	client, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return fmt.Errorf("Unable to connect to LXD: %w", err)
	}

	_, _, _ = client.GetServer()

	// Setup the project (with restrictions).
	projects, err := client.GetProjectNames()
	if err != nil {
		return fmt.Errorf("Unable to retrieve project list: %w", err)
	}

	if !shared.StringInSlice(projectName, projects) {
		// Create the project.
		err := client.CreateProject(api.ProjectsPost{
			Name: projectName,
			ProjectPut: api.ProjectPut{
				Description: fmt.Sprintf("User restricted project for %q (%s)", pw[0], pw[2]),
				Config: map[string]string{
					"features.images":               "true",
					"features.networks":             "false",
					"features.networks.zones":       "true",
					"features.profiles":             "true",
					"features.storage.volumes":      "true",
					"features.storage.buckets":      "true",
					"restricted":                    "true",
					"restricted.containers.nesting": "allow",
					"restricted.devices.disk":       "allow",
					"restricted.devices.disk.paths": pw[5],
					"restricted.devices.gpu":        "allow",
					"restricted.idmap.uid":          pw[2],
					"restricted.idmap.gid":          pw[3],
					"restricted.networks.access":    networkName,
				},
			},
		})
		if err != nil {
			return fmt.Errorf("Unable to create project: %w", err)
		}

		revert.Add(func() { _ = client.DeleteProject(projectName) })
	}

	// Parse the certificate.
	x509Cert, err := shared.ReadCert(filepath.Join(userPath, "client.crt"))
	if err != nil {
		return fmt.Errorf("Unable to read user certificate: %w", err)
	}

	// Add the certificate to the trust store.
	err = client.CreateCertificate(api.CertificatesPost{
		CertificatePut: api.CertificatePut{
			Name:        fmt.Sprintf("lxd-user-%d", uid),
			Type:        "client",
			Restricted:  true,
			Projects:    []string{projectName},
			Certificate: base64.StdEncoding.EncodeToString(x509Cert.Raw),
		},
	})
	if err != nil {
		return fmt.Errorf("Unable to add user certificate: %w", err)
	}

	revert.Add(func() { _ = client.DeleteCertificate(shared.CertFingerprint(x509Cert)) })

	// Create user-specific bridge.
	network := api.NetworksPost{}
	network.Config = map[string]string{}
	network.Type = "bridge"
	network.Name = networkName
	network.Description = fmt.Sprintf("Network for user restricted project user-%s", projectName)

	err = client.CreateNetwork(network)
	if err != nil {
		return fmt.Errorf("Failed to create network: %w", err)
	}

	// Setup default profile.
	err = client.UseProject(projectName).UpdateProfile("default", api.ProfilePut{
		Description: "Default LXD profile",
		Config: map[string]string{
			"raw.idmap": fmt.Sprintf("uid %s %s\ngid %s %s", pw[2], pw[2], pw[3], pw[3]),
		},
		Devices: map[string]map[string]string{
			"root": {
				"type": "disk",
				"path": "/",
				"pool": "default",
			},
			"eth0": {
				"type":    "nic",
				"name":    "eth0",
				"network": networkName,
			},
		},
	}, "")
	if err != nil {
		return fmt.Errorf("Unable to update the default profile: %w", err)
	}

	revert.Success()
	return nil
}
