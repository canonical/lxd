package main

import (
	"fmt"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

type configCmd struct {
	httpAddr string
}

func (c *configCmd) showByDefault() bool {
	return true
}

func (c *configCmd) usage() string {
	return gettext.Gettext(
		"Manage configuration.\n" +
			"\n" +
			"lxc config set [remote] password <newpwd>        Set admin password\n" +
			"lxc trust list [remote]                          List all trusted certs.\n" +
			"lxc trust add [remote] [certfile.crt]            Add certfile.crt to trusted hosts.\n" +
			"lxc trust remove [remote] [hostname|fingerprint] Remove the cert from trusted hosts.\n")
}

func (c *configCmd) flags() {}

func (c *configCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	switch args[0] {

	case "set":
		if len(args) < 2 {
			return errArgs
		}

		action := args[1]
		if action == "password" {
			if len(args) != 3 {
				return errArgs
			}

			password := args[2]
			c, err := lxd.NewClient(config, "")
			if err != nil {
				return err
			}

			_, err = c.SetRemotePwd(password)
			return err
		}

		return fmt.Errorf(gettext.Gettext("Only 'password' can be set currently"))
	case "trust":
		if len(args) < 2 {
			return errArgs
		}

		switch args[1] {
		case "list":
			var remote string
			if len(args) == 3 {
				remote = config.ParseRemote(args[2])
			} else {
				remote = config.DefaultRemote
			}

			d, err := lxd.NewClient(config, remote)
			if err != nil {
				return err
			}

			trust, err := d.CertificateList()
			if err != nil {
				return err
			}

			for host, fingerprint := range trust {
				fmt.Println(fmt.Sprintf("%s: %s", host, fingerprint))
			}

			return nil
		case "add":
			var remote string
			if len(args) < 3 {
				return fmt.Errorf(gettext.Gettext("No cert provided to add"))
			} else if len(args) == 4 {
				remote = config.ParseRemote(args[2])
			} else {
				remote = config.DefaultRemote
			}

			d, err := lxd.NewClient(config, remote)
			if err != nil {
				return err
			}

			fname := args[len(args)-1]
			cert, err := shared.ReadCert(fname)
			if err != nil {
				return err
			}

			name, _ := shared.SplitExt(fname)
			return d.CertificateAdd(cert, name)
		case "remove":
			var remote string
			if len(args) < 3 {
				return fmt.Errorf(gettext.Gettext("No fingerprint specified."))
			} else if len(args) == 4 {
				remote = config.ParseRemote(args[2])
			} else {
				remote = config.DefaultRemote
			}

			d, err := lxd.NewClient(config, remote)
			if err != nil {
				return err
			}

			toRemove := args[len(args)-1]
			trust, err := d.CertificateList()
			if err != nil {
				return err
			}

			/* Try to remove by hostname first. */
			for host, fingerprint := range trust {
				if host == toRemove {
					return d.CertificateRemove(fingerprint)
				}
			}

			return d.CertificateRemove(args[len(args)-1])
		default:
			return fmt.Errorf(gettext.Gettext("Unkonwn config trust command %s"), args[1])
		}
	default:
		return fmt.Errorf(gettext.Gettext("Unknown config command %s"), args[0])
	}
}
