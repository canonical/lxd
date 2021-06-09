package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh/terminal"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdRemote struct {
	global *cmdGlobal
}

func (c *cmdRemote) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remote")
	cmd.Short = i18n.G("Manage the list of remote servers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage the list of remote servers`))

	// Add
	remoteAddCmd := cmdRemoteAdd{global: c.global, remote: c}
	cmd.AddCommand(remoteAddCmd.Command())

	// Get default
	remoteGetDefaultCmd := cmdRemoteGetDefault{global: c.global, remote: c}
	cmd.AddCommand(remoteGetDefaultCmd.Command())

	// List
	remoteListCmd := cmdRemoteList{global: c.global, remote: c}
	cmd.AddCommand(remoteListCmd.Command())

	// Rename
	remoteRenameCmd := cmdRemoteRename{global: c.global, remote: c}
	cmd.AddCommand(remoteRenameCmd.Command())

	// Remove
	remoteRemoveCmd := cmdRemoteRemove{global: c.global, remote: c}
	cmd.AddCommand(remoteRemoveCmd.Command())

	// Set default
	remoteSwitchCmd := cmdRemoteSwitch{global: c.global, remote: c}
	cmd.AddCommand(remoteSwitchCmd.Command())

	// Set URL
	remoteSetURLCmd := cmdRemoteSetURL{global: c.global, remote: c}
	cmd.AddCommand(remoteSetURLCmd.Command())

	return cmd
}

// Add
type cmdRemoteAdd struct {
	global *cmdGlobal
	remote *cmdRemote

	flagAcceptCert bool
	flagPassword   string
	flagPublic     bool
	flagProtocol   string
	flagAuthType   string
	flagDomain     string
	flagProject    string
}

