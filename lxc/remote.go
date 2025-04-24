package main

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdRemote struct {
	global *cmdGlobal
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdRemote) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remote")
	cmd.Short = i18n.G("Manage the list of remote servers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage the list of remote servers`))

	// Add
	remoteAddCmd := cmdRemoteAdd{global: c.global, remote: c}
	cmd.AddCommand(remoteAddCmd.command())

	// Get default
	remoteGetDefaultCmd := cmdRemoteGetDefault{global: c.global, remote: c}
	cmd.AddCommand(remoteGetDefaultCmd.command())

	// List
	remoteListCmd := cmdRemoteList{global: c.global, remote: c}
	cmd.AddCommand(remoteListCmd.command())

	// Rename
	remoteRenameCmd := cmdRemoteRename{global: c.global, remote: c}
	cmd.AddCommand(remoteRenameCmd.command())

	// Remove
	remoteRemoveCmd := cmdRemoteRemove{global: c.global, remote: c}
	cmd.AddCommand(remoteRemoveCmd.command())

	// Set default
	remoteSwitchCmd := cmdRemoteSwitch{global: c.global, remote: c}
	cmd.AddCommand(remoteSwitchCmd.command())

	// Set URL
	remoteSetURLCmd := cmdRemoteSetURL{global: c.global, remote: c}
	cmd.AddCommand(remoteSetURLCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdRemoteAdd struct {
	global *cmdGlobal
	remote *cmdRemote

	flagAcceptCert bool
	flagPassword   string
	flagToken      string
	flagPublic     bool
	flagProtocol   string
	flagAuthType   string
	flagProject    string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdRemoteAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>] <IP|FQDN|URL|token>"))
	cmd.Short = i18n.G("Add new remote servers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add new remote servers

URL for remote resources must be HTTPS (https://).

Basic authentication can be used when combined with the "simplestreams" protocol:
  lxc remote add some-name https://LOGIN:PASSWORD@example.com/some/path --protocol=simplestreams
`))

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagAcceptCert, "accept-certificate", false, i18n.G("Accept certificate"))
	cmd.Flags().StringVar(&c.flagPassword, "password", "", i18n.G("Remote admin password")+"``")
	cmd.Flags().StringVar(&c.flagToken, "token", "", i18n.G("Remote trust token")+"``")
	cmd.Flags().StringVar(&c.flagProtocol, "protocol", "", i18n.G("Server protocol (lxd or simplestreams)")+"``")
	cmd.Flags().StringVar(&c.flagAuthType, "auth-type", "", i18n.G("Server authentication type (tls or oidc)")+"``")
	cmd.Flags().BoolVar(&c.flagPublic, "public", false, i18n.G("Public image server"))
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
			if shared.ValueInSlice("default", names) {
				// If we have access to the default project, use it.
				return "", nil
			}

			// Let's ask the user.
			fmt.Println(i18n.G("Available projects:"))
			for _, name := range names {
				fmt.Println(" - " + name)
			}

			return c.global.asker.AskChoice(i18n.G("Name of the project to use for this remote:")+" ", names, "")
		}

		return "", nil
	}

	_, _, err := d.GetProject(project)
	if err != nil {
		return "", err
	}

	return project, nil
}

func (c *cmdRemoteAdd) runToken(addr string, server string, token string, rawToken *api.CertificateAddToken) error {
	conf := c.global.conf

	// Certificate cannot be blindly accepted when using a trust token.
	if c.flagAcceptCert {
		return errors.New(i18n.G("The --accept-certificate flag is not supported when adding a remote using a trust token"))
	}

	if !conf.HasClientCertificate() {
		fmt.Fprint(os.Stderr, i18n.G("Generating a client certificate. This may take a minute...")+"\n")
		err := conf.GenerateClientCertificate()
		if err != nil {
			return err
		}
	}

	// If address is provided, use token on that specific address.
	if addr != "" {
		return c.addRemoteFromToken(addr, server, token, *rawToken)
	}

	// Otherwise, iterate over all addresses within the token.
	for _, addr := range rawToken.Addresses {
		addr = "https://" + addr

		err := c.addRemoteFromToken(addr, server, token, *rawToken)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusServiceUnavailable) {
				continue
			}

			return err
		}

		return nil
	}

	// Finally, fallback to manual input.
	fmt.Println(i18n.G("All server addresses are unavailable"))
	fmt.Print(i18n.G("Please provide an alternate server address (empty to abort):") + " ")

	line, err := shared.ReadStdin()
	if err != nil {
		return err
	}

	if len(line) == 0 {
		return errors.New(i18n.G("Failed to add remote"))
	}

	err = c.addRemoteFromToken(string(line), server, token, *rawToken)
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdRemoteAdd) addRemoteFromToken(addr string, server string, token string, rawToken api.CertificateAddToken) error {
	conf := c.global.conf

	var certificate *x509.Certificate
	var err error

	conf.Remotes[server] = config.Remote{Addr: addr, Protocol: c.flagProtocol, AuthType: c.flagAuthType}

	_, err = conf.GetInstanceServer(server)
	if err != nil {
		certificate, err = shared.GetRemoteCertificate(addr, c.global.conf.UserAgent)
		if err != nil {
			return api.StatusErrorf(http.StatusServiceUnavailable, "%s: %w", i18n.G("Unavailable remote server"), err)
		}

		certDigest := shared.CertFingerprint(certificate)
		if rawToken.Fingerprint != certDigest {
			return fmt.Errorf(i18n.G("Certificate fingerprint mismatch between certificate token and server %q"), addr)
		}

		dnam := conf.ConfigPath("servercerts")
		err := os.MkdirAll(dnam, 0750)
		if err != nil {
			return errors.New(i18n.G("Could not create server cert dir"))
		}

		certf := conf.ServerCertPath(server)

		certOut, err := os.Create(certf)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to create %q: %w"), certf, err)
		}

		err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to write server cert file %q: %w"), certf, err)
		}

		err = certOut.Close()
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to close server cert file %q: %w"), certf, err)
		}
	}

	d, err := conf.GetInstanceServer(server)
	if err != nil {
		return api.StatusErrorf(http.StatusServiceUnavailable, "%s: %w", i18n.G("Unavailable remote server"), err)
	}

	// Add client certificate to trust store. Even if we are already trusted (src.Auth == "trusted"),
	// we want to send the token to invalidate it. Therefore, we can ignore the conflict error, which
	// is thrown if we are trying to add a client cert that is already trusted by LXD remote.
	//
	// If "type" is not set on the token, the token was issued by the certificates API and CreateCertificate should be
	// called. If "type" is set, the token was issued by the auth API and CreateIdentityTLS should be called.
	if rawToken.Type == "" {
		req := api.CertificatesPost{}
		if d.HasExtension("explicit_trust_token") {
			req.TrustToken = token
		} else {
			req.Password = token //nolint:staticcheck
		}

		err = d.CreateCertificate(req)
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return err
		}
	} else {
		req := api.IdentitiesTLSPost{
			TrustToken: token,
		}

		err = d.CreateIdentityTLS(req)
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return err
		}
	}

	// And check if trusted now.
	srv, _, err := d.GetServer()
	if err != nil {
		return err
	}

	if srv.Auth != "trusted" {
		return errors.New(i18n.G("Server doesn't trust us after authentication"))
	}

	// Handle project.
	remote := conf.Remotes[server]
	project, err := c.findProject(d, c.flagProject)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to find project: %w"), err)
	}

	remote.Project = project
	conf.Remotes[server] = remote

	return conf.SaveConfig(c.global.confPath)
}

// Run is used in the RunE field of the cobra.Command returned by Command.
func (c *cmdRemoteAdd) run(cmd *cobra.Command, args []string) error {
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

	if len(addr) == 0 {
		return errors.New(i18n.G("Remote address must not be empty"))
	}

	// Trust token cannot be used when auth type is set to OIDC.
	if c.flagToken != "" && c.flagAuthType == "oidc" {
		return errors.New(i18n.G("Trust token cannot be used with OIDC authentication"))
	}

	// Trust token cannot be used for public remotes.
	if c.flagToken != "" && c.flagPublic {
		return errors.New(i18n.G("Trust token cannot be used for public remotes"))
	}

	// Certificate cannot be blindly accepted when using a trust token.
	if c.flagToken != "" && c.flagAcceptCert {
		return errors.New(i18n.G("The --accept-certificate flag is not supported when adding a remote using a trust token"))
	}

	// Validate the server name.
	if strings.Contains(server, ":") {
		return errors.New(i18n.G("Remote names may not contain colons"))
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

	// Check if the first argument is a trust token. In such case, we need to
	// decode it and use it to connect to the remote.
	rawToken, err := shared.CertificateTokenDecode(addr)
	if err == nil {
		return c.runToken("", server, addr, rawToken)
	}

	// Complex remote URL parsing
	remoteURL, err := url.Parse(addr)
	if err != nil {
		remoteURL = &url.URL{Host: addr}
	}

	// Fast track simplestreams
	if c.flagProtocol == "simplestreams" {
		if remoteURL.Scheme != "https" {
			return errors.New(i18n.G("Only https URLs are supported for simplestreams"))
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
		rPort = fmt.Sprint(shared.HTTPSDefaultPort)
	}

	if rScheme == "unix" {
		rHost = strings.TrimPrefix(strings.TrimPrefix(addr, "unix:"), "//")
		rPort = ""
	}

	if strings.Contains(rHost, ":") && !strings.HasPrefix(rHost, "[") {
		rHost = "[" + rHost + "]"
	}

	if rPort != "" {
		addr = rScheme + "://" + rHost + ":" + rPort
	} else {
		addr = rScheme + "://" + rHost
	}

	// Finally, actually add the remote, almost...  If the remote is a private
	// HTTPS server then we need to ensure we have a client certificate before
	// adding the remote server.
	if rScheme != "unix" && !c.flagPublic && (c.flagAuthType == api.AuthenticationMethodTLS || c.flagAuthType == "") {
		if !conf.HasClientCertificate() {
			fmt.Fprint(os.Stderr, i18n.G("Generating a client certificate. This may take a minute...")+"\n")
			err = conf.GenerateClientCertificate()
			if err != nil {
				return err
			}
		}
	}

	conf.Remotes[server] = config.Remote{Addr: addr, Protocol: c.flagProtocol, AuthType: c.flagAuthType}

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
		remote.AuthType = api.AuthenticationMethodTLS

		// Handle project.
		project, err := c.findProject(d.(lxd.InstanceServer), c.flagProject)
		if err != nil {
			return err
		}

		remote.Project = project

		conf.Remotes[server] = remote
		return conf.SaveConfig(c.global.confPath)
	}

	// Handle adding a remote with trust token.
	if c.flagToken != "" {
		rawToken, err := shared.CertificateTokenDecode(c.flagToken)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to decode trust token: %w"), err)
		}

		return c.runToken(addr, server, c.flagToken, rawToken)
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
		// Prompt for certificate acceptance if user did not allow us to blindly
		// accept the remote certificate.
		if !c.flagAcceptCert {
			digest := shared.CertFingerprint(certificate)

			fmt.Printf("%s: %s\n", i18n.G("Certificate fingerprint"), digest)
			fmt.Print(i18n.G("ok (y/n/[fingerprint])?") + " ")
			for {
				line, err := shared.ReadStdin()
				if err != nil {
					return err
				}

				// Continue with adding the remote if digest matches, or the user
				// confirmed a fingerprint.
				if string(line) == digest || strings.ToLower(string(line[0])) == i18n.G("y") {
					break
				}

				// If the input length matches the certificate fingerprint length
				// but the fingerprints do not match, return an error. This ensures
				// the scripts do not hang if incorrect fingerprint is provided.
				if len(line) == len(digest) {
					return errors.New(i18n.G("The provided fingerprint does not match the server certificate fingerprint"))
				}

				// Error out if the user didn't confirm the fingerprint.
				if len(line) < 1 || strings.ToLower(string(line[0])) == i18n.G("n") {
					return errors.New(i18n.G("Server certificate NACKed by user"))
				}

				// Ask again for any other invalid input.
				fmt.Print(i18n.G("Please type 'y', 'n' or the fingerprint: "))
			}
		}

		dnam := conf.ConfigPath("servercerts")
		err := os.MkdirAll(dnam, 0750)
		if err != nil {
			return errors.New(i18n.G("Could not create server cert dir"))
		}

		certf := conf.ServerCertPath(server)
		certOut, err := os.Create(certf)
		if err != nil {
			return err
		}

		err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
		if err != nil {
			return fmt.Errorf(i18n.G("Could not write server cert file %q: %w"), certf, err)
		}

		err = certOut.Close()
		if err != nil {
			return fmt.Errorf(i18n.G("Could not close server cert file %q: %w"), certf, err)
		}

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

	// Get server information
	srv, _, err := d.(lxd.InstanceServer).GetServer()
	if err != nil {
		return err
	}

	// If not specified, the preferred order of authentication is 1) OIDC 2) TLS.
	if c.flagAuthType == "" {
		if !srv.Public && shared.ValueInSlice(api.AuthenticationMethodOIDC, srv.AuthMethods) {
			c.flagAuthType = api.AuthenticationMethodOIDC
		} else {
			c.flagAuthType = api.AuthenticationMethodTLS
		}

		if shared.ValueInSlice(c.flagAuthType, []string{api.AuthenticationMethodOIDC}) {
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
			// Update the remote configuration
			remote := conf.Remotes[server]
			remote.AuthType = c.flagAuthType
			conf.Remotes[server] = remote
		}
	}

	if !srv.Public && !shared.ValueInSlice(c.flagAuthType, srv.AuthMethods) {
		return fmt.Errorf(i18n.G("Authentication type '%s' not supported by server"), c.flagAuthType)
	}

	// Detect public remotes
	if srv.Public {
		conf.Remotes[server] = config.Remote{Addr: addr, Public: true}
		return conf.SaveConfig(c.global.confPath)
	}

	// Check if additional authentication is required.
	if srv.Auth != "trusted" {
		if c.flagAuthType == api.AuthenticationMethodTLS {
			var gainTrust func() error

			// If the password flag isn't provided and the server supports the explicit_trust_token extension,
			// use the token instead and prompt for it if not present.
			if d.(lxd.InstanceServer).HasExtension("explicit_trust_token") && c.flagPassword == "" {
				// Prompt for trust token.
				token, err := c.global.asker.AskString(fmt.Sprintf(i18n.G("Trust token for %s: "), server), "", nil)
				if err != nil {
					return err
				}

				// Decode the token.
				certificateAddToken, err := shared.CertificateTokenDecode(token)
				if err != nil {
					return err
				}

				// If the type field is set it's for use with the auth API. Otherwise it's for use with the certificates API.
				if certificateAddToken.Type == "" {
					gainTrust = func() error {
						return d.(lxd.InstanceServer).CreateCertificate(api.CertificatesPost{
							Type:       api.CertificateTypeClient,
							TrustToken: token,
						})
					}
				} else {
					gainTrust = func() error {
						return d.(lxd.InstanceServer).CreateIdentityTLS(api.IdentitiesTLSPost{TrustToken: token})
					}
				}
			} else {
				// Prompt for trust password if token is not supported by the server.
				if c.flagPassword == "" {
					fmt.Printf(i18n.G("Admin password (or token) for %s:")+" ", server)
					pwd, err := term.ReadPassword(0)
					if err != nil {
						// We got an error, maybe this isn't a terminal, let's try to read it as a file.
						pwd, err = shared.ReadStdin()
						if err != nil {
							return err
						}
					}

					fmt.Println("")
					c.flagPassword = string(pwd)
				}

				gainTrust = func() error {
					return d.(lxd.InstanceServer).CreateCertificate(api.CertificatesPost{
						Type:     api.CertificateTypeClient,
						Password: c.flagPassword,
					})
				}
			}

			err = gainTrust()
			if err != nil {
				return err
			}
		} else {
			d.(lxd.InstanceServer).RequireAuthenticated(true)
		}

		// And check if trusted now.
		srv, _, err = d.(lxd.InstanceServer).GetServer()
		if err != nil {
			return err
		}

		if srv.Auth != "trusted" {
			return errors.New(i18n.G("Server doesn't trust us after authentication"))
		}

		if c.flagAuthType == api.AuthenticationMethodTLS {
			fmt.Println(i18n.G("Client certificate now trusted by server:"), server)
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

// Get default.
type cmdRemoteGetDefault struct {
	global *cmdGlobal
	remote *cmdRemote
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdRemoteGetDefault) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get-default")
	cmd.Short = i18n.G("Show the default remote")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show the default remote`))

	cmd.RunE = c.run

	return cmd
}

// Run is used in the RunE field of the cobra.Command returned by Command.
func (c *cmdRemoteGetDefault) run(cmd *cobra.Command, args []string) error {
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

// List.
type cmdRemoteList struct {
	global *cmdGlobal
	remote *cmdRemote

	flagFormat string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdRemoteList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List the available remotes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List the available remotes`))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	return cmd
}

// Run is used in the RunE field of the cobra.Command returned by Command.
func (c *cmdRemoteList) run(cmd *cobra.Command, args []string) error {
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
				rc.AuthType = api.AuthenticationMethodTLS
			}
		}

		strName := name
		if name == conf.DefaultRemote {
			strName = name + " (" + i18n.G("current") + ")"
		}

		data = append(data, []string{strName, rc.Addr, rc.Protocol, rc.AuthType, strPublic, strStatic, strGlobal})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("URL"),
		i18n.G("PROTOCOL"),
		i18n.G("AUTH TYPE"),
		i18n.G("PUBLIC"),
		i18n.G("STATIC"),
		i18n.G("GLOBAL"),
	}

	return cli.RenderTable(c.flagFormat, header, data, conf.Remotes)
}

// Rename.
type cmdRemoteRename struct {
	global *cmdGlobal
	remote *cmdRemote
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdRemoteRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("<remote> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename remotes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename remotes`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, "", false, filterStaticRemotes)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run is used in the RunE field of the cobra.Command returned by Command.
func (c *cmdRemoteRename) run(cmd *cobra.Command, args []string) error {
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

	_, ok = conf.Remotes[args[1]]
	if ok {
		return fmt.Errorf(i18n.G("Remote %s already exists"), args[1])
	}

	// Rename the certificate file
	oldPath := conf.ServerCertPath(args[0])
	newPath := conf.ServerCertPath(args[1])
	if shared.PathExists(oldPath) {
		if conf.Remotes[args[0]].Global {
			err := conf.CopyGlobalCert(args[0], args[1])
			if err != nil {
				return err
			}
		} else {
			err := os.Rename(oldPath, newPath)
			if err != nil {
				return err
			}
		}
	}

	rc.Global = false
	conf.Remotes[args[1]] = rc
	delete(conf.Remotes, args[0])

	if conf.DefaultRemote == args[0] {
		conf.DefaultRemote = args[1]
	}

	return conf.SaveConfig(c.global.confPath)
}

// Remove.
type cmdRemoteRemove struct {
	global *cmdGlobal
	remote *cmdRemote
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdRemoteRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("<remote>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove remotes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove remotes`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, "", false, filterStaticRemotes, filterGlobalRemotes, filterDefaultRemote(*c.global.conf))
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run is used in the RunE field of the cobra.Command returned by Command.
func (c *cmdRemoteRemove) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Can't remove the default remote"))
	}

	delete(conf.Remotes, args[0])

	_ = os.Remove(conf.ServerCertPath(args[0]))
	_ = os.Remove(conf.OIDCTokenPath(args[0]))

	return conf.SaveConfig(c.global.confPath)
}

// Set default.
type cmdRemoteSwitch struct {
	global *cmdGlobal
	remote *cmdRemote
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdRemoteSwitch) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Aliases = []string{"set-default"}
	cmd.Use = usage("switch", i18n.G("<remote>"))
	cmd.Short = i18n.G("Switch the default remote")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Switch the default remote`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			// It's valid to switch to a public remote, but filter them from completions to prevent leading new users down a bad path.
			return c.global.cmpRemotes(toComplete, "", false, filterDefaultRemote(*c.global.conf), filterPublicRemotes)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run is used in the RunE field of the cobra.Command returned by Command.
func (c *cmdRemoteSwitch) run(cmd *cobra.Command, args []string) error {
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

// Set URL.
type cmdRemoteSetURL struct {
	global *cmdGlobal
	remote *cmdRemote
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdRemoteSetURL) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set-url", i18n.G("<remote> <URL>"))
	cmd.Short = i18n.G("Set the URL for the remote")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set the URL for the remote`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, "", false, filterStaticRemotes)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run is used in the RunE field of the cobra.Command returned by Command.
func (c *cmdRemoteSetURL) run(cmd *cobra.Command, args []string) error {
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
	if remote.Global {
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
