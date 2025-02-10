package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
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
	cmd.Short = i18n.G("Manage cluster links")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage cluster links`))

	// Create
	clusterLinkCreateCmd := cmdClusterLinkCreate{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkCreateCmd.command())

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
	cmd.Use = usage("create", i18n.G("[<remote>:]<link> [key=value...]"))
	cmd.Short = i18n.G("Create cluster links")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create cluster links

When run with a token, creates an active cluster link.
When run without a token, creates a pending cluster link that must be activated by creating a cluster link on the remote cluster.`))
	cmd.Example = cli.FormatSection("", i18n.G(`lxc cluster link create backup-cluster --auth-group backups
	Create a pending cluster link reachable at "10.0.0.0:8443" and "10.0.0.1:8443" called "backup-cluster", belonging to the authentication group "backups".

	lxc cluster link create main-cluster <token from backup-bluster> --auth-group backups
	Create a cluster link with "backup-cluster" called "main-cluster", belonging to the auth group "backups".

	lxc cluster link create recovery-cluster < config.yaml
	Create a pending cluster link with the configuration from "config.yaml" called "recovery-cluster".`))
	cmd.Flags().StringVarP(&c.flagToken, "token", "t", "", "Trust token to use when creating cluster link")
	cmd.Flags().StringSliceVarP(&c.flagAuthGroups, "auth-group", "g", []string{}, "Authentication groups to add the newly created cluster link identity to")
	cmd.Flags().StringVarP(&c.flagDescription, "description", "d", "", "Cluster link description")

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
		for i := 2; i < len(args); i++ {
			entry := strings.SplitN(args[i], "=", 2)
			if len(entry) < 2 {
				return fmt.Errorf(i18n.G("Bad key=value pair: %s"), entry)
			}

			clusterLink.Config[entry[0]] = entry[1]
		}
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
			return fmt.Errorf("Failed to encode identity token: %w", err)
		}

		if !c.global.flagQuiet {
			fmt.Printf(i18n.G("Cluster link %s created (pending)")+"\n", clusterLinkName)

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

			fmt.Printf(i18n.G("Cluster link %q (%s) pending identity token:")+"\n", clusterLinkName, pendingIdentityUUID.String())
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
			fmt.Printf(i18n.G("Cluster link %s created")+"\n", clusterLinkName)
		}
	}

	return nil
}