func (c *cmdRemoteAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>] <IP|FQDN|URL>"))
	cmd.Short = i18n.G("Add new remote servers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add new remote servers

URL for remote resources must be HTTPS (https://).

Basic authentication can be used when combined with the "simplestreams" protocol:
  lxc remote add some-name https://LOGIN:PASSWORD@example.com/some/path --protocol=simplestreams
`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagAcceptCert, "accept-certificate", false, i18n.G("Accept certificate"))
	cmd.Flags().StringVar(&c.flagPassword, "password", "", i18n.G("Remote admin password")+"``")
	cmd.Flags().StringVar(&c.flagProtocol, "protocol", "", i18n.G("Server protocol (lxd or simplestreams)")+"``")
	cmd.Flags().StringVar(&c.flagAuthType, "auth-type", "", i18n.G("Server authentication type (tls or candid)")+"``")
	cmd.Flags().BoolVar(&c.flagPublic, "public", false, i18n.G("Public image server"))
	cmd.Flags().StringVar(&c.flagDomain, "domain", "", i18n.G("Candid domain to use")+"``")
	cmd.Flags().StringVar(&c.flagProject, "project", "", i18n.G("Project to use for the remote")+"``")

	return cmd
}

func (c *cmdRemoteAdd) findProject(d lxd.InstanceServer, project string) (string, error) {
	if project == "" {
		// Check if we can pull a list of projects.
		if d.HasExtension("projects") {
			// Retrieve the allowed projects.
			names, err := d.GetProjectNames()
			if err != nil {
				return "", err
			}

			if len(names) == 0 {
				// If no allowed projects, just keep it to the default.
				return "", nil
			} else if len(names) == 1 {
				// If only a single project, use that.
				return names[0], nil
			}

			// Deal with multiple projects.
			if shared.StringInSlice("default", names) {
				// If we have access to the default project, use it.
				return "", nil
			}

			// Let's ask the user.
			fmt.Println(i18n.G("Available projects:"))
			for _, name := range names {
				fmt.Println(" - " + name)
			}
			return cli.AskChoice(i18n.G("Name of the project to use for this remote:")+" ", names, ""), nil
		}

		return "", nil
	}

	_, _, err := d.GetProject(project)
	if err != nil {
		return "", err
	}

	return project, nil
}

func (c *cmdRemoteAdd) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Determine server name and address
	server := args[0]
	addr := args[0]
	if len(args) > 1 {
		addr = args[1]
	}

	// Validate the server name.
	if strings.Contains(server, ":") {
		return fmt.Errorf(i18n.G("Remote names may not contain colons"))
	}

	// Check for existing remote
	remote, ok := conf.Remotes[server]
	if ok {
		return fmt.Errorf(i18n.G("Remote %s exists as <%s>"), server, remote.Addr)
	}

	// Parse the URL
	var rScheme string
	var rHost string
	var rPort string

	if c.flagProtocol == "" {
		c.flagProtocol = "lxd"
	}

	// Initialize the remotes list if needed
	if conf.Remotes == nil {
		conf.Remotes = map[string]config.Remote{}
	}

	// Complex remote URL parsing
	remoteURL, err := url.Parse(addr)
	if err != nil {
		remoteURL = &url.URL{Host: addr}
	}

	// Fast track simplestreams
	if c.flagProtocol == "simplestreams" {
		if remoteURL.Scheme != "https" {
			return fmt.Errorf(i18n.G("Only https URLs are supported for simplestreams"))
		}

		conf.Remotes[server] = config.Remote{Addr: addr, Public: true, Protocol: c.flagProtocol}
		return conf.SaveConfig(c.global.confPath)
	} else if c.flagProtocol != "lxd" {
		return fmt.Errorf(i18n.G("Invalid protocol: %s"), c.flagProtocol)
	}

	// Fix broken URL parser
	if !strings.Contains(addr, "://") && remoteURL.Scheme != "" && remoteURL.Scheme != "unix" && remoteURL.Host == "" {
		remoteURL.Host = addr
		remoteURL.Scheme = ""
	}

	if remoteURL.Scheme != "" {
		if remoteURL.Scheme != "unix" && remoteURL.Scheme != "https" {
			return fmt.Errorf(i18n.G("Invalid URL scheme \"%s\" in \"%s\""), remoteURL.Scheme, addr)
		}

		rScheme = remoteURL.Scheme
	} else if addr[0] == '/' {
		rScheme = "unix"
	} else {
		if !shared.IsUnixSocket(addr) {
			rScheme = "https"
		} else {
			rScheme = "unix"
		}
	}

	if remoteURL.Host != "" {
		rHost = remoteURL.Host
	} else {
		rHost = addr
	}

	host, port, err := net.SplitHostPort(rHost)
	if err == nil {
		rHost = host
		rPort = port
	} else {
		rPort = fmt.Sprintf("%d", shared.DefaultPort)
	}

	if rScheme == "unix" {
		rHost = strings.TrimPrefix(strings.TrimPrefix(addr, "unix:"), "//")
		rPort = ""
	}

	if strings.Contains(rHost, ":") && !strings.HasPrefix(rHost, "[") {
		rHost = fmt.Sprintf("[%s]", rHost)
	}

	if rPort != "" {
		addr = rScheme + "://" + rHost + ":" + rPort
	} else {
		addr = rScheme + "://" + rHost
	}

	// Finally, actually add the remote, almost...  If the remote is a private
	// HTTPS server then we need to ensure we have a client certificate before
	// adding the remote server.
	if rScheme != "unix" && !c.flagPublic && (c.flagAuthType == "tls" || c.flagAuthType == "") {
		if !conf.HasClientCertificate() {
			fmt.Fprintf(os.Stderr, i18n.G("Generating a client certificate. This may take a minute...")+"\n")
			err = conf.GenerateClientCertificate()
			if err != nil {
				return err
			}
		}
	}
	conf.Remotes[server] = config.Remote{Addr: addr, Protocol: c.flagProtocol, AuthType: c.flagAuthType, Domain: c.flagDomain}

	// Attempt to connect
	var d lxd.ImageServer
	if c.flagPublic {
		d, err = conf.GetImageServer(server)
	} else {
		d, err = conf.GetInstanceServer(server)
	}

	// Handle Unix socket connections
	if strings.HasPrefix(addr, "unix:") {
		if err != nil {
			return err
		}

		remote := conf.Remotes[server]
		remote.AuthType = "tls"

		// Handle project.
		project, err := c.findProject(d.(lxd.InstanceServer), c.flagProject)
		if err != nil {
			return err
		}
		remote.Project = project

		conf.Remotes[server] = remote
		return conf.SaveConfig(c.global.confPath)
	}

	// Check if the system CA worked for the TLS connection
	var certificate *x509.Certificate
	if err != nil {
		// Failed to connect using the system CA, so retrieve the remote certificate
		certificate, err = shared.GetRemoteCertificate(addr, c.global.conf.UserAgent)
		if err != nil {
			return err
		}
	}

	// Handle certificate prompt
	if certificate != nil {
		if !c.flagAcceptCert {
			digest := shared.CertFingerprint(certificate)

			fmt.Printf(i18n.G("Certificate fingerprint: %s")+"\n", digest)
			fmt.Printf(i18n.G("ok (y/n/[fingerprint])?") + " ")
			line, err := shared.ReadStdin()
			if err != nil {
				return err
			}

			if string(line) != digest {
				if len(line) < 1 || strings.ToLower(string(line[0])) == i18n.G("n") {
					return fmt.Errorf(i18n.G("Server certificate NACKed by user"))
				} else if strings.ToLower(string(line[0])) != i18n.G("y") {
					return fmt.Errorf(i18n.G("Please type 'y', 'n' or the fingerprint:"))
				}
			}
		}

		dnam := conf.ConfigPath("servercerts")
		err := os.MkdirAll(dnam, 0750)
		if err != nil {
			return fmt.Errorf(i18n.G("Could not create server cert dir"))
		}

		certf := conf.ServerCertPath(server)
		certOut, err := os.Create(certf)
		if err != nil {
			return err
		}

		pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
		certOut.Close()

		// Setup a new connection, this time with the remote certificate
		if c.flagPublic {
			d, err = conf.GetImageServer(server)
		} else {
			d, err = conf.GetInstanceServer(server)
		}

		if err != nil {
			return err
		}
	}

	// Handle public remotes
	if c.flagPublic {
		conf.Remotes[server] = config.Remote{Addr: addr, Public: true}
		return conf.SaveConfig(c.global.confPath)
	}

	if c.flagAuthType == "candid" {
		d.(lxd.InstanceServer).RequireAuthenticated(false)
	}

	// Get server information
	srv, _, err := d.(lxd.InstanceServer).GetServer()
	if err != nil {
		return err
	}

	// If not specified, default authentication to Candid
	if c.flagAuthType == "" {
		if !srv.Public && shared.StringInSlice("candid", srv.AuthMethods) {
			c.flagAuthType = "candid"

			// Update the remote configuration
			remote := conf.Remotes[server]
			remote.AuthType = c.flagAuthType
			conf.Remotes[server] = remote

			// Re-setup the client
			d, err = conf.GetInstanceServer(server)
			if err != nil {
				return err
			}

			d.(lxd.InstanceServer).RequireAuthenticated(false)

			srv, _, err = d.(lxd.InstanceServer).GetServer()
			if err != nil {
				return err
			}
		} else {
			c.flagAuthType = "tls"

			// Update the remote configuration
			remote := conf.Remotes[server]
			remote.AuthType = c.flagAuthType
			conf.Remotes[server] = remote
		}
	}

	if !srv.Public && !shared.StringInSlice(c.flagAuthType, srv.AuthMethods) {
		return fmt.Errorf(i18n.G("Authentication type '%s' not supported by server"), c.flagAuthType)
	}

	// Detect public remotes
	if srv.Public {
		conf.Remotes[server] = config.Remote{Addr: addr, Public: true}
		return conf.SaveConfig(c.global.confPath)
	}

	// Check if additional authentication is required.
	if srv.Auth != "trusted" {
		if c.flagAuthType == "tls" {
			// Prompt for trust password
			if c.flagPassword == "" {
				fmt.Printf(i18n.G("Admin password for %s:")+" ", server)
				pwd, err := terminal.ReadPassword(0)
				if err != nil {
					/* We got an error, maybe this isn't a terminal, let's try to
					 * read it as a file */
					pwd, err = shared.ReadStdin()
					if err != nil {
						return err
					}
				}
				fmt.Println("")
				c.flagPassword = string(pwd)
			}

			// Add client certificate to trust store
			req := api.CertificatesPost{
				Password: c.flagPassword,
			}
			req.Type = api.CertificateTypeClient

			err = d.(lxd.InstanceServer).CreateCertificate(req)
			if err != nil {
				return err
			}
		} else {
			d.(lxd.InstanceServer).RequireAuthenticated(true)
		}

		// And check if trusted now
		srv, _, err = d.(lxd.InstanceServer).GetServer()
		if err != nil {
			return err
		}

		if srv.Auth != "trusted" {
			return fmt.Errorf(i18n.G("Server doesn't trust us after authentication"))
		}

		if c.flagAuthType == "tls" {
			fmt.Println(i18n.G("Client certificate stored at server:")+" ", server)
		}
	}

	// Handle project.
	remote = conf.Remotes[server]
	project, err := c.findProject(d.(lxd.InstanceServer), c.flagProject)
	if err != nil {
		return err
	}
	remote.Project = project
	conf.Remotes[server] = remote

	return conf.SaveConfig(c.global.confPath)
}

// Get default
type cmdRemoteGetDefault struct {
	global *cmdGlobal
	remote *cmdRemote
}

func (c *cmdRemoteGetDefault) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get-default")
	cmd.Short = i18n.G("Show the default remote")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show the default remote`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdRemoteGetDefault) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 0)
	if exit {
		return err
	}

	// Show the default remote
	fmt.Println(conf.DefaultRemote)

	return nil
}

