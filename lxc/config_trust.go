package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
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

	// Remove
	configTrustRemoveCmd := cmdConfigTrustRemove{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustRemoveCmd.Command())

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
	cmd.Use = usage("add", i18n.G("[<remote>:] <cert>"))
	cmd.Short = i18n.G("Add new trusted clients")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add new trusted clients`))

	cmd.Flags().StringVar(&c.flagName, "name", "", i18n.G("Alternative certificate name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustAdd) Run(cmd *cobra.Command, args []string) error {
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
		name, _ = shared.SplitExt(fname)
	}

	// Add trust relationship.
	x509Cert, err := shared.ReadCert(fname)
	if err != nil {
		return err
	}

	cert := api.CertificatesPost{}
	cert.Certificate = base64.StdEncoding.EncodeToString(x509Cert.Raw)
	cert.Name = name
	cert.Type = api.CertificateTypeClient

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

// Remove
type cmdConfigTrustRemove struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

func (c *cmdConfigTrustRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:] <hostname|fingerprint>"))
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
