package main

import (
	"archive/tar"
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/osarch"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
	"github.com/canonical/lxd/shared/version"
)

type cmdMigrate struct {
	global *cmdGlobal

	// Instance options.
	flagInstanceName string
	flagInstanceType string
	flagProject      string
	flagProfiles     []string
	flagNoProfiles   bool
	flagStorage      string
	flagStorageSize  string
	flagNetwork      string
	flagMountPaths   []string
	flagConfig       []string
	flagSource       string

	// Target server.
	flagServer   string
	flagToken    string
	flagCertPath string
	flagKeyPath  string

	// Other.
	flagRsyncArgs      string
	flagConversionOpts []string
	flagNonInteractive bool
}

func (c *cmdMigrate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "lxd-migrate"
	cmd.Short = "Physical to instance migration tool"
	cmd.Long = `Description:
  Physical to instance migration tool

  This tool lets you turn any Linux filesystem (including your current one)
  into a LXD instance on a remote LXD host.

  It will setup a clean mount tree made of the root filesystem and any
  additional mount you list, then transfer this through LXD's migration
  API to create a new instance from it.
`
	cmd.RunE = c.run

	// Instance flags.
	cmd.Flags().StringVar(&c.flagInstanceName, "name", "", "Name of the new instance"+"``")
	cmd.Flags().StringVar(&c.flagInstanceType, "type", "", "Type of the instance to create (container or vm)"+"``")
	cmd.Flags().StringVar(&c.flagProject, "project", "", "Project name"+"``")
	cmd.Flags().StringSliceVar(&c.flagProfiles, "profiles", nil, "Profiles to apply on the new instance"+"``")
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, "Create the instance with no profiles applied"+"``")
	cmd.Flags().StringVar(&c.flagStorage, "storage", "", "Storage pool name"+"``")
	cmd.Flags().StringVar(&c.flagStorageSize, "storage-size", "", "Size of the instance's storage volume"+"``")
	cmd.Flags().StringVar(&c.flagNetwork, "network", "", "Network name"+"``")
	cmd.Flags().StringArrayVar(&c.flagMountPaths, "mount-path", nil, "Additional container mount paths"+"``")
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, "Config key/value to apply to the new instance"+"``")
	cmd.Flags().StringVar(&c.flagSource, "source", "", "Path to the root filesystem for containers, or to the block device or disk image file for virtual machines"+"``")

	// Target server.
	cmd.Flags().StringVar(&c.flagServer, "server", "", "Unix or HTTPS URL of the target server"+"``")
	cmd.Flags().StringVar(&c.flagToken, "token", "", "Authentication token for HTTPS remote"+"``")
	cmd.Flags().StringVar(&c.flagCertPath, "cert-path", "", "Trusted certificate path"+"``")
	cmd.Flags().StringVar(&c.flagKeyPath, "key-path", "", "Trusted certificate key path"+"``")

	// Other flags.
	cmd.Flags().StringVar(&c.flagRsyncArgs, "rsync-args", "", "Extra arguments to pass to rsync"+"``")
	cmd.Flags().StringSliceVar(&c.flagConversionOpts, "conversion", []string{"format"}, "Comma-separated list of conversion options to apply. Allowed values are: [format, virtio]")
	cmd.Flags().BoolVar(&c.flagNonInteractive, "non-interactive", false, "Prevent further interaction if migration questions are incomplete"+"``")

	return cmd
}

type cmdMigrateData struct {
	SourcePath   string
	Mounts       []string
	InstanceArgs api.InstancesPost
	Project      string
}

