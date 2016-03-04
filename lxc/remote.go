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

	"github.com/codegangsta/cli"
	"github.com/olekukonko/tablewriter"
	"golang.org/x/crypto/ssh/terminal"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

var commandRemote = cli.Command{
	Name:  "remote",
	Usage: i18n.G("Manage remote LXD servers."),
	Description: i18n.G(`Manage remote LXD servers.
   lxc remote add <name> <url> [--accept-certificate] [--password=PASSWORD]
                               [--public] [--protocol=PROTOCOL]                Add the remote <name> at <url>.
   lxc remote remove <name>                                                    Remove the remote <name>.
   lxc remote list                                                             List all remotes.
   lxc remote set-url <name> <url>                                             Update <name>'s url to <url>.
   lxc remote rename <old> <new>                                               Rename remote <old> to <new>.
   lxc remote set-default <name>                                               Set the default remote.
   lxc remote get-default                                                      Print the default remote.`),

	Subcommands: []cli.Command{

		cli.Command{
			Name:      "add",
			ArgsUsage: i18n.G("<name> <url> [--accept-certificate] [--password=PASSWORD] [--public] [--protocol=PROTOCOL]"),
			Usage:     i18n.G("Add the remote <name> at <url>."),

			Flags: commandGlobalFlagsWrapper(
				cli.BoolFlag{
					Name:  "accept-certificate",
					Usage: i18n.G("Accept certificate."),
				},
				cli.StringFlag{
					Name:  "password",
					Usage: i18n.G("Remote admin password."),
				},
				cli.BoolFlag{
					Name:  "public",
					Usage: i18n.G("Public image server."),
				},
				cli.StringFlag{
					Name:  "protocol",
					Usage: i18n.G("Server protocol (lxd or simplestreams)."),
				},
			),
			Action: commandWrapper(commandActionRemoteAdd),
		},

		cli.Command{
			Name:      "remove",
			ArgsUsage: i18n.G("<name>"),
			Usage:     i18n.G("Remove the remote <name>."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionRemoteRemove),
		},

		cli.Command{
			Name:      "list",
			ArgsUsage: i18n.G(""),
			Usage:     i18n.G("List all remotes."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionRemoteList),
		},

		cli.Command{
			Name:      "rename",
			ArgsUsage: i18n.G("<old> <new>"),
			Usage:     i18n.G("Rename remote <old> to <new>."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionRemoteRename),
		},

		cli.Command{
			Name:      "set-url",
			ArgsUsage: i18n.G("<name> <url>"),
			Usage:     i18n.G("Update <name>'s url to <url>."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionRemoteSetURL),
		},

		cli.Command{
			Name:      "set-default",
			ArgsUsage: i18n.G("<name>"),
			Usage:     i18n.G("Set the default remote."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionRemoteSetDefault),
		},

		cli.Command{
			Name:      "get-default",
			ArgsUsage: i18n.G(""),
			Usage:     i18n.G("Print the default remote."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionRemoteGetDefault),
		},
	},
}

func remoteRemoveCertificate(config *lxd.Config, remote string) error {
	delete(config.Remotes, remote)

	certf := config.ServerCertPath(remote)
	shared.Debugf("Trying to remove %s", certf)

	return os.Remove(certf)
}

func commandActionRemoteAdd(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	if len(args) < 2 {
		return errArgs
	}

	if rc, ok := config.Remotes[args[0]]; ok {
		return fmt.Errorf(i18n.G("remote %s exists as <%s>"), args[0], rc.Addr)
	}

	err := remoteAddServer(
		config, args[0], args[1],
		context.Bool("accept-certificate"),
		context.String("password"),
		context.Bool("public"),
		context.String("protocol"),
	)
	if err != nil {
		delete(config.Remotes, args[0])
		remoteRemoveCertificate(config, args[0])
		return err
	}

	return lxd.SaveConfig(config, commandConfigPath)
}

