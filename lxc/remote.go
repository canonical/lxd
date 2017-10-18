package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/olekukonko/tablewriter"

	"golang.org/x/crypto/ssh/terminal"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/logger"
)

type remoteCmd struct {
	acceptCert bool
	password   string
	public     bool
	protocol   string
	authType   string
}

func (c *remoteCmd) showByDefault() bool {
	return true
}

func (c *remoteCmd) usage() string {
	return i18n.G(
		`Usage: lxc remote <subcommand> [options]

Manage the list of remote LXD servers.

lxc remote add [<remote>] <IP|FQDN|URL> [--accept-certificate] [--password=PASSWORD] [--public] [--protocol=PROTOCOL] [--auth-type=AUTH_TYPE]
    Add the remote <remote> at <url>.

lxc remote remove <remote>
    Remove the remote <remote>.

lxc remote list
    List all remotes.

lxc remote rename <old name> <new name>
    Rename remote <old name> to <new name>.

lxc remote set-url <remote> <url>
    Update <remote>'s url to <url>.

lxc remote set-default <remote>
    Set the default remote.

lxc remote get-default
    Print the default remote.`)
}

func (c *remoteCmd) flags() {
	gnuflag.BoolVar(&c.acceptCert, "accept-certificate", false, i18n.G("Accept certificate"))
	gnuflag.StringVar(&c.password, "password", "", i18n.G("Remote admin password"))
	gnuflag.StringVar(&c.protocol, "protocol", "", i18n.G("Server protocol (lxd or simplestreams)"))
	gnuflag.StringVar(&c.authType, "auth-type", "", i18n.G("Server authentication type (tls or macaroons)"))
	gnuflag.BoolVar(&c.public, "public", false, i18n.G("Public image server"))
}