func (c *cmdMigrateData) render() string {
	data := struct {
		Name        string            `yaml:"Name"`
		Project     string            `yaml:"Project"`
		Type        api.InstanceType  `yaml:"Type"`
		Source      string            `yaml:"Source"`
		Mounts      []string          `yaml:"Mounts,omitempty"`
		Profiles    []string          `yaml:"Profiles,omitempty"`
		StoragePool string            `yaml:"Storage pool,omitempty"`
		StorageSize string            `yaml:"Storage volume size,omitempty"`
		Network     string            `yaml:"Network name,omitempty"`
		Config      map[string]string `yaml:"Config,omitempty"`
	}{
		c.InstanceArgs.Name,
		c.Project,
		c.InstanceArgs.Type,
		c.SourcePath,
		c.Mounts,
		c.InstanceArgs.Profiles,
		"",
		"",
		"",
		c.InstanceArgs.Config,
	}

	disk, ok := c.InstanceArgs.Devices["root"]
	if ok {
		data.StoragePool = disk["pool"]

		size, ok := disk["size"]
		if ok {
			data.StorageSize = size
		}
	}

	network, ok := c.InstanceArgs.Devices["eth0"]
	if ok {
		data.Network = network["network"]
	}

	out, err := yaml.Marshal(&data)
	if err != nil {
		return ""
	}

	return string(out)
}

func (c *cmdMigrate) askServer() (lxd.InstanceServer, string, error) {
	var serverURL string
	var err error

	// Ensure trust token is not used along trust certificate and/or its corresponding key.
	if c.flagToken != "" && (c.flagCertPath != "" || c.flagKeyPath != "") {
		return nil, "", errors.New("Authentication token is mutually exclusive with certificate path and key")
	}

	if c.flagNonInteractive || c.flagServer != "" {
		// Try to connect to unix socket if server URL is empty or has a "unix:" prefix.
		if c.flagServer == "" || strings.HasPrefix(c.flagServer, "unix:") {
			path := strings.TrimLeft(strings.TrimPrefix(c.flagServer, "unix:"), "/")
			local, err := c.connectLocal(path)
			if err != nil {
				return nil, "", err
			}

			return local, "", err
		}

		// Otherwise, just parse the provided address.
		serverURL, err = parseURL(c.flagServer)
		if err != nil {
			return nil, "", err
		}
	} else {
		// Suggest connection to local server, if accessible.
		local, err := c.connectLocal("")
		if err == nil {
			useLocal, err := c.global.asker.AskBool("The local LXD server is the target [default=yes]: ", "yes")
			if err != nil {
				return nil, "", err
			}

			if useLocal {
				return local, "", nil
			}
		}

		// Parse server address.
		serverURL, err = c.global.asker.AskString("Please provide LXD server URL: ", "", nil)
		if err != nil {
			return nil, "", err
		}

		serverURL, err = parseURL(serverURL)
		if err != nil {
			return nil, "", err
		}
	}

	args := lxd.ConnectionArgs{
		UserAgent:          "LXD-MIGRATE " + version.Version,
		InsecureSkipVerify: true,
	}

	certificate, err := shared.GetRemoteCertificate(context.Background(), serverURL, args.UserAgent)
	if err != nil {
		return nil, "", fmt.Errorf("Failed to get remote certificate: %w", err)
	}

	digest := shared.CertFingerprint(certificate)

	if !c.flagNonInteractive {
		fmt.Println("Certificate fingerprint:", digest)
		fmt.Print("ok (y/n)? ")

		line, err := shared.ReadStdin()
		if err != nil {
			return nil, "", err
		}

		if len(line) < 1 || !strings.EqualFold(string(line[0]), "y") {
			return nil, "", errors.New("Server certificate rejected by user")
		}

		fmt.Println("")
	}

	server, err := lxd.ConnectLXD(serverURL, &args)
	if err != nil {
		return nil, "", fmt.Errorf("Failed to connect to server: %w", err)
	}

	apiServer, _, err := server.GetServer()
	if err != nil {
		return nil, "", fmt.Errorf("Failed to get server: %w", err)
	}

	type AuthMethod int

	const (
		authMethodTLSCertificate AuthMethod = iota
		authMethodTLSTemporaryCertificate
		authMethodTLSCertificateToken
	)

	// TLS is always available for LXD servers
	var availableAuthMethods []AuthMethod
	var authMethod AuthMethod
	var authType string
	var certPath string
	var keyPath string
	var token string

	if c.flagToken != "" {
		token = c.flagToken
		authMethod = authMethodTLSCertificateToken

		_, err = shared.CertificateTokenDecode(token)
		if err != nil {
			return nil, "", fmt.Errorf("Failed to decode certificate token: %w", err)
		}
	} else if c.flagKeyPath != "" || c.flagCertPath != "" {
		if c.flagKeyPath == "" {
			return nil, "", errors.New("Certificate path is required when certificate key is set")
		}

		if c.flagCertPath == "" {
			return nil, "", errors.New("Certificate key path is required when certificate path is set")
		}

		certPath = c.flagCertPath
		keyPath = c.flagKeyPath
	} else {
		if c.flagNonInteractive {
			return nil, "", errors.New("Authentication token is required for HTTPS remote in non-interactive mode")
		}

		i := 1

		if slices.Contains(apiServer.AuthMethods, api.AuthenticationMethodTLS) {
			fmt.Printf("%d) Use a certificate token\n", i)
			availableAuthMethods = append(availableAuthMethods, authMethodTLSCertificateToken)
			i++
			fmt.Printf("%d) Use an existing TLS authentication certificate\n", i)
			availableAuthMethods = append(availableAuthMethods, authMethodTLSCertificate)
			i++
			fmt.Printf("%d) Generate a temporary TLS authentication certificate\n", i)
			availableAuthMethods = append(availableAuthMethods, authMethodTLSTemporaryCertificate)
		}

		if len(apiServer.AuthMethods) > 1 || slices.Contains(apiServer.AuthMethods, api.AuthenticationMethodTLS) {
			authMethodInt, err := c.global.asker.AskInt("Please pick an authentication mechanism above: ", 1, int64(i), "", nil)
			if err != nil {
				return nil, "", err
			}

			authMethod = availableAuthMethods[authMethodInt-1]
		}

		switch authMethod {
		case authMethodTLSCertificate:
			certPath, err = c.global.asker.AskString("Please provide the certificate path: ", "", func(path string) error {
				if !shared.PathExists(path) {
					return errors.New("File does not exist")
				}

				return nil
			})
			if err != nil {
				return nil, "", err
			}

			keyPath, err = c.global.asker.AskString("Please provide the keyfile path: ", "", func(path string) error {
				if !shared.PathExists(path) {
					return errors.New("File does not exist")
				}

				return nil
			})
			if err != nil {
				return nil, "", err
			}

		case authMethodTLSCertificateToken:
			token, err = c.global.asker.AskString("Please provide the certificate token: ", "", func(token string) error {
				_, err := shared.CertificateTokenDecode(token)
				if err != nil {
					return err
				}

				return nil
			})
			if err != nil {
				return nil, "", err
			}
		}
	}

	switch authMethod {
	case authMethodTLSCertificate, authMethodTLSTemporaryCertificate, authMethodTLSCertificateToken:
		authType = api.AuthenticationMethodTLS
	}

	return c.connectTarget(serverURL, certPath, keyPath, authType, token)
}

