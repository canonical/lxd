package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdConfigTrust struct {
	global *cmdGlobal
	config *cmdConfig
}

func (c *cmdConfigTrust) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("trust")
	cmd.Short = i18n.G("Manage trusted clients")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage trusted clients`))

	// Add
	configTrustAddCmd := cmdConfigTrustAdd{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustAddCmd.Command())

	// List
	configTrustListCmd := cmdConfigTrustList{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustListCmd.Command())

	// List tokens
	configTrustListTokensCmd := cmdConfigTrustListTokens{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustListTokensCmd.Command())

	// Remove
	configTrustRemoveCmd := cmdConfigTrustRemove{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustRemoveCmd.Command())

	// Revoke token
	configTrustRevokeTokenCmd := cmdConfigTrustRevokeToken{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustRevokeTokenCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { cmd.Usage() }
	return cmd
}

// Add
type cmdConfigTrustAdd struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust

	flagName string
}

func (c *cmdConfigTrustAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:] [<cert>]"))
	cmd.Short = i18n.G("Add new trusted clients")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add new trusted clients`))

	cmd.Flags().StringVar(&c.flagName, "name", "", i18n.G("Alternative certificate name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustAdd) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 2)
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

	cert := api.CertificatesPost{}
	cert.Type = api.CertificateTypeClient

	if len(args) == 0 {
		// Use token
		cert.Token = true

		cert.Name, err = cli.AskString(i18n.G("Please provide client name: "), "", nil)
		if err != nil {
			return err
		}
	} else {
		// Load the certificate.
		fname := args[len(args)-1]
		if fname == "-" {
			fname = "/dev/stdin"
		} else {
			fname = shared.HostPathFollow(fname)
		}

		var name string
		if c.flagName != "" {
			name = c.flagName
		} else {
			name = filepath.Base(fname)
		}

		// Add trust relationship.
		x509Cert, err := shared.ReadCert(fname)
		if err != nil {
			return err
		}

		cert.Certificate = base64.StdEncoding.EncodeToString(x509Cert.Raw)
		cert.Name = name
	}

	if cert.Token {
		op, err := resource.server.CreateCertificateToken(cert)
		if err != nil {
			return err
		}

		if !c.global.flagQuiet {
			opAPI := op.Get()
			certificateToken, err := opAPI.ToCertificateAddToken()
			if err != nil {
				return fmt.Errorf(i18n.G("Failed converting token operation to certificate add token: %w"), err)
			}

			fmt.Printf(i18n.G("Client %s certificate add token: %s")+"\n", cert.Name, certificateToken.String())
		}

		return nil
	}

	return resource.server.CreateCertificate(cert)
}

// List
type cmdConfigTrustList struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust

	flagFormat string
}

func (c *cmdConfigTrustList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List trusted clients")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List trusted clients`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml)")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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

	// List trust relationships
	trust, err := resource.server.GetCertificates()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, cert := range trust {
		fp := cert.Fingerprint[0:12]

		certBlock, _ := pem.Decode([]byte(cert.Certificate))
		if certBlock == nil {
			return fmt.Errorf(i18n.G("Invalid certificate"))
		}

		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return err
		}

		const layout = "Jan 2, 2006 at 3:04pm (MST)"
		issue := cert.NotBefore.Format(layout)
		expiry := cert.NotAfter.Format(layout)
		data = append(data, []string{fp, cert.Subject.CommonName, issue, expiry})
	}
	sort.Sort(stringList(data))

	header := []string{
		i18n.G("FINGERPRINT"),
		i18n.G("COMMON NAME"),
		i18n.G("ISSUE DATE"),
		i18n.G("EXPIRY DATE"),
	}

	return utils.RenderTable(c.flagFormat, header, data, trust)
}

// List tokens
type cmdConfigTrustListTokens struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust

	flagFormat string
}

func (c *cmdConfigTrustListTokens) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list-tokens", i18n.G("[<remote>:]"))
	cmd.Short = i18n.G("List all active certificate add tokens")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List all active certificate add tokens`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml)")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustListTokens) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote.
	remote := ""
	if len(args) == 1 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Get the certificate add tokens. Use default project as join tokens are created in default project.
	ops, err := resource.server.UseProject("default").GetOperations()
	if err != nil {
		return err
	}

	// Convert the join token operation into encoded form for display.
	type displayToken struct {
		ClientName string
		Token      string
	}

	displayTokens := make([]displayToken, 0)

	for _, op := range ops {
		if op.Class != api.OperationClassToken {
			continue
		}

		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		joinToken, err := op.ToCertificateAddToken()
		if err != nil {
			continue // Operation is not a valid certificate add token operation.
		}

		displayTokens = append(displayTokens, displayToken{
			ClientName: joinToken.ClientName,
			Token:      joinToken.String(),
		})
	}

	// Render the table.
	data := [][]string{}
	for _, token := range displayTokens {
		line := []string{token.ClientName, token.Token}
		data = append(data, line)
	}
	sort.Sort(byName(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("TOKEN"),
	}

	return utils.RenderTable(c.flagFormat, header, data, displayTokens)
}

// Remove
type cmdConfigTrustRemove struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

func (c *cmdConfigTrustRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:] <fingerprint>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove trusted clients")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove trusted clients`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustRemove) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
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

	// Remove trust relationship
	return resource.server.DeleteCertificate(args[len(args)-1])
}

// List tokens
type cmdConfigTrustRevokeToken struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

func (c *cmdConfigTrustRevokeToken) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("revoke-token", i18n.G("[<remote>:] <token>"))
	cmd.Short = i18n.G("Revoke certificate add token")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Revoke certificate add token`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustRevokeToken) Run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Get the certificate add tokens. Use default project as certificate add tokens are created in default project.
	ops, err := resource.server.UseProject("default").GetOperations()
	if err != nil {
		return err
	}

	for _, op := range ops {
		if op.Class != api.OperationClassToken {
			continue
		}

		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		joinToken, err := op.ToCertificateAddToken()
		if err != nil {
			continue // Operation is not a valid certificate add token operation.
		}

		if joinToken.ClientName == resource.name {
			// Delete the operation
			err = resource.server.DeleteOperation(op.ID)
			if err != nil {
				return err
			}

			if !c.global.flagQuiet {
				fmt.Printf(i18n.G("Certificate add token for %s deleted")+"\n", resource.name)
			}

			return nil
		}
	}

	return fmt.Errorf(i18n.G("No certificate add token for member %s on remote: %s"), resource.name, resource.remote)
}
