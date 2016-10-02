package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/olekukonko/tablewriter"

	"golang.org/x/crypto/ssh/terminal"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type remoteCmd struct {
	acceptCert bool
	password   string
	public     bool
	protocol   string
}

func (c *remoteCmd) showByDefault() bool {
	return true
}

func (c *remoteCmd) usage() string {
	return i18n.G(
		`Manage remote LXD servers.

lxc remote add <name> <url> [--accept-certificate] [--password=PASSWORD]
                            [--public] [--protocol=PROTOCOL]                Add the remote <name> at <url>.
lxc remote remove <name>                                                    Remove the remote <name>.
lxc remote list                                                             List all remotes.
lxc remote rename <old> <new>                                               Rename remote <old> to <new>.
lxc remote set-url <name> <url>                                             Update <name>'s url to <url>.
lxc remote set-default <name>                                               Set the default remote.
lxc remote get-default                                                      Print the default remote.`)
}

func (c *remoteCmd) flags() {
	gnuflag.BoolVar(&c.acceptCert, "accept-certificate", false, i18n.G("Accept certificate"))
	gnuflag.StringVar(&c.password, "password", "", i18n.G("Remote admin password"))
	gnuflag.StringVar(&c.protocol, "protocol", "", i18n.G("Server protocol (lxd or simplestreams)"))
	gnuflag.BoolVar(&c.public, "public", false, i18n.G("Public image server"))
}

func generateClientCertificate(config *lxd.Config) error {
	// Generate a client certificate if necessary.  The default repositories are
	// either local or public, neither of which requires a client certificate.
	// Generation of the cert is delayed to avoid unnecessary overhead, e.g in
	// testing scenarios where only the default repositories are used.
	certf := config.ConfigPath("client.crt")
	keyf := config.ConfigPath("client.key")
	if !shared.PathExists(certf) || !shared.PathExists(keyf) {
		fmt.Fprintf(os.Stderr, i18n.G("Generating a client certificate. This may take a minute...")+"\n")

		return shared.FindOrGenCert(certf, keyf, true)
	}
	return nil
}

func getRemoteCertificate(address string) (*x509.Certificate, error) {
	// Setup a permissive TLS config
	tlsConfig, err := shared.GetTLSConfig("", "", "", nil)
	if err != nil {
		return nil, err
	}

	tlsConfig.InsecureSkipVerify = true
	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
		Dial:            shared.RFC3493Dialer,
		Proxy:           shared.ProxyFromEnvironment,
	}

	// Connect
	client := &http.Client{Transport: tr}
	resp, err := client.Get(address)
	if err != nil {
		return nil, err
	}

	// Retrieve the certificate
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return nil, fmt.Errorf(i18n.G("Unable to read remote TLS certificate"))
	}

	return resp.TLS.PeerCertificates[0], nil
}

