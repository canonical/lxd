package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/termios"
	"github.com/canonical/lxd/shared/version"
)

type cmdClusterLink struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterLink) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("link")
	cmd.Short = "Manage cluster links"
	cmd.Long = cli.FormatSection("Description", `Manage cluster links`)

	// Create
	clusterLinkCreateCmd := cmdClusterLinkCreate{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkCreateCmd.command())

	// List
	clusterLinkListCmd := cmdClusterLinkList{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkListCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdClusterLinkCreate struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagToken       string
	flagAuthGroups  []string
	flagDescription string
}

func (c *cmdClusterLinkCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", "[<remote>:]<link> [key=value...]")
	cmd.Short = "Create cluster links"
	cmd.Long = cli.FormatSection("Description", `Create cluster links

When run with the --token flag, creates an active cluster link.
When run without a token, creates a pending cluster link that must be activated by creating a cluster link on the remote cluster.`)
	cmd.Example = cli.FormatSection("", `lxc cluster link create backup-cluster --auth-group backups
	Create a pending cluster link reachable at "10.0.0.0:8443" and "10.0.0.1:8443" called "backup-cluster", belonging to the authentication group "backups".

	lxc cluster link create main-cluster --token <token from backup-cluster> --auth-group backups
	Create a cluster link with "backup-cluster" called "main-cluster", belonging to the auth group "backups".

	lxc cluster link create recovery-cluster < config.yaml
	Create a pending cluster link with the configuration from "config.yaml" called "recovery-cluster".`)
	cmd.Flags().StringVarP(&c.flagToken, "token", "t", "", cli.FormatStringFlagLabel("Trust token to use when creating cluster link"))
	cmd.Flags().StringSliceVarP(&c.flagAuthGroups, "auth-group", "g", []string{}, cli.FormatStringFlagLabel("Authentication groups to add the newly created cluster link identity to"))
	cmd.Flags().StringVarP(&c.flagDescription, "description", "d", "", cli.FormatStringFlagLabel("Cluster link description"))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdClusterLinkCreate) run(cmd *cobra.Command, args []string) error {
	var stdinData api.ClusterLinkPut

	// Quick checks
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(contents, &stdinData)
		if err != nil {
			return err
		}
	}

	// Parse remote
	remoteName, clusterLinkName, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	transporter, wrapper := newLocationHeaderTransportWrapper()
	client, err := c.global.conf.GetInstanceServerWithConnectionArgs(remoteName, &lxd.ConnectionArgs{TransportWrapper: wrapper})
	if err != nil {
		return err
	}

	clusterLink := api.ClusterLinkPost{
		Name:           clusterLinkName,
		ClusterLinkPut: stdinData,
	}

	if c.flagDescription != "" {
		clusterLink.Description = c.flagDescription
	}

	if stdinData.Config == nil {
		clusterLink.Config = map[string]string{}
	}

	for i := 1; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf("Bad key=value pair: %s", args[i])
		}

		clusterLink.Config[entry[0]] = entry[1]
	}

	// Set auth groups if provided
	if len(c.flagAuthGroups) > 0 {
		clusterLink.AuthGroups = c.flagAuthGroups
	}

	if c.flagToken == "" {
		token, err := client.CreateIdentityClusterLinkToken(clusterLink)
		if err != nil {
			return err
		}

		// Encode certificate add token to JSON.
		tokenJSON, err := json.Marshal(token)
		if err != nil {
			return fmt.Errorf("Failed encoding identity token: %w", err)
		}

		if !c.global.flagQuiet {
			fmt.Printf("Cluster link %s created (pending)"+"\n", clusterLinkName)

			pendingIdentityURL, err := url.Parse(transporter.location)
			if err != nil {
				return fmt.Errorf("Received invalid location header %q: %w", transporter.location, err)
			}

			var pendingIdentityUUIDStr string
			identityURLPrefix := api.NewURL().Path(version.APIVersion, "auth", "identities", api.AuthenticationMethodTLS).String()
			_, err = fmt.Sscanf(pendingIdentityURL.Path, identityURLPrefix+"/%s", &pendingIdentityUUIDStr)
			if err != nil {
				return fmt.Errorf("Received unexpected location header %q: %w", transporter.location, err)
			}

			pendingIdentityUUID, err := uuid.Parse(pendingIdentityUUIDStr)
			if err != nil {
				return fmt.Errorf("Received invalid pending identity UUID %q: %w", pendingIdentityUUIDStr, err)
			}

			fmt.Printf("Cluster link %q (%s) pending identity token:"+"\n", clusterLinkName, pendingIdentityUUID.String())
		}

		// Print the base64 encoded token.
		fmt.Println(base64.StdEncoding.EncodeToString(tokenJSON))
	} else {
		clusterLink.TrustToken = c.flagToken
		err = client.CreateClusterLink(clusterLink)
		if err != nil {
			return err
		}

		if !c.global.flagQuiet {
			fmt.Printf("Cluster link %s created"+"\n", clusterLinkName)
		}
	}

	return nil
}

// List.
type cmdClusterLinkList struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagFormat string
}

func (c *cmdClusterLinkList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List cluster links"
	cmd.Long = cli.FormatSection("Description", `List cluster links`)

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 1 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterLinkList) run(cmd *cobra.Command, args []string) error {
	// Quick checks
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	client := resource.server

	clusterLinks, err := client.GetClusterLinks()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, clusterLink := range clusterLinks {
		clusterLinkIdentity, _, err := client.GetIdentity(api.AuthenticationMethodTLS, clusterLink.Name)
		if err != nil {
			return err
		}

		identityStatus := "ACTIVE"
		if clusterLinkIdentity.Type == api.IdentityTypeCertificateClusterLinkPending {
			identityStatus = "PENDING"
		}

		details := []string{
			clusterLink.Name,
			clusterLink.Config["volatile.addresses"],
			clusterLink.Description,
			identityStatus,
			strings.ToUpper(clusterLink.Type),
		}

		data = append(data, details)
	}
	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		"NAME",
		"ADDRESSES",
		"DESCRIPTION",
		"IDENTITY STATUS",
		"TYPE",
	}

	return cli.RenderTable(c.flagFormat, header, data, clusterLinks)
}