func commandActionRemoteRemove(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	if len(args) != 1 {
		return errArgs
	}

	rc, ok := config.Remotes[args[0]]
	if !ok {
		return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[0])
	}

	if rc.Static {
		return fmt.Errorf(i18n.G("remote %s is static and cannot be modified"), args[0])
	}

	if config.DefaultRemote == args[0] {
		return fmt.Errorf(i18n.G("can't remove the default remote"))
	}

	delete(config.Remotes, args[0])

	remoteRemoveCertificate(config, args[0])

	return lxd.SaveConfig(config, commandConfigPath)
}

func commandActionRemoteList(config *lxd.Config, context *cli.Context) error {
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
}

func commandActionRemoteRename(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	if len(args) != 2 {
		return errArgs
	}

	rc, ok := config.Remotes[args[0]]
	if !ok {
		return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[0])
	}

	if rc.Static {
		return fmt.Errorf(i18n.G("remote %s is static and cannot be modified"), args[0])
	}

	if _, ok := config.Remotes[args[1]]; ok {
		return fmt.Errorf(i18n.G("remote %s already exists"), args[1])
	}

	// Rename the certificate file
	oldPath := filepath.Join(config.ConfigPath("servercerts"), fmt.Sprintf("%s.crt", args[0]))
	newPath := filepath.Join(config.ConfigPath("servercerts"), fmt.Sprintf("%s.crt", args[1]))
	if shared.PathExists(oldPath) {
		err := os.Rename(oldPath, newPath)
		if err != nil {
			return err
		}
	}

	config.Remotes[args[1]] = rc
	delete(config.Remotes, args[0])

	if config.DefaultRemote == args[0] {
		config.DefaultRemote = args[1]
	}

	return lxd.SaveConfig(config, commandConfigPath)
}

func commandActionRemoteSetURL(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	if len(args) != 2 {
		return errArgs
	}
	rc, ok := config.Remotes[args[0]]
	if !ok {
		return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[0])
	}

	if rc.Static {
		return fmt.Errorf(i18n.G("remote %s is static and cannot be modified"), args[0])
	}

	config.Remotes[args[0]] = lxd.RemoteConfig{Addr: args[1]}

	return lxd.SaveConfig(config, commandConfigPath)
}

func commandActionRemoteSetDefault(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	if len(args) != 1 {
		return errArgs
	}

	_, ok := config.Remotes[args[0]]
	if !ok {
		return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[0])
	}
	config.DefaultRemote = args[0]

	return lxd.SaveConfig(config, commandConfigPath)
}

func commandActionRemoteGetDefault(config *lxd.Config, context *cli.Context) error {
	if len(context.Args()) != 0 {
		return errArgs
	}
	fmt.Println(config.DefaultRemote)
	return nil
}

func getRemoteCertificate(address string) (*x509.Certificate, error) {
	// Setup a permissive TLS config
	tlsConfig, err := shared.GetTLSConfig("", "", nil)
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

func remoteAddServer(config *lxd.Config, server string, addr string, acceptCert bool, password string, public bool, protocol string) error {
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

	if remoteURL.Scheme != "" {
		if remoteURL.Scheme != "unix" && remoteURL.Scheme != "https" {
			return fmt.Errorf(i18n.G("Invalid URL scheme \"%s\" in \"%s\""), remoteURL.Scheme, addr)
		}

		rScheme = remoteURL.Scheme
	} else if addr[0] == '/' {
		rScheme = "unix"
	} else {
		if !shared.PathExists(addr) {
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
		if addr[0:5] == "unix:" {
			if addr[0:7] == "unix://" {
				if len(addr) > 8 {
					rHost = addr[8:]
				} else {
					rHost = ""
				}
			} else {
				rHost = addr[6:]
			}
		}
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

	/* Actually add the remote */
	config.Remotes[server] = lxd.RemoteConfig{Addr: addr, Protocol: protocol}

	remote := config.ParseRemote(server)
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	if len(addr) > 5 && addr[0:5] == "unix:" {
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
