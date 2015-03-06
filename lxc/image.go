package main

import (
	"fmt"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
)

type imageCmd struct{}

func (c *imageCmd) showByDefault() bool {
	return true
}

func (c *imageCmd) usage() string {
	return gettext.Gettext(
		"lxc image import <tarball> [target] [--created-at=ISO-8601] [--expires-at=ISO-8601] [--fingerprint=HASH] [prop=value]\n" +
			"\n" +
			"lxc image list [resource:] [filter]\n" +
			"lxc image delete [resource:]<image>\n" +
			"lxc image export [resource:]<image>\n" +
			"\n" +
			"Lists the images at resource, or local images.\n" +
			"Filters are not yet supported.\n" +
			"\n" +
			"lxc image alias list [resource:]\n" +
			"lxc image alias create <alias> <target>\n" +
			"lxc image alias delete <alias>\n" +
			"list, create, delete image aliases\n")
}

func (c *imageCmd) flags() {}

func doImageAlias(config *lxd.Config, args []string) error {
	var remote string
	switch args[1] {
	case "list":
		/* alias list [<remote>:] */
		if len(args) > 2 {
			remote, _ = config.ParseRemoteAndContainer(args[2])
		} else {
			remote = ""
		}
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		resp, err := d.ListAliases()
		if err != nil {
			return err
		}

		for _, alias := range resp {
			/* /1.0/images/aliases/ALIAS_NAME */
			prefix := "/1.0/images/aliases/"
			offset := len(prefix)
			if len(alias) < offset+1 {
				fmt.Printf(gettext.Gettext("(Bad alias entry: %s\n"), alias)
			} else {
				fmt.Println(alias[offset:])
			}
		}
		return nil
	case "create":
		/* alias create [<remote>:]<alias> <target> */
		if len(args) < 4 {
			return errArgs
		}
		remote, alias := config.ParseRemoteAndContainer(args[2])
		target := args[3]
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}
		/* TODO - what about description? */
		err = d.PostAlias(alias, alias, target)
		return err
	case "delete":
		/* alias delete [<remote>:]<alias> */
		if len(args) < 3 {
			return errArgs
		}
		remote, alias := config.ParseRemoteAndContainer(args[2])
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}
		err = d.DeleteAlias(alias)
		return err
	}
	return errArgs
}

func (c *imageCmd) run(config *lxd.Config, args []string) error {
	var remote string

	if len(args) < 1 {
		return errArgs
	}

	switch args[0] {
	case "alias":
		if len(args) < 2 {
			return errArgs
		}
		return doImageAlias(config, args)
	case "delete":
		/* delete [<remote>:]<image> */
		if len(args) < 2 {
			return errArgs
		}
		remote, image := config.ParseRemoteAndContainer(args[1])
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}
		err = d.DeleteImage(image)
		return err
	case "import":
		if len(args) < 2 {
			return errArgs
		}
		imagefile := args[1]

		/* todo - accept properties between the image name and the (optional) resource, i.e.
		 * "tarball --created-at=<date> os=fedora arch=amd64 dakara:
		 * or
		 * "tarball --created-at=<date> os=fedora arch=amd64
		 * For now I'm not accepting those, so I can just assume that len(args)>2 means
		 * args[2] is the resource, else we use local */
		if len(args) > 2 {
			remote, _ = config.ParseRemoteAndContainer(args[2])
		} else {
			remote = ""
		}

		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		_, err = d.PostImage(imagefile)
		if err != nil {
			return err
		}

		return nil

	case "list":
		if len(args) > 1 {
			remote, _ = config.ParseRemoteAndContainer(args[1])
		} else {
			remote = ""
		}
		// XXX TODO if name is not "" we'll want to filter for just that image name

		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		resp, err := d.ListImages()
		if err != nil {
			return err
		}

		for _, image := range resp {
			/* /1.0/images/IMAGE_NAME */
			prefix := "/1.0/images/"
			offset := len(prefix)
			if len(image) < offset+1 {
				fmt.Printf(gettext.Gettext("(Bad image entry: %s\n"), image)
			} else {
				fmt.Println(image[offset:])
			}
		}
		return nil
	case "export":
		if len(args) < 3 {
			return errArgs
		}

		remote, image := config.ParseRemoteAndContainer(args[1])
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		_, err = d.ExportImage(image, args[2])
		if err != nil {
			return err
		}

		return nil
	default:
		return fmt.Errorf(gettext.Gettext("Unknown image command %s"), args[0])
	}
}
