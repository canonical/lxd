package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"
)

type imageCmd struct{}

func (c *imageCmd) showByDefault() bool {
	return true
}

var imageEditHelp string = gettext.Gettext(
	"### This is a yaml representation of the image properties.\n" +
		"### Any line starting with a '# will be ignored.\n" +
		"###\n" +
		"### Each property is represented by thee lines:\n" +
		"###\n" +
		"###  The first is 'imagetype: ' followed by an integer.  0 means\n" +
		"###  a short string, 1 means a long text value containing newlines.\n" +
		"###\n" +
		"###  This is followed by the key and value\n" +
		"###\n" +
		"###  An example would be:\n" +
		"### - imagetype: 0\n" +
		"###   key: os\n" +
		"###   value: Ubuntu\n")

func (c *imageCmd) usage() string {
	return gettext.Gettext(
		"lxc image import <tarball> [target] [--created-at=ISO-8601] [--expires-at=ISO-8601] [--fingerprint=HASH] [prop=value]\n" +
			"\n" +
			"lxc image delete [resource:]<image>\n" +
			"lxc image edit [resource:]\n" +
			"lxc image export [resource:]<image>\n" +
			"lxc image list [resource:] [filter]\n" +
			"\n" +
			"Lists the images at resource, or local images.\n" +
			"Filters are not yet supported.\n" +
			"\n" +
			"lxc image alias create <alias> <target>\n" +
			"lxc image alias delete <alias>\n" +
			"lxc image alias list [resource:]\n" +
			"create, delete, list image aliases\n")
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

		var properties []string
		if len(args) > 2 {
			split := strings.Split(args[2], "=")
			if len(split) == 1 {
				remote, _ = config.ParseRemoteAndContainer(args[2])
				if len(args) > 3 {
					properties = args[3:]
				} else {
					properties = []string{}
				}
			} else {
				properties = args[2:]
			}
		} else {
			remote = ""
			properties = []string{}
		}

		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		_, err = d.PostImage(imagefile, properties)
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

		imagenames, err := d.ListImages()
		if err != nil {
			return err
		}

		images := []shared.ImageInfo{}
		prefix := "/1.0/images/"
		offset := len(prefix)
		for _, image := range imagenames {
			var plainname string
			if len(image) < offset+1 {
				fmt.Printf(gettext.Gettext("(Bad image entry: %s\n"), image)
				continue
			}
			plainname = image[offset:]
			info, err := d.GetImageInfo(plainname)
			if err != nil {
				// XXX should we warn?  bail?
				continue
			}
			images = append(images, *info)
		}

		return showImages(images)

	case "edit":
		if len(args) < 2 {
			return errArgs
		}
		remote, image := config.ParseRemoteAndContainer(args[1])
		if image == "" {
			return errArgs
		}
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}
		info, err := d.GetImageInfo(image)
		if err != nil {
			return err
		}

		properties := info.Properties
		editor := os.Getenv("VISUAL")
		if editor == "" {
			editor = os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
		}
		data, err := yaml.Marshal(&properties)
		f, err := ioutil.TempFile("", "lxc_image_")
		if err != nil {
			return err
		}
		fname := f.Name()
		if err = f.Chmod(0700); err != nil {
			f.Close()
			os.Remove(fname)
			return err
		}
		f.Write([]byte(imageEditHelp))
		f.Write(data)
		f.Close()
		defer os.Remove(fname)
		cmd := exec.Command(editor, fname)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		if err != nil {
			return err
		}
		contents, err := ioutil.ReadFile(fname)
		if err != nil {
			return err
		}
		newdata := shared.ImageProperties{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}
		err = d.PutImageProperties(image, newdata)
		return err

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

func shortest_alias(list shared.ImageAliases) string {
	shortest := ""
	for _, l := range list {
		if shortest == "" {
			shortest = l.Name
			continue
		}
		if len(l.Name) != 0 && len(l.Name) < len(shortest) {
			shortest = l.Name
		}
	}

	return shortest
}

func find_description(props shared.ImageProperties) string {
	for _, p := range props {
		if p.Key == "description" {
			return p.Value
		}
	}
	return ""
}

func showImages(images []shared.ImageInfo) error {
	data := [][]string{}
	for _, image := range images {
		shortest := shortest_alias(image.Aliases)
		fp := image.Fingerprint[0:8]
		public := "no"
		description := find_description(image.Properties)
		if image.Public == 1 {
			public = "yes"
		}
		data = append(data, []string{shortest, fp, public, description})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ALIAS", "HASH", "PUBLIC", "DESCRIPTION"})

	for _, v := range data {
		table.Append(v)
	}
	table.Render()

	return nil
}
