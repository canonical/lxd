package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/termios"
)

type cmdInit struct {
	global *cmdGlobal

	flagConfig        []string
	flagDevice        []string
	flagEphemeral     bool
	flagNetwork       string
	flagProfile       []string
	flagStorage       string
	flagTarget        string
	flagTargetProject string
	flagType          string
	flagNoProfiles    bool
	flagEmpty         bool
	flagVM            bool
}

func (c *cmdInit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("init", "[<registry|remote>:]<image> [<remote>:][<name>]")
	cmd.Short = "Create instances from images"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc init ubuntu:24.04 u1
    Create a container (but do not start it)

lxc init ubuntu:24.04 u1 < config.yaml
    Create a container with configuration from config.yaml

lxc init ubuntu:24.04 v1 --vm -c limits.cpu=4 -c limits.memory=4GiB
    Create a virtual machine with 4 vCPUs and 4GiB of RAM

lxc init ubuntu:24.04 v1 --vm -c limits.cpu=2 -c limits.memory=8GiB -d root,size=32GiB
    Create a virtual machine with 2 vCPUs, 8GiB of RAM and a root disk of 32GiB

Note: The --project flag sets the project for both the image remote and the instance remote.
If the image remote is a public remote (e.g. simplestreams) then this project is ignored by the image remote.
If the image remote is another LXD server, specify the source project for the image remote 
with --project and the instance remote with --target-project (if different from --project).

If the destination LXD server supports image registries, the source image
must be from an image registry or a local store.`)

	cmd.RunE = c.run
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, cli.FormatStringFlagLabel("Config key/value to apply to the new instance"))
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, cli.FormatStringFlagLabel("Profile to apply to the new instance"))
	cmd.Flags().StringArrayVarP(&c.flagDevice, "device", "d", nil, cli.FormatStringFlagLabel("New key/value to apply to a specific device"))
	cmd.Flags().BoolVarP(&c.flagEphemeral, "ephemeral", "e", false, "Ephemeral instance")
	cmd.Flags().StringVarP(&c.flagNetwork, "network", "n", "", cli.FormatStringFlagLabel("Network name"))
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", cli.FormatStringFlagLabel("Storage pool name"))
	cmd.Flags().StringVarP(&c.flagType, "type", "t", "", cli.FormatStringFlagLabel("Instance type"))
	cmd.Flags().StringVar(&c.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", cli.FormatStringFlagLabel("Project to create the instance in (if different from --project)"))
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, "Create the instance with no profiles applied")
	cmd.Flags().BoolVar(&c.flagEmpty, "empty", false, "Create an empty instance")
	cmd.Flags().BoolVar(&c.flagVM, "vm", false, "Create a virtual machine")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 0 {
			return c.global.cmpImages(toComplete, false)
		}

		return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
	}

	_ = cmd.RegisterFlagCompletionFunc("profile", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpTopLevelResource("profile", toComplete)
	})

	return cmd
}

func (c *cmdInit) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 2)
	if exit {
		return err
	}

	if len(args) == 0 && !c.flagEmpty {
		_ = cmd.Usage()
		return nil
	}

	_, _, err = c.create(c.global.conf, args, false)
	return err
}

func (c *cmdInit) create(conf *config.Config, args []string, launch bool) (lxd.InstanceServer, string, error) {
	var name string
	var image string
	var remote string
	var iremote string
	var err error
	var stdinData api.InstancePut
	var devicesMap map[string]map[string]string
	var configMap map[string]string
	var profiles []string

	// If stdin isn't a terminal, read text from it.
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, "", err
		}

		err = yaml.Unmarshal(contents, &stdinData)
		if err != nil {
			return nil, "", err
		}
	}

	if len(args) > 0 {
		iremote, image = conf.ParseRemoteUnchecked(args[0])

		if len(args) == 1 {
			remote, name, err = conf.ParseRemote("")
			if err != nil {
				return nil, "", err
			}
		} else if len(args) == 2 {
			remote, name, err = conf.ParseRemote(args[1])
			if err != nil {
				return nil, "", err
			}
		}
	}

	if c.flagEmpty {
		if len(args) > 1 {
			return nil, "", errors.New("--empty cannot be combined with an image name")
		}

		if len(args) == 0 {
			remote, name, err = conf.ParseRemote("")
			if err != nil {
				return nil, "", err
			}
		} else if len(args) == 1 {
			remote, name, err = conf.ParseRemote(args[0])
			if err != nil {
				return nil, "", err
			}

			image = ""
			iremote = ""
		}
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return nil, "", err
	}

	// Overwrite profiles.
	if c.flagProfile != nil {
		profiles = c.flagProfile
	} else if c.flagNoProfiles {
		profiles = []string{}
	}

	if !c.global.flagQuiet {
		if d.HasExtension("instance_create_start") && launch {
			if name == "" {
				fmt.Print("Launching the instance\n")
			} else {
				fmt.Printf("Launching %s\n", name)
			}
		} else {
			if name == "" {
				fmt.Print("Creating the instance\n")
			} else {
				fmt.Printf("Creating %s\n", name)
			}
		}
	}

	if len(stdinData.Devices) > 0 {
		devicesMap = stdinData.Devices
	} else {
		devicesMap = map[string]map[string]string{}
	}

	if c.flagNetwork != "" {
		network, _, err := d.GetNetwork(c.flagNetwork)
		if err != nil {
			return nil, "", fmt.Errorf("Failed loading network %q: %w", c.flagNetwork, err)
		}

		// Prepare the instance's NIC device entry.
		var device map[string]string

		if network.Managed && d.HasExtension("instance_nic_network") {
			// If network is managed, use the network property rather than nictype, so that the
			// network's inherited properties are loaded into the NIC when started.
			device = map[string]string{
				"name":    "eth0",
				"type":    "nic",
				"network": network.Name,
			}
		} else {
			// If network is unmanaged default to using a macvlan connected to the specified interface.
			device = map[string]string{
				"name":    "eth0",
				"type":    "nic",
				"nictype": "macvlan",
				"parent":  c.flagNetwork,
			}

			if network.Type == "bridge" {
				// If the network type is an unmanaged bridge, use bridged NIC type.
				device["nictype"] = "bridged"
			}
		}

		devicesMap["eth0"] = device
	}

	if len(stdinData.Config) > 0 {
		configMap = stdinData.Config
	} else {
		configMap = map[string]string{}
	}

	for _, entry := range c.flagConfig {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			return nil, "", fmt.Errorf("Bad key=value pair: %q", entry)
		}

		configMap[key] = value
	}

	// Check if the specified storage pool exists.
	if c.flagStorage != "" {
		_, _, err := d.GetStoragePool(c.flagStorage)
		if err != nil {
			return nil, "", fmt.Errorf("Failed loading storage pool %q: %w", c.flagStorage, err)
		}

		devicesMap["root"] = map[string]string{
			"type": "disk",
			"path": "/",
			"pool": c.flagStorage,
		}
	}

	// Decide whether we are creating a container or a virtual machine.
	var instanceDBType api.InstanceType
	if c.flagVM {
		instanceDBType = api.InstanceTypeVM
	} else if !d.HasExtension("image_registries_operations") {
		// If the server doesn't support the image_registries_operations extension, we must provide a default
		// type as the server won't be able to infer it from the image source itself.
		instanceDBType = api.InstanceTypeContainer
	}

	// Set the target if provided.
	if c.flagTarget != "" {
		d = d.UseTarget(c.flagTarget)
	}

	// Set the target project if provided.
	if c.flagTargetProject != "" {
		d = d.UseProject(c.flagTargetProject)
	}

	// Setup instance creation request.
	req := api.InstancesPost{
		Name:         name,
		InstanceType: c.flagType,
		Type:         instanceDBType,
		Start:        launch,
	}

	req.Config = configMap
	req.Ephemeral = c.flagEphemeral
	req.Description = stdinData.Description

	if !c.flagNoProfiles && len(profiles) == 0 {
		if len(stdinData.Profiles) > 0 {
			req.Profiles = stdinData.Profiles
		} else {
			req.Profiles = nil
		}
	} else {
		req.Profiles = profiles
	}

	// Handle device overrides.
	deviceOverrides, err := parseDeviceOverrides(c.flagDevice)
	if err != nil {
		return nil, "", err
	}

	// Check to see if any of the overridden devices are for devices that are not yet defined in the
	// local devices (and thus maybe expected to be coming from profiles).
	profileDevices := make(map[string]map[string]string)
	needProfileExpansion := false
	for deviceName := range deviceOverrides {
		_, isLocalDevice := devicesMap[deviceName]
		if !isLocalDevice {
			needProfileExpansion = true
			break
		}
	}

	// If there are device overrides that are expected to be applied to profile devices then load the profiles
	// that would be applied server-side.
	if needProfileExpansion {
		// If the list of profiles is empty then LXD would apply the default profile on the server side.
		profileDevices, err = getProfileDevices(d, req.Profiles)
		if err != nil {
			return nil, "", err
		}
	}

	// Apply device overrides.
	devicesMap, err = shared.ApplyDeviceOverrides(devicesMap, profileDevices, deviceOverrides)
	if err != nil {
		return nil, "", err
	}

	req.Devices = devicesMap

	var opInfo api.Operation
	if !c.flagEmpty {
		// Get the image server and image info.
		iremote, image = guessImage(conf, d, remote, iremote, image)

		// Deal with the default image.
		if image == "" {
			image = "default"
		}

		var imgRemoteServer lxd.ImageServer
		var imgInfo *api.Image
		var legacyRemote string
		var err error

		// If the server supports image registries, we can use server-side image resolution and download.
		// This avoids resolving the image on the client side, and passes the registry name or source project
		// to the server so it can handle the resolution directly (which works for both public and private images,
		// and supports features like automatic local caching and alias resolution).
		if d.HasExtension("image_registries_operations") {
			var registryName string
			imgInfo, registryName = resolveRegistryImageSource(conf, iremote, image, remote, c.global.flagProject)

			if registryName != "" {
				// Remote image registry.
				// Check if the server has an image registry with this name.
				_, _, err := d.GetImageRegistry(registryName)
				if err != nil {
					// Only fall back for 404 (registry not found).
					if !api.StatusErrorCheck(err, http.StatusNotFound) {
						return nil, "", fmt.Errorf("Failed checking image registry %q: %w", registryName, err)
					}

					// Registry not found. If the local remote is a SimpleStreams remote with a
					// transitional URL, fall back to sending the deprecated Server and Protocol
					// fields so the server can auto-create the registry.
					remoteConfig := conf.Remotes[iremote]
					if remoteConfig.Protocol != api.ImageRegistryProtocolSimpleStreams || !api.IsTransitionalSimpleStreamsURL(remoteConfig.Addr) {
						return nil, "", fmt.Errorf("Image registry %q not found", registryName)
					}

					req.Source.Server = remoteConfig.Addr                        //nolint:staticcheck
					req.Source.Protocol = api.ImageRegistryProtocolSimpleStreams //nolint:staticcheck
				} else {
					// Registry exists on the server — use it directly.
					req.Source.ImageRegistry = registryName
				}
			}
		} else {
			// Fetch image info from the given remote (legacy client-side resolution path).
			// Normalize empty remote to the default remote, since ParseRemoteUnchecked
			// does not fill in the default.
			legacyRemote = iremote
			if legacyRemote == "" {
				legacyRemote = conf.DefaultRemote
			}

			imgRemoteServer, imgInfo, err = getImgInfo(conf, legacyRemote, image, c.global.flagProject, &req.Source)
			if err != nil {
				return nil, "", err
			}
		}

		// Update the source project if it was determined by getImgInfo.
		if imgRemoteServer == nil && imgInfo.Project != "" {
			req.Source.Project = imgInfo.Project
		}

		// Only perform legacy type and protocol checks if we are NOT using an image registry.
		if imgRemoteServer != nil && conf.Remotes[legacyRemote].Protocol != "simplestreams" {
			if imgInfo.Type != "virtual-machine" && c.flagVM {
				return nil, "", errors.New("Asked for a VM but image is of type container")
			}

			req.Type = api.InstanceType(imgInfo.Type)
		}

		// Create the instance.
		op, err := d.CreateInstanceFromImage(imgRemoteServer, *imgInfo, req)
		if err != nil {
			return nil, "", err
		}

		// Watch the background operation.
		progress := cli.ProgressRenderer{
			Format: "Retrieving image: %s",
			Quiet:  c.global.flagQuiet,
		}

		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return nil, "", err
		}

		err = cli.CancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return nil, "", err
		}

		progress.Done("")

		// Extract the instance name.
		info, err := op.GetTarget()
		if err != nil {
			return nil, "", err
		}

		opInfo = *info
	} else {
		req.Source.Type = api.SourceTypeNone

		op, err := d.CreateInstance(req)
		if err != nil {
			return nil, "", err
		}

		err = op.Wait()
		if err != nil {
			return nil, "", err
		}

		opInfo = op.Get()
	}

	if name == "" {
		if d.HasExtension("operation_metadata_entity_url") {
			name, _, err = getEntityFromOperationMetadata(opInfo.Metadata)
		} else {
			// Use "instances"/"containers" here and not "entity.TypeInstance"/"entity.TypeContainer" because the change
			// to use entity type names happened after the operation_metadata_entity_url extension.
			name, _, err = getEntityFromOperationResources(opInfo.Resources, "instances", "containers")
		}

		if err != nil {
			return nil, "", fmt.Errorf("Failed getting instance name from operation: %w", err)
		}

		fmt.Printf("Instance name is: %s\n", name)
	}

	// Validate the network setup.
	c.checkNetwork(d, name)

	return d, name, nil
}

func (c *cmdInit) checkNetwork(d lxd.InstanceServer, name string) {
	ct, _, err := d.GetInstance(name)
	if err != nil {
		return
	}

	for _, d := range ct.ExpandedDevices {
		if d["type"] == "nic" {
			return
		}
	}

	fmt.Fprint(os.Stderr, "\n"+"The instance you are starting does not have any network attached to it.\n")
	fmt.Fprint(os.Stderr, "  "+"To create a new network, use: lxc network create\n")
	fmt.Fprint(os.Stderr, "  "+"To attach a network to an instance, use: lxc network attach\n\n")
}
