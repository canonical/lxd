package main

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/version"
)

type cmdInit struct {
	global *cmdGlobal

	flagAuto    bool
	flagMinimal bool
	flagPreseed bool
	flagDump    bool

	flagNetworkAddress        string
	flagNetworkPort           int64
	flagStorageBackend        string
	flagStorageDevice         string
	flagStorageLoopSize       int
	flagStoragePool           string
	flagUITemporaryAccessLink bool

	hostname string
}

// Command returns a cobra command to configure the LXD daemon.
func (c *cmdInit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "init"
	cmd.Short = "Configure the LXD daemon"
	cmd.Long = `Description:
  Configure the LXD daemon
`
	cmd.Example = `  init --minimal
  init --auto [--network-address=IP]
              [--network-port=8443]
              [--storage-backend=dir]
              [--storage-create-device=DEVICE]
              [--storage-create-loop=SIZE]
              [--storage-pool=POOL]
              [--ui-temporary-access-link]
  init --preseed
  init --dump
  init --ui-temporary-access-link
`
	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagAuto, "auto", false, "Automatic (non-interactive) mode")
	cmd.Flags().BoolVar(&c.flagMinimal, "minimal", false, "Minimal configuration (non-interactive)")
	cmd.Flags().BoolVar(&c.flagPreseed, "preseed", false, "Pre-seed mode, expects YAML config from stdin")
	cmd.Flags().BoolVar(&c.flagDump, "dump", false, "Dump YAML config to stdout")

	cmd.Flags().StringVar(&c.flagNetworkAddress, "network-address", "", cli.FormatStringFlagLabel("Address to bind LXD to (default: none)"))
	cmd.Flags().Int64Var(&c.flagNetworkPort, "network-port", -1, fmt.Sprintf("Port to bind LXD to (default: %d)", shared.HTTPSDefaultPort))
	cmd.Flags().StringVar(&c.flagStorageBackend, "storage-backend", "", cli.FormatStringFlagLabel("Storage backend to use (btrfs, dir, lvm or zfs, default: dir)"))
	cmd.Flags().StringVar(&c.flagStorageDevice, "storage-create-device", "", cli.FormatStringFlagLabel("Setup device based storage using DEVICE"))
	cmd.Flags().IntVar(&c.flagStorageLoopSize, "storage-create-loop", -1, "Setup loop based storage with SIZE in GiB")
	cmd.Flags().StringVar(&c.flagStoragePool, "storage-pool", "", cli.FormatStringFlagLabel("Storage pool to use or create"))
	cmd.Flags().BoolVar(&c.flagUITemporaryAccessLink, "ui-temporary-access-link", false, "Generate the URL for accessing LXD UI temporarily")

	return cmd
}