// newMigrateData creates a new migration configuration from the provided flags. The configuration is
// validated and an error is returned if any of the flags contain an invalid value. A server connection
// is required for some of the validations, such as checking if the instance name is available.
func (c *cmdMigrate) newMigrateData(server lxd.InstanceServer) (*cmdMigrateData, error) {
	config := &cmdMigrateData{}
	config.InstanceArgs.Config = map[string]string{}
	config.InstanceArgs.Devices = map[string]map[string]string{}
	config.InstanceArgs.Source = api.InstanceSource{
		Type:              api.SourceTypeConversion,
		Mode:              "push",
		ConversionOptions: c.flagConversionOpts,
	}

	// If server does not support conversion, fallback to migration.
	// Migration will move the image to the server and import it as
	// LXD instance. This means that images of different formats,
	// such as VMDK and QCow2, will not work.
	if !server.HasExtension("instance_import_conversion") {
		config.InstanceArgs.Source.Type = api.SourceTypeMigration
	}

	// Parse instance type from a flag.
	if c.flagInstanceType != "" {
		switch c.flagInstanceType {
		case "container":
			config.InstanceArgs.Type = api.InstanceTypeContainer
		case "vm":
			config.InstanceArgs.Type = api.InstanceTypeVM
		default:
			return nil, fmt.Errorf("Invalid instance type %q: Valid values are [%s]", c.flagInstanceType, strings.Join([]string{"container", "vm"}, ", "))
		}
	}

	// Determine project from flags.
	if c.flagProject != "" {
		projectNames, err := server.GetProjectNames()
		if err != nil {
			return nil, err
		}

		if !slices.Contains(projectNames, c.flagProject) {
			return nil, fmt.Errorf("Project %q does not exist", c.flagProject)
		}

		config.Project = c.flagProject
		server = server.UseProject(config.Project)
	}

	// Parse instance name from a flag.
	if c.flagInstanceName != "" {
		instanceNames, err := server.GetInstanceNames(api.InstanceTypeAny)
		if err != nil {
			return nil, err
		}

		if slices.Contains(instanceNames, c.flagInstanceName) {
			return nil, fmt.Errorf("Instance %q already exists", c.flagInstanceName)
		}

		config.InstanceArgs.Name = c.flagInstanceName
	}

	// Parse source path from a flag.
	if c.flagSource != "" {
		err := c.checkSource(c.flagSource, config.InstanceArgs.Type, config.InstanceArgs.Source.Type)
		if err != nil {
			return nil, fmt.Errorf("Invalid source path %q: %w", c.flagSource, err)
		}

		config.SourcePath = c.flagSource
	}

	// Configure profiles from flags.
	if c.flagNoProfiles {
		config.InstanceArgs.Profiles = []string{}
	} else if len(c.flagProfiles) > 0 {
		profileNames, err := server.GetProfileNames()
		if err != nil {
			return nil, err
		}

		for _, profile := range c.flagProfiles {
			if !slices.Contains(profileNames, profile) {
				return nil, fmt.Errorf("Profile %q not found", profile)
			}
		}

		config.InstanceArgs.Profiles = c.flagProfiles
	} else {
		config.InstanceArgs.Profiles = []string{"default"}
	}

	// Parse instance config from flags.
	if len(c.flagConfig) > 0 {
		for _, entry := range c.flagConfig {
			key, value, found := strings.Cut(entry, "=")
			if !found {
				return nil, fmt.Errorf("Invalid configuration entry: Entry %q is not in key=value format", entry)
			}

			config.InstanceArgs.Config[key] = value
		}
	}

	// Configure root storage disk from flags.
	if c.flagStorage != "" {
		storagePools, err := server.GetStoragePoolNames()
		if err != nil {
			return nil, err
		}

		if len(storagePools) == 0 {
			return nil, errors.New("No storage pools available")
		}

		if !slices.Contains(storagePools, c.flagStorage) {
			return nil, fmt.Errorf("Storage pool %q not found", c.flagStorage)
		}

		config.InstanceArgs.Devices["root"] = map[string]string{
			"type": "disk",
			"pool": c.flagStorage,
			"path": "/",
		}

		if c.flagStorageSize != "" {
			_, err := units.ParseByteSizeString(c.flagStorageSize)
			if err != nil {
				return nil, err
			}

			config.InstanceArgs.Devices["root"]["size"] = c.flagStorageSize
		}
	}

	// Configure instance NIC connected to network from a flag.
	if c.flagNetwork != "" {
		networks, err := server.GetNetworkNames()
		if err != nil {
			return nil, err
		}

		if !slices.Contains(networks, c.flagNetwork) {
			return nil, fmt.Errorf("Network %q not found", c.flagNetwork)
		}

		config.InstanceArgs.Devices["eth0"] = map[string]string{
			"name":    "eth0",
			"type":    "nic",
			"network": c.flagNetwork,
		}
	}

	// Configure additional mounts for containers.
	if len(c.flagMountPaths) > 0 {
		if config.InstanceArgs.Type != "" && config.InstanceArgs.Type != api.InstanceTypeContainer {
			return nil, errors.New("Additional mount paths are supported only for containers")
		}

		for _, path := range c.flagMountPaths {
			if !shared.PathExists(path) {
				return nil, fmt.Errorf("Invalid mount path %q: Path does not exist", path)
			}

			config.Mounts = append(config.Mounts, path)
		}
	}

	return config, nil
}