func (c *remoteCmd) addServer(config *lxd.Config, server string, addr string, acceptCert bool, password string, public bool, protocol string) error {
	var rScheme string
	var rHost string
	var rPort string

	// Setup the remotes list
	if config.Remotes == nil {
		config.Remotes = make(map[string]lxd.RemoteConfig)
	}

	/* Complex remote URL parsing */
	remoteURL, err := url.Parse(addr)
	if err != nil {
		return err
	}

	// Fast track simplestreams
	if protocol == "simplestreams" {
		if remoteURL.Scheme != "https" {
			return fmt.Errorf(i18n.G("Only https URLs are supported for simplestreams"))
		}

		config.Remotes[server] = lxd.RemoteConfig{Addr: addr, Public: true, Protocol: protocol}
		return nil
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
	if rScheme != "unix" && !public {
		err = generateClientCertificate(config)
		if err != nil {
			return err
		}
	}
	config.Remotes[server] = lxd.RemoteConfig{Addr: addr, Protocol: protocol}

	remote := config.ParseRemote(server)
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	if strings.HasPrefix(addr, "unix:") {
		// NewClient succeeded so there was a lxd there (we fingered
		// it) so just accept it
		return nil
	}

	var certificate *x509.Certificate

	/* Attempt to connect using the system root CA */
	_, err = d.GetServerConfig()
	if err != nil {
		// Failed to connect using the system CA, so retrieve the remote certificate
		certificate, err = getRemoteCertificate(addr)
		if err != nil {
			return err
		}
	}

	if certificate != nil {
		if !acceptCert {
			digest := sha256.Sum256(certificate.Raw)

			fmt.Printf(i18n.G("Certificate fingerprint: %x")+"\n", digest)
			fmt.Printf(i18n.G("ok (y/n)?") + " ")
			line, err := shared.ReadStdin()
			if err != nil {
				return err
			}

			if len(line) < 1 || line[0] != 'y' && line[0] != 'Y' {
				return fmt.Errorf(i18n.G("Server certificate NACKed by user"))
			}
		}

		dnam := d.Config.ConfigPath("servercerts")
		err := os.MkdirAll(dnam, 0750)
		if err != nil {
			return fmt.Errorf(i18n.G("Could not create server cert dir"))
		}

		certf := fmt.Sprintf("%s/%s.crt", dnam, d.Name)
		certOut, err := os.Create(certf)
		if err != nil {
			return err
		}

		pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
		certOut.Close()

		// Setup a new connection, this time with the remote certificate
		d, err = lxd.NewClient(config, remote)
		if err != nil {
			return err
		}
	}

	if d.IsPublic() || public {
		config.Remotes[server] = lxd.RemoteConfig{Addr: addr, Public: true}

		if _, err := d.GetServerConfig(); err != nil {
			return err
		}

		return nil
	}

	if d.AmTrusted() {
		// server already has our cert, so we're done
		return nil
	}

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

	err = d.AddMyCertToServer(password)
	if err != nil {
		return err
	}

	if !d.AmTrusted() {
		return fmt.Errorf(i18n.G("Server doesn't trust us after adding our cert"))
	}

	fmt.Println(i18n.G("Client certificate stored at server: "), server)
	return nil
}

func (c *remoteCmd) removeCertificate(config *lxd.Config, remote string) {
	certf := config.ServerCertPath(remote)
	shared.LogDebugf("Trying to remove %s", certf)

	os.Remove(certf)
}

func (c *remoteCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	switch args[0] {
	case "add":
		if len(args) < 3 {
			return errArgs
		}

		if rc, ok := config.Remotes[args[1]]; ok {
			return fmt.Errorf(i18n.G("remote %s exists as <%s>"), args[1], rc.Addr)
		}

		err := c.addServer(config, args[1], args[2], c.acceptCert, c.password, c.public, c.protocol)
		if err != nil {
			delete(config.Remotes, args[1])
			c.removeCertificate(config, args[1])
			return err
		}

	case "remove":
		if len(args) != 2 {
			return errArgs
		}

		rc, ok := config.Remotes[args[1]]
		if !ok {
			return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[1])
		}

		if rc.Static {
			return fmt.Errorf(i18n.G("remote %s is static and cannot be modified"), args[1])
		}

		if config.DefaultRemote == args[1] {
			return fmt.Errorf(i18n.G("can't remove the default remote"))
		}

		delete(config.Remotes, args[1])

		c.removeCertificate(config, args[1])

	case "list":
		data := [][]string{}
		for name, rc := range config.Remotes {
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

			strName := name
			if name == config.DefaultRemote {
				strName = fmt.Sprintf("%s (%s)", name, i18n.G("default"))
			}
			data = append(data, []string{strName, rc.Addr, rc.Protocol, strPublic, strStatic})
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetAutoWrapText(false)
		table.SetAlignment(tablewriter.ALIGN_LEFT)
		table.SetRowLine(true)
		table.SetHeader([]string{
			i18n.G("NAME"),
			i18n.G("URL"),
			i18n.G("PROTOCOL"),
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

		rc, ok := config.Remotes[args[1]]
		if !ok {
			return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[1])
		}

		if rc.Static {
			return fmt.Errorf(i18n.G("remote %s is static and cannot be modified"), args[1])
		}

		if _, ok := config.Remotes[args[2]]; ok {
			return fmt.Errorf(i18n.G("remote %s already exists"), args[2])
		}

		// Rename the certificate file
		oldPath := filepath.Join(config.ConfigPath("servercerts"), fmt.Sprintf("%s.crt", args[1]))
		newPath := filepath.Join(config.ConfigPath("servercerts"), fmt.Sprintf("%s.crt", args[2]))
		if shared.PathExists(oldPath) {
			err := os.Rename(oldPath, newPath)
			if err != nil {
				return err
			}
		}

		config.Remotes[args[2]] = rc
		delete(config.Remotes, args[1])

		if config.DefaultRemote == args[1] {
			config.DefaultRemote = args[2]
		}

	case "set-url":
		if len(args) != 3 {
			return errArgs
		}

		rc, ok := config.Remotes[args[1]]
		if !ok {
			return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[1])
		}

		if rc.Static {
			return fmt.Errorf(i18n.G("remote %s is static and cannot be modified"), args[1])
		}

		config.Remotes[args[1]] = lxd.RemoteConfig{Addr: args[2]}

	case "set-default":
		if len(args) != 2 {
			return errArgs
		}

		_, ok := config.Remotes[args[1]]
		if !ok {
			return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[1])
		}
		config.DefaultRemote = args[1]

	case "get-default":
		if len(args) != 1 {
			return errArgs
		}
		fmt.Println(config.DefaultRemote)
		return nil
	default:
		return errArgs
	}

	return lxd.SaveConfig(config, configPath)
}