// Run executes the command to configure the LXD daemon.
func (c *cmdInit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if c.flagAuto && c.flagPreseed {
		return errors.New("Can't use --auto and --preseed together")
	}

	if c.flagMinimal && c.flagPreseed {
		return errors.New("Can't use --minimal and --preseed together")
	}

	if c.flagMinimal && c.flagAuto {
		return errors.New("Can't use --minimal and --auto together")
	}

	if c.flagUITemporaryAccessLink && (c.flagPreseed || c.flagDump || c.flagMinimal) {
		return errors.New("Can't use --ui-temporary-access-link with --preseed, --dump, or --minimal")
	}

	if !c.flagAuto && (c.flagNetworkAddress != "" || c.flagNetworkPort != -1 ||
		c.flagStorageBackend != "" || c.flagStorageDevice != "" ||
		c.flagStorageLoopSize != -1 || c.flagStoragePool != "") {
		return errors.New("Configuration flags require --auto")
	}

	if c.flagDump && (c.flagAuto || c.flagMinimal ||
		c.flagPreseed || c.flagNetworkAddress != "" ||
		c.flagNetworkPort != -1 || c.flagStorageBackend != "" ||
		c.flagStorageDevice != "" || c.flagStorageLoopSize != -1 ||
		c.flagStoragePool != "") {
		return errors.New("Can't use --dump with other flags")
	}

	// Connect to LXD
	d, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return fmt.Errorf("Failed to connect to local LXD: %w", err)
	}

	server, _, err := d.GetServer()
	if err != nil {
		return fmt.Errorf("Failed to connect to get LXD server info: %w", err)
	}

	// If UI temporary access link flag is set, but auto mode is not enabled,
	// generate the link and exit.
	if c.flagUITemporaryAccessLink && !c.flagAuto {
		return c.createUITemporaryAccessLink(d)
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
	var config *api.InitPreseed

	// Preseed mode
	if c.flagPreseed {
		config, err = c.runPreseed()
		if err != nil {
			return err
		}
	}

	// Auto mode
	if c.flagAuto || c.flagMinimal {
		config, err = c.RunAuto(cmd, args, d, server)
		if err != nil {
			return err
		}
	}

	// Interactive mode
	if !c.flagAuto && !c.flagMinimal && !c.flagPreseed {
		config, err = c.RunInteractive(cmd, args, d, server)
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

		content, err := os.ReadFile(config.Cluster.ClusterCertificatePath)
		if err != nil {
			return err
		}

		config.Cluster.ClusterCertificate = string(content)
	}

	// Check if we got a cluster join token, if so, fill in the config with it.
	if config.Cluster != nil && config.Cluster.ClusterToken != "" {
		joinToken, err := shared.JoinTokenDecode(config.Cluster.ClusterToken)
		if err != nil {
			return fmt.Errorf("Invalid cluster join token: %w", err)
		}

		// Set server name from join token
		config.Cluster.ServerName = joinToken.ServerName

		// Attempt to find a working cluster member to use for joining by retrieving the
		// cluster certificate from each address in the join token until we succeed.
		for _, clusterAddress := range joinToken.Addresses {
			// Cluster URL
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
		// Ensure the server and cluster addresses are in canonical form.
		config.Cluster.ServerAddress = util.CanonicalNetworkAddress(config.Cluster.ServerAddress, shared.HTTPSDefaultPort)
		config.Cluster.ClusterAddress = util.CanonicalNetworkAddress(config.Cluster.ClusterAddress, shared.HTTPSDefaultPort)

		op, err := d.UpdateCluster(config.Cluster.ClusterPut, "")
		if err != nil {
			return fmt.Errorf("Failed to join cluster: %w", err)
		}

		err = op.Wait()
		if err != nil {
			return fmt.Errorf("Failed to join cluster: %w", err)
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

	if c.flagUITemporaryAccessLink {
		err = c.createUITemporaryAccessLink(d)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

func (c *cmdInit) defaultHostname() string {
	if c.hostname != "" {
		return c.hostname
	}

	// Cluster server name
	hostName, err := os.Hostname()
	if err != nil {
		hostName = "lxd"
	}

	c.hostname = hostName
	return hostName
}

func (c *cmdInit) createUITemporaryAccessLink(d lxd.InstanceServer) error {
	// Refresh server info.
	server, _, err := d.GetServer()
	if err != nil {
		return fmt.Errorf("Failed to refresh LXD server info: %w", err)
	}

	var serverAddress string
	if len(server.Environment.Addresses) > 0 {
		serverAddress = server.Environment.Addresses[0]
	}

	if serverAddress == "" {
		return errors.New("LXD server address is not set, can't create UI temporary access link")
	}

	uiAdminIdentityName := "ui-admin-temporary"
	uiAdminIdentityGroup := "admins"
	uiAdminIdentityGroupDesc := "Server administrators"
	uiAdminIdentityGroupPerm := api.Permission{
		Entitlement:     string(auth.EntitlementAdmin),
		EntityType:      entity.TypeServer.String(),
		EntityReference: "/" + version.APIVersion,
	}

	// Ensure admins group exists and has the expected permissions.
	adminsGroup, etag, err := d.GetAuthGroup(uiAdminIdentityGroup)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("Failed to check for existing temporary UI identity: %w", err)
	}

	if adminsGroup == nil {
		// Create group if it doesn't exist.
		adminsGroupReq := api.AuthGroupsPost{
			AuthGroupPost: api.AuthGroupPost{
				Name: uiAdminIdentityGroup,
			},
			AuthGroupPut: api.AuthGroupPut{
				Description: uiAdminIdentityGroupDesc,
				Permissions: []api.Permission{uiAdminIdentityGroupPerm},
			},
		}

		err := d.CreateAuthGroup(adminsGroupReq)
		if err != nil {
			return fmt.Errorf("Failed to create admin auth group: %w", err)
		}
	} else if len(adminsGroup.Permissions) != 1 ||
		adminsGroup.Permissions[0].Entitlement != uiAdminIdentityGroupPerm.Entitlement ||
		adminsGroup.Permissions[0].EntityType != uiAdminIdentityGroupPerm.EntityType ||
		adminsGroup.Permissions[0].EntityReference != uiAdminIdentityGroupPerm.EntityReference {
		// Ensure admins group has the correct permissions.
		adminsGroupReq := api.AuthGroupPut{
			Description: uiAdminIdentityGroupDesc,
			Permissions: []api.Permission{uiAdminIdentityGroupPerm},
		}

		err := d.UpdateAuthGroup(uiAdminIdentityGroup, adminsGroupReq, etag)
		if err != nil {
			return fmt.Errorf("Failed to update admin auth group: %w", err)
		}
	}

	// Check if identity already exists.
	uiAdminIdentity, etag, err := d.GetIdentity(api.AuthenticationMethodBearer, uiAdminIdentityName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("Failed to check for existing temporary UI identity: %w", err)
	}

	if uiAdminIdentity == nil {
		// Create identity if it doesn't exist.
		uiAdminIdentityReq := api.IdentitiesBearerPost{
			Name:   uiAdminIdentityName,
			Type:   api.IdentityTypeBearerTokenClient,
			Groups: []string{uiAdminIdentityGroup},
		}

		err := d.CreateIdentityBearer(uiAdminIdentityReq)
		if err != nil {
			return fmt.Errorf("Failed to create temporary UI identity: %w", err)
		}
	} else if len(uiAdminIdentity.Groups) != 1 || uiAdminIdentity.Groups[0] != uiAdminIdentityGroup {
		// Ensure identity is part of group admins.
		uiAdminIdentityReq := api.IdentityPut{
			Groups: []string{uiAdminIdentityGroup},
		}

		err := d.UpdateIdentity(api.AuthenticationMethodBearer, uiAdminIdentityName, uiAdminIdentityReq, etag)
		if err != nil {
			return fmt.Errorf("Failed to update temporary UI identity: %w", err)
		}
	}

	// Issue bearer token for the identity (validity 1 day).
	tokenRequest := api.IdentityBearerTokenPost{
		Expiry: "1d",
	}

	token, err := d.IssueBearerIdentityToken(uiAdminIdentityName, tokenRequest)
	if err != nil {
		return fmt.Errorf("Failed to issue bearer token for temporary UI access link: %w", err)
	}

	tokenExpiry := time.Now().Add(24 * time.Hour).Format("2006-01-02 15:04")
	uiAccessLink := api.NewURL().Scheme("https").Host(serverAddress).WithQuery("token", token.Token)
	fmt.Println("UI temporary identity (type: Client token bearer): " + uiAdminIdentityName)
	fmt.Println("UI temporary access link (expires: " + tokenExpiry + "): " + uiAccessLink.String())
	return nil
}