// runInteractive populates the migration request by interacting with the user. If any value is already
// provided using flags, the corresponding questions are skipped.
func (c *cmdMigrate) runInteractive(config *cmdMigrateData, server lxd.InstanceServer) error {
	var err error

	// Instance type.
	if config.InstanceArgs.Type == "" {
		instanceType, err := c.global.asker.AskInt("Would you like to create a container (1) or virtual-machine (2)?: ", 1, 2, "1", nil)
		if err != nil {
			return err
		}

		switch instanceType {
		case 1:
			config.InstanceArgs.Type = api.InstanceTypeContainer
		case 2:
			config.InstanceArgs.Type = api.InstanceTypeVM
		}
	}

	// As soon as we know the instance type, we can check if additional mount paths are supported.
	// This applies only in case if additional mounts were configured using flags.
	if len(config.Mounts) > 0 && config.InstanceArgs.Type != api.InstanceTypeContainer {
		return errors.New("Additional mount paths are supported only for containers")
	}

	// Project.
	if config.Project == "" {
		config.Project = "default"

		projectNames, err := server.GetProjectNames()
		if err != nil {
			return err
		}

		if len(projectNames) > 1 {
			project, err := c.global.asker.AskChoice("Project to create the instance in [default=default]: ", projectNames, "default")
			if err != nil {
				return err
			}

			config.Project = project
		}
	}

	server = server.UseProject(config.Project)

	// Instance name.
	if config.InstanceArgs.Name == "" {
		instanceNames, err := server.GetInstanceNames(api.InstanceTypeAny)
		if err != nil {
			return err
		}

		for {
			instanceName, err := c.global.asker.AskString("Name of the new instance: ", "", nil)
			if err != nil {
				return err
			}

			// Validate instance name.
			err = instancetype.ValidName(instanceName, false)
			if err != nil {
				fmt.Println(err)
				continue
			}

			// Ensure instance name is not already used.
			if slices.Contains(instanceNames, instanceName) {
				fmt.Printf("Instance %q already exists\n", instanceName)
				continue
			}

			config.InstanceArgs.Name = instanceName
			break
		}
	}

	if config.SourcePath == "" {
		question := "Please provide the path to a root filesystem: "
		if config.InstanceArgs.Type == api.InstanceTypeVM {
			question = "Please provide the path to the block device or disk image file: "
		}

		config.SourcePath, err = c.global.asker.AskString(question, "", func(s string) error {
			return c.checkSource(s, config.InstanceArgs.Type, config.InstanceArgs.Source.Type)
		})
		if err != nil {
			return err
		}
	}

	// Ask VM supports the secureboot. In non-interactive mode, security.secureboot can be
	// configured using --config flag.
	if !c.flagNonInteractive && config.InstanceArgs.Type == api.InstanceTypeVM {
		architectureName, _ := osarch.ArchitectureGetLocal()

		if slices.Contains([]string{"x86_64", "aarch64"}, architectureName) {
			hasSecureBoot, err := c.global.asker.AskBool("Does the VM support UEFI Secure Boot? [default=no]: ", "no")
			if err != nil {
				return err
			}

			if !hasSecureBoot {
				config.InstanceArgs.Config["security.secureboot"] = "false"
			}
		}
	}

	// Additional mounts for containers
	if config.InstanceArgs.Type == api.InstanceTypeContainer {
		addMounts, err := c.global.asker.AskBool("Do you want to add additional filesystem mounts? [default=no]: ", "no")
		if err != nil {
			return err
		}

		if addMounts {
			for {
				path, err := c.global.asker.AskString("Please provide the filesystem mount path [empty value to continue]: ", "", func(s string) error {
					if s != "" {
						if shared.PathExists(s) {
							return nil
						}

						return errors.New("Path does not exist")
					}

					return nil
				})
				if err != nil {
					return err
				}

				if path == "" {
					break
				}

				config.Mounts = append(config.Mounts, path)
			}
		}
	}

	for {
		fmt.Println("\nInstance to be created:")

		scanner := bufio.NewScanner(strings.NewReader(config.render()))
		for scanner.Scan() {
			fmt.Printf("  %s\n", scanner.Text())
		}

		fmt.Print(`
Additional overrides can be applied at this stage:
1) Begin the migration with the above configuration
2) Override profile list
3) Set additional configuration options
4) Change instance storage pool or volume size
5) Change instance network

`)

		choice, err := c.global.asker.AskInt("Please pick one of the options above [default=1]: ", 1, 5, "1", nil)
		if err != nil {
			return err
		}

		switch choice {
		case 1:
			return nil
		case 2:
			err = c.askProfiles(server, config)
		case 3:
			err = c.askConfig(config)
		case 4:
			err = c.askStorage(server, config)
		case 5:
			err = c.askNetwork(server, config)
		}

		if err != nil {
			fmt.Println(err)
		}
	}
}

