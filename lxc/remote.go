package main

import (
	"crypto/sha256"
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

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type remoteCmd struct {
	httpAddr   string
	acceptCert bool
	password   string
	public     bool
}

func (c *remoteCmd) showByDefault() bool {
	return true
}

func (c *remoteCmd) usage() string {
	return i18n.G(
		`Manage remote LXD servers.

lxc remote add <name> <url> [--accept-certificate] [--password=PASSWORD] [--public]    Add the remote <name> at <url>.
lxc remote remove <name>                                                               Remove the remote <name>.
lxc remote list                                                                        List all remotes.
lxc remote rename <old> <new>                                                          Rename remote <old> to <new>.
lxc remote set-url <name> <url>                                                        Update <name>'s url to <url>.
lxc remote set-default <name>                                                          Set the default remote.
lxc remote get-default                                                                 Print the default remote.`)
}

func (c *remoteCmd) flags() {
	gnuflag.BoolVar(&c.acceptCert, "accept-certificate", false, i18n.G("Accept certificate"))
	gnuflag.StringVar(&c.password, "password", "", i18n.G("Remote admin password"))
	gnuflag.BoolVar(&c.public, "public", false, i18n.G("Public image server"))
}

func (c *remoteCmd) addServer(config *lxd.Config, server string, addr string, acceptCert bool, password string, public bool) error {
	var r_scheme string
	var r_host string
	var r_port string

	/* Complex remote URL parsing */
	remote_url, err := url.Parse(addr)
	if err != nil {
		return err
	}

	if remote_url.Scheme != "" {
		if remote_url.Scheme != "unix" && remote_url.Scheme != "https" {
			r_scheme = "https"
		} else {
			r_scheme = remote_url.Scheme
		}
	} else if addr[0] == '/' {
		r_scheme = "unix"
	} else {
		if !shared.PathExists(addr) {
			r_scheme = "https"
		} else {
			r_scheme = "unix"
		}
	}

	if remote_url.Host != "" {
		r_host = remote_url.Host
	} else {
		r_host = addr
	}

	host, port, err := net.SplitHostPort(r_host)
	if err == nil {
		r_host = host
		r_port = port
	} else {
		r_port = shared.DefaultPort
	}

	if r_scheme == "unix" {
		if addr[0:5] == "unix:" {
			if addr[0:7] == "unix://" {
				if len(addr) > 8 {
					r_host = addr[8:]
				} else {
					r_host = ""
				}
			} else {
				r_host = addr[6:]
			}
		}
		r_port = ""
	}

	if strings.Contains(r_host, ":") && !strings.HasPrefix(r_host, "[") {
		r_host = fmt.Sprintf("[%s]", r_host)
	}

	if r_port != "" {
		addr = r_scheme + "://" + r_host + ":" + r_port
	} else {
		addr = r_scheme + "://" + r_host
	}

	if config.Remotes == nil {
		config.Remotes = make(map[string]lxd.RemoteConfig)
	}

	/* Actually add the remote */
	config.Remotes[server] = lxd.RemoteConfig{Addr: addr}

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
	err = d.Finger()
	if err != nil {
		// Failed to connect using the system CA, so retrieve the remote certificate
		certificate, err = shared.GetRemoteCertificate(addr)
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

		if err := d.Finger(); err != nil {
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
	shared.Debugf("Trying to remove %s", certf)

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

		err := c.addServer(config, args[1], args[2], c.acceptCert, c.password, c.public)
		if err != nil {
			delete(config.Remotes, args[1])
			c.removeCertificate(config, args[1])
			return err
		}

	case "remove":
		if len(args) != 2 {
			return errArgs
		}

		if _, ok := config.Remotes[args[1]]; !ok {
			return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[1])
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

			strName := name
			if name == config.DefaultRemote {
				strName = fmt.Sprintf("%s (%s)", name, i18n.G("default"))
			}
			data = append(data, []string{strName, rc.Addr, strPublic})
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{
			i18n.G("NAME"),
			i18n.G("URL"),
			i18n.G("PUBLIC")})
		sort.Sort(ByName(data))
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
		_, ok := config.Remotes[args[1]]
		if !ok {
			return fmt.Errorf(i18n.G("remote %s doesn't exist"), args[1])
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