func (c *remoteCmd) addServer(conf *config.Config, server string, addr string, acceptCert bool, password string, public bool, protocol string, authType string) error {
	var rScheme string
	var rHost string
	var rPort string

	if protocol == "" {
		protocol = "lxd"
	}
	if authType == "" {
		authType = "tls"
	}

	// Setup the remotes list
	if conf.Remotes == nil {
		conf.Remotes = make(map[string]config.Remote)
	}

	/* Complex remote URL parsing */
	remoteURL, err := url.Parse(addr)
	if err != nil {
		remoteURL = &url.URL{Host: addr}
	}

	// Fast track simplestreams
	if protocol == "simplestreams" {
		if remoteURL.Scheme != "https" {
			return fmt.Errorf(i18n.G("Only https URLs are supported for simplestreams"))
		}

		conf.Remotes[server] = config.Remote{Addr: addr, Public: true, Protocol: protocol}
		return nil
	} else if protocol != "lxd" {
		return fmt.Errorf(i18n.G("Invalid protocol: %s"), protocol)
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
		rPort = shared.DefaultPort
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
	if rScheme != "unix" && !public && authType == "tls" {
		if !conf.HasClientCertificate() {
			fmt.Fprintf(os.Stderr, i18n.G("Generating a client certificate. This may take a minute...")+"\n")
			err = conf.GenerateClientCertificate()
			if err != nil {
				return err
			}
		}
	}
	conf.Remotes[server] = config.Remote{Addr: addr, Protocol: protocol, AuthType: authType}

	// Attempt to connect
	var d lxd.ImageServer
	if public {
		d, err = conf.GetImageServer(server)
	} else {
		d, err = conf.GetContainerServer(server)
	}

	// Handle Unix socket connections
	if strings.HasPrefix(addr, "unix:") {
		return err
	}

	// Check if the system CA worked for the TLS connection
	var certificate *x509.Certificate
	if err != nil {
		// Failed to connect using the system CA, so retrieve the remote certificate
		certificate, err = shared.GetRemoteCertificate(addr)
		if err != nil {
			return err
		}
	}

	// Handle certificate prompt
	if certificate != nil {
		if !acceptCert {
			digest := shared.CertFingerprint(certificate)

			fmt.Printf(i18n.G("Certificate fingerprint: %s")+"\n", digest)
			fmt.Printf(i18n.G("ok (y/n)?") + " ")
			line, err := shared.ReadStdin()
			if err != nil {
				return err
			}

			if len(line) < 1 || line[0] != 'y' && line[0] != 'Y' {
				return fmt.Errorf(i18n.G("Server certificate NACKed by user"))
			}
		}

		dnam := conf.ConfigPath("servercerts")
		err := os.MkdirAll(dnam, 0750)
		if err != nil {
			return fmt.Errorf(i18n.G("Could not create server cert dir"))
		}

		certf := fmt.Sprintf("%s/%s.crt", dnam, server)
		certOut, err := os.Create(certf)
		if err != nil {
			return err
		}

		pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
		certOut.Close()

		// Setup a new connection, this time with the remote certificate
		if public {
			d, err = conf.GetImageServer(server)
		} else {
			d, err = conf.GetContainerServer(server)
		}

		if err != nil {
			return err
		}
	}

	// Handle public remotes
	if public {
		conf.Remotes[server] = config.Remote{Addr: addr, Public: true}
		return nil
	}

	if authType == "macaroons" {
		d.(lxd.ContainerServer).RequireAuthenticated(false)
	}

	// Get server information
	srv, _, err := d.(lxd.ContainerServer).GetServer()
	if err != nil {
		return err
	}

	if !srv.Public && !shared.StringInSlice(authType, srv.AuthMethods) {
		return fmt.Errorf(i18n.G("Authentication type '%s' not supported by server"), authType)
	}

	// Detect public remotes
	if srv.Public {
		conf.Remotes[server] = config.Remote{Addr: addr, Public: true}
		return nil
	}

	// Check if our cert is already trusted
	if srv.Auth == "trusted" {
		return nil
	}

	if authType == "tls" {
		// Prompt for trust password
		if password == "" {
			fmt.Printf(i18n.G("Admin password for %s: "), server)
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
			password = string(pwd)
		}

		// Add client certificate to trust store
		req := api.CertificatesPost{
			Password: password,
		}
		req.Type = "client"

		err = d.(lxd.ContainerServer).CreateCertificate(req)
		if err != nil {
			return err
		}
	} else {
		d.(lxd.ContainerServer).RequireAuthenticated(true)
	}

	// And check if trusted now
	srv, _, err = d.(lxd.ContainerServer).GetServer()
	if err != nil {
		return err
	}

	if srv.Auth != "trusted" {
		return fmt.Errorf(i18n.G("Server doesn't trust us after authentication"))
	}

	if authType == "tls" {
		fmt.Println(i18n.G("Client certificate stored at server: "), server)
	}
	return nil
}

func (c *remoteCmd) removeCertificate(conf *config.Config, remote string) {
	certf := conf.ServerCertPath(remote)
	logger.Debugf("Trying to remove %s", certf)

	os.Remove(certf)
}

func (c *remoteCmd) run(conf *config.Config, args []string) error {
	if len(args) < 1 {
		return errUsage
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			return errArgs
		}

		remote := args[1]
		fqdn := args[1]
		if len(args) > 2 {
			fqdn = args[2]
		}

		if rc, ok := conf.Remotes[remote]; ok {
			return fmt.Errorf(i18n.G("remote %s exists as <%s>"), remote, rc.Addr)
		}

		err := c.addServer(conf, remote, fqdn, c.acceptCert, c.password, c.public, c.protocol, c.authType)
		if err != nil {
			delete(conf.Remotes, remote)
			c.removeCertificate(conf, remote)
			return err
		}

	case "remove":
		if len(args) != 2 {
			return errArgs
		}

		rc, ok := conf.Remotes[args[1]]
		if !ok {
			return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[1])
		}

		if rc.Static {
			return fmt.Errorf(i18n.G("remote %s is static and cannot be modified"), args[1])
		}

		if conf.DefaultRemote == args[1] {
			return fmt.Errorf(i18n.G("can't remove the default remote"))
		}

		delete(conf.Remotes, args[1])

		c.removeCertificate(conf, args[1])

	case "list":
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

			if rc.Protocol == "" {
				rc.Protocol = "lxd"
			}
			if rc.AuthType == "" && !rc.Public {
				rc.AuthType = "tls"
			}

			strName := name
			if name == conf.DefaultRemote {
				strName = fmt.Sprintf("%s (%s)", name, i18n.G("default"))
			}
			data = append(data, []string{strName, rc.Addr, rc.Protocol, rc.AuthType, strPublic, strStatic})
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetAutoWrapText(false)
		table.SetAlignment(tablewriter.ALIGN_LEFT)
		table.SetRowLine(true)
		table.SetHeader([]string{
			i18n.G("NAME"),
			i18n.G("URL"),
			i18n.G("PROTOCOL"),
			i18n.G("AUTH TYPE"),
			i18n.G("PUBLIC"),
			i18n.G("STATIC")})
		sort.Sort(byName(data))
		table.AppendBulk(data)
		table.Render()

		return nil

	case "rename":
		if len(args) != 3 {
			return errArgs
		}

		rc, ok := conf.Remotes[args[1]]
		if !ok {
			return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[1])
		}

		if rc.Static {
			return fmt.Errorf(i18n.G("remote %s is static and cannot be modified"), args[1])
		}

		if _, ok := conf.Remotes[args[2]]; ok {
			return fmt.Errorf(i18n.G("remote %s already exists"), args[2])
		}

		// Rename the certificate file
		oldPath := filepath.Join(conf.ConfigPath("servercerts"), fmt.Sprintf("%s.crt", args[1]))
		newPath := filepath.Join(conf.ConfigPath("servercerts"), fmt.Sprintf("%s.crt", args[2]))
		if shared.PathExists(oldPath) {
			err := os.Rename(oldPath, newPath)
			if err != nil {
				return err
			}
		}

		conf.Remotes[args[2]] = rc
		delete(conf.Remotes, args[1])

		if conf.DefaultRemote == args[1] {
			conf.DefaultRemote = args[2]
		}

	case "set-url":
		if len(args) != 3 {
			return errArgs
		}

		rc, ok := conf.Remotes[args[1]]
		if !ok {
			return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[1])
		}

		if rc.Static {
			return fmt.Errorf(i18n.G("remote %s is static and cannot be modified"), args[1])
		}

		conf.Remotes[args[1]] = config.Remote{Addr: args[2]}

	case "set-default":
		if len(args) != 2 {
			return errArgs
		}

		_, ok := conf.Remotes[args[1]]
		if !ok {
			return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[1])
		}
		conf.DefaultRemote = args[1]

	case "get-default":
		if len(args) != 1 {
			return errArgs
		}
		fmt.Println(conf.DefaultRemote)
		return nil
	default:
		return errArgs
	}

	return conf.SaveConfig(configPath)
}