func (c *cmdMigrate) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if os.Geteuid() != 0 {
		return errors.New("This tool must be run as root")
	}

	// Check conversion options.
	supportedConversionOptions := []string{"format", "virtio"}
	for _, opt := range c.flagConversionOpts {
		if !slices.Contains(supportedConversionOptions, opt) {
			return fmt.Errorf("Unsupported conversion option %q, supported conversion options are %v", opt, supportedConversionOptions)
		}
	}

	// Check the required flags in non-interactive mode.
	if c.flagNonInteractive {
		if c.flagInstanceType == "" {
			return errors.New("Instance type is required in non-interactive mode")
		}

		if c.flagSource == "" {
			return errors.New("Source path is required in non-interactive mode")
		}
	}

	// Check instance type.
	if c.flagInstanceType != "" && !slices.Contains([]string{"container", "vm"}, c.flagInstanceType) {
		return fmt.Errorf("Invalid instance type %q: Valid values are [%s]", c.flagInstanceType, strings.Join([]string{"container", "vm"}, ", "))
	}

	// Check instance name.
	if c.flagInstanceName != "" {
		err := instancetype.ValidName(c.flagInstanceName, false)
		if err != nil {
			return err
		}
	}

	// Check source path. This is only precheck, as we cannot know the whether
	// conversion is supported until the connection with the server is established.
	if c.flagSource != "" {
		err := c.checkSource(c.flagSource, "", "")
		if err != nil {
			return fmt.Errorf("Invalid source path %q: %w", c.flagSource, err)
		}
	}

	// Ensure no-profiles and profiles flags are not used together.
	if c.flagNoProfiles && len(c.flagProfiles) > 0 {
		return errors.New("Flags --no-profiles and --profiles are mutually exclusive")
	}

	_, err := exec.LookPath("rsync")
	if err != nil {
		return err
	}

	_, err = exec.LookPath("file")
	if err != nil {
		return err
	}

	// Server
	server, clientFingerprint, err := c.askServer()
	if err != nil {
		return err
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-sigChan

		if clientFingerprint != "" {
			_ = server.DeleteCertificate(clientFingerprint)
		}

		cancel()

		// The following nolint directive ignores the "deep-exit" rule of the revive linter.
		// We should be exiting cleanly by passing the above context into each invoked method and checking for
		// cancellation. Unfortunately our client methods do not accept a context argument.
		os.Exit(1) //nolint:revive
	}()

	if clientFingerprint != "" {
		defer func() { _ = server.DeleteCertificate(clientFingerprint) }()
	}

	config, err := c.newMigrateData(server)
	if err != nil {
		return err
	}

	if c.flagNonInteractive {
		// In non-interactive mode, print the instance to be created and continue with the migration.
		fmt.Println("\nInstance to be created:")
		scanner := bufio.NewScanner(strings.NewReader(config.render()))
		for scanner.Scan() {
			fmt.Printf("  %s\n", scanner.Text())
		}
	} else {
		// Otherwise, run in interactive mode where user is asked for missing information
		// and given the opportunity to review and modify the instance configuration.
		err = c.runInteractive(config, server)
		if err != nil {
			return err
		}
	}

	if config.Project != "" {
		server = server.UseProject(config.Project)
	}

	if config.InstanceArgs.Type != api.InstanceTypeVM && len(config.InstanceArgs.Source.ConversionOptions) > 0 {
		fmt.Printf("Instance type %q does not support conversion options. Ignored conversion options: %v\n", config.InstanceArgs.Type, config.InstanceArgs.Source.ConversionOptions)
		config.InstanceArgs.Source.ConversionOptions = []string{}
	}

	config.Mounts = append(config.Mounts, config.SourcePath)

	// Get and sort the mounts
	sort.Strings(config.Mounts)

	// Create the mount namespace and ensure we're not moved around
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Unshare a new mntns so our mounts don't leak
	err = unix.Unshare(unix.CLONE_NEWNS)
	if err != nil {
		return fmt.Errorf("Failed to unshare mount namespace: %w", err)
	}

	// Prevent mount propagation back to initial namespace
	err = unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, "")
	if err != nil {
		return fmt.Errorf("Failed to disable mount propagation: %w", err)
	}

	// Create the temporary directory to be used for the mounts
	path, err := os.MkdirTemp("", "lxd-migrate_mount_")
	if err != nil {
		return err
	}

	// Automatically clean-up the temporary path on exit
	defer func(path string) {
		_ = unix.Unmount(path, unix.MNT_DETACH)
		_ = os.Remove(path)
	}(path)

	var fullPath string

	if config.InstanceArgs.Type == api.InstanceTypeContainer {
		// Create the rootfs directory
		fullPath = path + "/rootfs"

		err = os.Mkdir(fullPath, 0755)
		if err != nil {
			return err
		}

		// Setup the source (mounts)
		err = setupSource(fullPath, config.Mounts)
		if err != nil {
			return fmt.Errorf("Failed to setup the source: %w", err)
		}
	} else {
		isImageTypeRaw, err := isImageTypeRaw(config.SourcePath)
		if err != nil {
			return err
		}

		// If image type is raw, formatting is not required.
		if isImageTypeRaw && slices.Contains(config.InstanceArgs.Source.ConversionOptions, "format") {
			fmt.Println(`Formatting is not required for images of type raw. Ignoring conversion option "format".`)
			config.InstanceArgs.Source.ConversionOptions = shared.RemoveElementsFromSlice(config.InstanceArgs.Source.ConversionOptions, "format")
		}

		fullPath = path
		target := filepath.Join(path, "root.img")

		err = os.WriteFile(target, nil, 0644)
		if err != nil {
			return fmt.Errorf("Failed to create %q: %w", target, err)
		}

		// Mount the path
		err = unix.Mount(config.SourcePath, target, "none", unix.MS_BIND, "")
		if err != nil {
			return fmt.Errorf("Failed to mount %s: %w", config.SourcePath, err)
		}

		// Make it read-only
		err = unix.Mount("", target, "none", unix.MS_BIND|unix.MS_RDONLY|unix.MS_REMOUNT, "")
		if err != nil {
			return fmt.Errorf("Failed to make %s read-only: %w", config.SourcePath, err)
		}

		// In conversion mode, server expects the volume size hint in the request.
		if config.InstanceArgs.Source.Type == api.SourceTypeConversion {
			size, err := block.DiskSizeBytes(target)
			if err != nil {
				return err
			}

			config.InstanceArgs.Source.SourceDiskSize = size
		}
	}

	// System architecture
	architectureName, err := osarch.ArchitectureGetLocal()
	if err != nil {
		return err
	}

	config.InstanceArgs.Architecture = architectureName

	revert := revert.New()
	defer revert.Fail()

	// Create the instance
	op, err := server.CreateInstance(config.InstanceArgs)
	if err != nil {
		return err
	}

	revert.Add(func() {
		_, _ = server.DeleteInstance(config.InstanceArgs.Name)
	})

	progressPrefix := "Transferring instance: %s"
	if config.InstanceArgs.Source.Type == api.SourceTypeConversion {
		// In conversion mode, progress prefix is determined on the server side.
		progressPrefix = "%s"
	}

	progress := cli.ProgressRenderer{Format: progressPrefix}
	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	if config.InstanceArgs.Source.Type == api.SourceTypeConversion {
		err = transferRootDiskForConversion(ctx, op, fullPath, c.flagRsyncArgs, config.InstanceArgs.Type)
	} else {
		err = transferRootDiskForMigration(ctx, op, fullPath, c.flagRsyncArgs, config.InstanceArgs.Type)
	}

	if err != nil {
		return err
	}

	progress.Done(fmt.Sprintf("Instance %s successfully created", config.InstanceArgs.Name))
	revert.Success()

	return nil
}

