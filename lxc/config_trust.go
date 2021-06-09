package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
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

	// Edit
	configTrustEditCmd := cmdConfigTrustEdit{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustEditCmd.Command())

	// List
	configTrustListCmd := cmdConfigTrustList{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustListCmd.Command())

	// Remove
	configTrustRemoveCmd := cmdConfigTrustRemove{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustRemoveCmd.Command())

	// Show
	configTrustShowCmd := cmdConfigTrustShow{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustShowCmd.Command())

	return cmd
}

// Add
type cmdConfigTrustAdd struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust

	flagName       string
	flagProjects   string
	flagRestricted bool
}

func (c *cmdConfigTrustAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:] <cert>"))
	cmd.Short = i18n.G("Add new trusted clients")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add new trusted clients`))

	cmd.Flags().BoolVar(&c.flagRestricted, "restricted", false, i18n.G("Restrict the certificate to one or more projects"))
	cmd.Flags().StringVar(&c.flagProjects, "projects", "", i18n.G("List of projects to restrict the certificate to")+"``")
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
	cert.Restricted = c.flagRestricted
	if c.flagProjects != "" {
		cert.Projects = strings.Split(c.flagProjects, ",")
	}

	return resource.server.CreateCertificate(cert)
}

// Edit
type cmdConfigTrustEdit struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

func (c *cmdConfigTrustEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<fingerprint>"))
	cmd.Short = i18n.G("Edit trust configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit trust configurations as YAML`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the certificate.
### Any line starting with a '# will be ignored.
###
### Note that the fingerprint is shown but cannot be changed`)
}

func (c *cmdConfigTrustEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing certificate fingerprint"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.CertificatePut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateCertificate(resource.name, newdata, "")
	}

	// Extract the current value
	cert, etag, err := resource.server.GetCertificate(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&cert)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.CertificatePut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateCertificate(resource.name, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = shared.TextEditor("", content)
			if err != nil {
				return err
			}
			continue
		}
		break
	}

	return nil
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

		tlsCert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return err
		}

		const layout = "Jan 2, 2006 at 3:04pm (MST)"
		issue := tlsCert.NotBefore.Format(layout)
		expiry := tlsCert.NotAfter.Format(layout)
		data = append(data, []string{cert.Type, cert.Name, tlsCert.Subject.CommonName, fp, issue, expiry})
	}
	sort.Sort(stringList(data))

	header := []string{
		i18n.G("TYPE"),
		i18n.G("NAME"),
		i18n.G("COMMON NAME"),
		i18n.G("FINGERPRINT"),
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

// Show
type cmdConfigTrustShow struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

func (c *cmdConfigTrustShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<fingerprint>"))
	cmd.Short = i18n.G("Show trust configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show trust configurations`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
	client := resource.server

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing certificate fingerprint"))
	}

	// Show the certificate configuration
	cert, _, err := client.GetCertificate(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&cert)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