// List
type cmdRemoteList struct {
	global *cmdGlobal
	remote *cmdRemote

	flagFormat string
}

func (c *cmdRemoteList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List the available remotes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List the available remotes`))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml)")+"``")

	return cmd
}

func (c *cmdRemoteList) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 0)
	if exit {
		return err
	}

	// List the remotes
	data := [][]string{}
	for name, rc := range conf.Remotes {
		strPublic := i18n.G("NO")
		if rc.Public {
			strPublic = i18n.G("YES")
		}

		strStatic := i18n.G("NO")
		if rc.Static {
			strStatic = i18n.G("YES")
		}

		strGlobal := i18n.G("NO")
		if rc.Global {
			strGlobal = i18n.G("YES")
		}

		if rc.Protocol == "" {
			rc.Protocol = "lxd"
		}

		if rc.AuthType == "" {
			if strings.HasPrefix(rc.Addr, "unix:") {
				rc.AuthType = "file access"
			} else if rc.Protocol == "simplestreams" {
				rc.AuthType = "none"
			} else {
				rc.AuthType = "tls"
			}
		}

		if rc.AuthType == "candid" && rc.Domain != "" {
			rc.AuthType = fmt.Sprintf("%s (%s)", rc.AuthType, rc.Domain)
		}

		strName := name
		if name == conf.DefaultRemote {
			strName = fmt.Sprintf("%s (%s)", name, i18n.G("current"))
		}
		data = append(data, []string{strName, rc.Addr, rc.Protocol, rc.AuthType, strPublic, strStatic, strGlobal})
	}
	sort.Sort(byName(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("URL"),
		i18n.G("PROTOCOL"),
		i18n.G("AUTH TYPE"),
		i18n.G("PUBLIC"),
		i18n.G("STATIC"),
		i18n.G("GLOBAL"),
	}

	return utils.RenderTable(c.flagFormat, header, data, conf.Remotes)
}

// Rename
type cmdRemoteRename struct {
	global *cmdGlobal
	remote *cmdRemote
}

func (c *cmdRemoteRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("<remote> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename remotes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename remotes`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdRemoteRename) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Rename the remote
	rc, ok := conf.Remotes[args[0]]
	if !ok {
		return fmt.Errorf(i18n.G("Remote %s doesn't exist"), args[0])
	}

	if rc.Static {
		return fmt.Errorf(i18n.G("Remote %s is static and cannot be modified"), args[0])
	}

	if _, ok := conf.Remotes[args[1]]; ok {
		return fmt.Errorf(i18n.G("Remote %s already exists"), args[1])
	}

	// Rename the certificate file
	oldPath := conf.ServerCertPath(args[0])
	newPath := conf.ServerCertPath(args[1])
	if shared.PathExists(oldPath) {
		if conf.Remotes[args[0]].Global == true {
			err := conf.CopyGlobalCert(args[0], args[1])
			if err != nil {
				return err
			}
			rc.Global = false
		} else {
			err := os.Rename(oldPath, newPath)
			if err != nil {
				return err
			}
		}
	}

	conf.Remotes[args[1]] = rc
	delete(conf.Remotes, args[0])

	if conf.DefaultRemote == args[0] {
		conf.DefaultRemote = args[1]
	}

	return conf.SaveConfig(c.global.confPath)
}

