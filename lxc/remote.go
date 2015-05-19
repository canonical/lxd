package main

import (
	"fmt"
	"net"
	"net/url"
	"os"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
	"github.com/lxc/lxd/shared"
	"golang.org/x/crypto/ssh/terminal"
)

type remoteCmd struct {
	httpAddr   string
	acceptCert bool
	password   string
}

func (c *remoteCmd) showByDefault() bool {
	return true
}

func (c *remoteCmd) usage() string {
	return gettext.Gettext(
		"Manage remote LXD servers.\n" +
			"\n" +
			"lxc remote add <name> <url> [--accept-certificate] [--password=PASSWORD]  Add the remote <name> at <url>.\n" +
			"lxc remote remove <name>                                                  Remove the remote <name>.\n" +
			"lxc remote list                                                           List all remotes.\n" +
			"lxc remote rename <old> <new>                                             Rename remote <old> to <new>.\n" +
			"lxc remote set-url <name> <url>                                           Update <name>'s url to <url>.\n" +
			"lxc remote set-default <name>                                             Set the default remote.\n" +
			"lxc remote get-default                                                    Print the default remote.\n")
}

func (c *remoteCmd) flags() {
	gnuflag.BoolVar(&c.acceptCert, "accept-certificate", false, gettext.Gettext("Accept certificate"))
	gnuflag.StringVar(&c.password, "password", "", gettext.Gettext("Remote admin password"))
}

func addServer(config *lxd.Config, server string, addr string, acceptCert bool, password string) error {
	var r_scheme string
	var r_host string
	var r_port string

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
		_, err := os.Stat(addr)
		if err != nil && os.IsNotExist(err) {
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
		r_port = "8443"
	}

	if r_scheme == "unix" {
		if addr[0:5] == "unix:" {
			if addr[0:7] == "unix://" {
				r_host = addr[8:]
			} else {
				r_host = addr[6:]
			}
		}
		r_port = ""
	}

	if r_port != "" {
		addr = r_scheme + "://" + r_host + ":" + r_port
	} else {
		addr = r_scheme + "://" + r_host
	}

	if config.Remotes == nil {
		config.Remotes = make(map[string]lxd.RemoteConfig)
	}

	config.Remotes[server] = lxd.RemoteConfig{Addr: addr}

	remote := config.ParseRemote(server)
	c, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	if len(addr) > 5 && addr[0:5] == "unix:" {
		// NewClient succeeded so there was a lxd there (we fingered
		// it) so just accept it
		return nil
	}

	err = c.UserAuthServerCert(host, acceptCert)
	if err != nil {
		return err
	}

	if c.AmTrusted() {
		// server already has our cert, so we're done
		return nil
	}

	if password == "" {
		fmt.Printf(gettext.Gettext("Admin password for %s: "), server)
		pwd, err := terminal.ReadPassword(0)
		if err != nil {
			/* We got an error, maybe this isn't a terminal, let's try to
			 * read it as a file */
			pwd, err = shared.ReadStdin()
			if err != nil {
				return err
			}
		}
		fmt.Printf("\n")
		password = string(pwd)
	}

	err = c.AddMyCertToServer(password)
	if err != nil {
		return err
	}

	if !c.AmTrusted() {
		return fmt.Errorf(gettext.Gettext("Server doesn't trust us after adding our cert"))
	}

	fmt.Println(gettext.Gettext("Client certificate stored at server: "), server)
	return nil
}

func removeCertificate(remote string) {
	certf := lxd.ServerCertPath(remote)
	shared.Debugf("Trying to remove %s\n", certf)

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
			return fmt.Errorf(gettext.Gettext("remote %s exists as <%s>"), args[1], rc.Addr)
		}

		err := addServer(config, args[1], args[2], c.acceptCert, c.password)
		if err != nil {
			delete(config.Remotes, args[1])
			return err
		}

	case "remove":
		if len(args) != 2 {
			return errArgs
		}

		if _, ok := config.Remotes[args[1]]; !ok {
			return fmt.Errorf(gettext.Gettext("remote %s doesn't exist"), args[1])
		}

		if config.DefaultRemote == args[1] {
			config.DefaultRemote = ""
		}

		delete(config.Remotes, args[1])

		removeCertificate(args[1])

	case "list":
		for name, rc := range config.Remotes {
			fmt.Println(fmt.Sprintf("%s <%s>", name, rc.Addr))
		}
		/* Here, we don't need to save since we didn't actually modify
		 * anything, so just return. */
		return nil

	case "rename":
		if len(args) != 3 {
			return errArgs
		}

		rc, ok := config.Remotes[args[1]]
		if !ok {
			return fmt.Errorf(gettext.Gettext("remote %s doesn't exist"), args[1])
		}

		if _, ok := config.Remotes[args[2]]; ok {
			return fmt.Errorf(gettext.Gettext("remote %s already exists"), args[2])
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
			return fmt.Errorf(gettext.Gettext("remote %s doesn't exist"), args[1])
		}
		config.Remotes[args[1]] = lxd.RemoteConfig{Addr: args[2]}

	case "set-default":
		if len(args) != 2 {
			return errArgs
		}

		_, ok := config.Remotes[args[1]]
		if !ok {
			return fmt.Errorf(gettext.Gettext("remote %s doesn't exist"), args[1])
		}
		config.DefaultRemote = args[1]

	case "get-default":
		if len(args) != 1 {
			return errArgs
		}
		fmt.Println(config.DefaultRemote)
		return nil
	default:
		return fmt.Errorf(gettext.Gettext("Unknown remote subcommand %s"), args[0])
	}

	return lxd.SaveConfig(config)
}