func (c *cmdMigrate) askProfiles(server lxd.InstanceServer, config *cmdMigrateData) error {
	profileNames, err := server.GetProfileNames()
	if err != nil {
		return err
	}

	profiles, err := c.global.asker.AskString("Which profiles do you want to apply to the instance? (space separated) [default=default, \"-\" for none]: ", "default", func(s string) error {
		// This indicates that no profiles should be applied.
		if s == "-" {
			return nil
		}

		profiles := strings.SplitSeq(s, " ")

		for profile := range profiles {
			if !slices.Contains(profileNames, profile) {
				return fmt.Errorf("Unknown profile %q", profile)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if profiles != "-" {
		config.InstanceArgs.Profiles = strings.Split(profiles, " ")
	}

	return nil
}

func (c *cmdMigrate) askConfig(config *cmdMigrateData) error {
	configs, err := c.global.asker.AskString("Please specify config keys and values (key=value ...): ", "", func(s string) error {
		if s == "" {
			return nil
		}

		for entry := range strings.SplitSeq(s, " ") {
			if !strings.Contains(entry, "=") {
				return fmt.Errorf("Bad key=value configuration: %v", entry)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	for entry := range strings.SplitSeq(configs, " ") {
		key, value, _ := strings.Cut(entry, "=")
		config.InstanceArgs.Config[key] = value
	}

	return nil
}

func (c *cmdMigrate) askStorage(server lxd.InstanceServer, config *cmdMigrateData) error {
	storagePools, err := server.GetStoragePoolNames()
	if err != nil {
		return err
	}

	if len(storagePools) == 0 {
		return errors.New("No storage pools available")
	}

	storagePool, err := c.global.asker.AskChoice("Please provide the storage pool to use: ", storagePools, "")
	if err != nil {
		return err
	}

	config.InstanceArgs.Devices["root"] = map[string]string{
		"type": "disk",
		"pool": storagePool,
		"path": "/",
	}

	changeStorageSize, err := c.global.asker.AskBool("Do you want to change the storage volume size? [default=no]: ", "no")
	if err != nil {
		return err
	}

	if changeStorageSize {
		size, err := c.global.asker.AskString("Please specify the storage volume size: ", "", func(s string) error {
			_, err := units.ParseByteSizeString(s)
			return err
		})
		if err != nil {
			return err
		}

		config.InstanceArgs.Devices["root"]["size"] = size
	}

	return nil
}

func (c *cmdMigrate) askNetwork(server lxd.InstanceServer, config *cmdMigrateData) error {
	networks, err := server.GetNetworkNames()
	if err != nil {
		return err
	}

	network, err := c.global.asker.AskChoice("Please specify the network to use for the instance: ", networks, "")
	if err != nil {
		return err
	}

	config.InstanceArgs.Devices["eth0"] = map[string]string{
		"name":    "eth0",
		"type":    "nic",
		"network": network,
	}

	return nil
}

// checkSource checks if the source path is valid and can be used for migration.
// Source path can represent a disk, image, or partition.
func (c *cmdMigrate) checkSource(path string, instanceType api.InstanceType, migrationMode string) error {
	if !shared.PathExists(path) {
		return errors.New("Path does not exist")
	}

	if instanceType == api.InstanceTypeVM && migrationMode == api.SourceTypeMigration {
		isImageTypeRaw, err := isImageTypeRaw(path)
		if err != nil {
			return err
		}

		if !isImageTypeRaw {
			return errors.New("Source disk format cannot be converted by server. Source disk should be in raw format")
		}
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}

	defer file.Close()

	// Ensure the source file is not a tarball.
	_, err = tar.NewReader(file).Next()
	if err == nil {
		return errors.New("Source cannot be a tar archive or OVA file")
	}

	return nil
}