// Remove
type cmdRemoteRemove struct {
	global *cmdGlobal
	remote *cmdRemote
}

func (c *cmdRemoteRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("<remote>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove remotes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove remotes`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdRemoteRemove) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Remove the remote
	rc, ok := conf.Remotes[args[0]]
	if !ok {
		return fmt.Errorf(i18n.G("Remote %s doesn't exist"), args[0])
	}

	if rc.Static {
		return fmt.Errorf(i18n.G("Remote %s is static and cannot be modified"), args[0])
	}

	if rc.Global {
		return fmt.Errorf(i18n.G("Remote %s is global and cannot be removed"), args[0])
	}

	if conf.DefaultRemote == args[0] {
		return fmt.Errorf(i18n.G("Can't remove the default remote"))
	}

	delete(conf.Remotes, args[0])

	os.Remove(conf.ServerCertPath(args[0]))
	os.Remove(conf.CookiesPath(args[0]))

	return conf.SaveConfig(c.global.confPath)
}

// Set default
type cmdRemoteSwitch struct {
	global *cmdGlobal
	remote *cmdRemote
}

func (c *cmdRemoteSwitch) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Aliases = []string{"set-default"}
	cmd.Use = usage("switch", i18n.G("<remote>"))
	cmd.Short = i18n.G("Switch the default remote")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Switch the default remote`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdRemoteSwitch) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Set the default remote
	_, ok := conf.Remotes[args[0]]
	if !ok {
		return fmt.Errorf(i18n.G("Remote %s doesn't exist"), args[0])
	}

	conf.DefaultRemote = args[0]

	return conf.SaveConfig(c.global.confPath)
}

// Set URL
type cmdRemoteSetURL struct {
	global *cmdGlobal
	remote *cmdRemote
}

func (c *cmdRemoteSetURL) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set-url", i18n.G("<remote> <URL>"))
	cmd.Short = i18n.G("Set the URL for the remote")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set the URL for the remote`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdRemoteSetURL) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Set the URL
	rc, ok := conf.Remotes[args[0]]
	if !ok {
		return fmt.Errorf(i18n.G("Remote %s doesn't exist"), args[0])
	}

	if rc.Static {
		return fmt.Errorf(i18n.G("Remote %s is static and cannot be modified"), args[0])
	}

	remote := conf.Remotes[args[0]]
	if remote.Global == true {
		err := conf.CopyGlobalCert(args[0], args[0])
		if err != nil {
			return err
		}
		remote.Global = false
		conf.Remotes[args[0]] = remote
	}

	remote.Addr = args[1]
	conf.Remotes[args[0]] = remote

	return conf.SaveConfig(c.global.confPath)
}
