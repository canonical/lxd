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
			"\n" +
			"Lists the images at resource, or local images.\n" +
			"Filters are not yet supported.\n")
}

func (c *imageCmd) flags() {}

func (c *imageCmd) run(config *lxd.Config, args []string) error {
	var remote string

	if len(args) < 1 {
		return errArgs
	}

	switch args[0] {
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

		_, err = d.PutImage(imagefile)
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
			if len(image) < 13 {
				fmt.Printf(gettext.Gettext("(Bad image entry: %s\n"), image)
			} else {
				fmt.Println(image[12:])
			}
		}
		return nil
	default:
		return fmt.Errorf(gettext.Gettext("Unknown image command %s"), args[0])
	}
}
