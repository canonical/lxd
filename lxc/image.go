package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
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
		"lxc image import <tarball> [target] [--public] [--created-at=ISO-8601] [--expires-at=ISO-8601] [--fingerprint=FINGERPRINT] [prop=value]\n" +
			"\n" +
			"lxc image copy [resource:]<image> <resource>: [--alias=ALIAS].. [--copy-alias]\n" +
			"lxc image delete [resource:]<image>\n" +
			"lxc image edit [resource:]\n" +
			"lxc image export [resource:]<image>\n" +
			"lxc image info [resource:]<image>\n" +
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

type aliasList []string

func (f *aliasList) String() string {
	return fmt.Sprint(*f)
}

func (f *aliasList) Set(value string) error {
	if f == nil {
		*f = make(aliasList, 1)
	} else {
		*f = append(*f, value)
	}
	return nil
}

var addAliases aliasList
var publicImage bool = false
var copyAliases bool = false

func (c *imageCmd) flags() {
	gnuflag.BoolVar(&publicImage, "public", false, gettext.Gettext("Make image public"))
	gnuflag.BoolVar(&copyAliases, "copy-aliases", false, gettext.Gettext("Copy aliases from source"))
	gnuflag.Var(&addAliases, "alias", "New alias to define at target")
}

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

		for _, url := range resp {
			/* /1.0/images/aliases/ALIAS_NAME */
			alias := fromUrl(url, "/1.0/images/aliases/")
			if alias == "" {
				fmt.Printf(gettext.Gettext("(Bad alias entry: %s\n"), url)
			} else {
				fmt.Println(alias)
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

	case "copy":
		/* copy [<remote>:]<image> [<rmeote>:]<image> */
		if len(args) != 3 {
			return errArgs
		}
		remote, inName := config.ParseRemoteAndContainer(args[1])
		if inName == "" {
			return errArgs
		}
		destRemote, outName := config.ParseRemoteAndContainer(args[2])
		if outName != "" {
			return errArgs
		}
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}
		dest, err := lxd.NewClient(config, destRemote)
		if err != nil {
			return err
		}
		image := dereferenceAlias(d, inName)
		return d.CopyImage(image, dest, copyAliases, addAliases, publicImage)

	case "delete":
		/* delete [<remote>:]<image> */
		if len(args) < 2 {
			return errArgs
		}
		remote, inName := config.ParseRemoteAndContainer(args[1])
		if inName == "" {
			return errArgs
		}
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}
		image := dereferenceAlias(d, inName)
		err = d.DeleteImage(image)
		return err

	case "info":
		if len(args) < 2 {
			return errArgs
		}
		remote, inName := config.ParseRemoteAndContainer(args[1])
		if inName == "" {
			return errArgs
		}
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		image := dereferenceAlias(d, inName)
		info, err := d.GetImageInfo(image)
		if err != nil {
			return err
		}
		fmt.Printf(gettext.Gettext("Fingerprint: %s\n"), info.Fingerprint)
		public := "no"
		if info.Public == 1 {
			public = "yes"
		}
		fmt.Printf(gettext.Gettext("Size: %.2vMB\n"), float64(info.Size)/1024.0/1024.0)
		fmt.Printf(gettext.Gettext("Architecture: %s\n"), arch_to_string(info.Architecture))
		fmt.Printf(gettext.Gettext("Public: %s\n"), public)
		fmt.Printf(gettext.Gettext("Timestamps:\n"))
		const layout = "2006/01/02 15:04 UTC"
		if info.CreationDate != 0 {
			fmt.Printf("    Created: %s\n", time.Unix(info.CreationDate, 0).UTC().Format(layout))
		}
		fmt.Printf("    Uploaded: %s\n", time.Unix(info.UploadDate, 0).UTC().Format(layout))
		if info.ExpiryDate != 0 {
			fmt.Printf("    Expires: %s\n", time.Unix(info.ExpiryDate, 0).UTC().Format(layout))
		} else {
			fmt.Printf("    Expires: never\n")
		}
		fmt.Printf(gettext.Gettext("Properties:\n"))
		for key, value := range info.Properties {
			fmt.Printf("    %s: %s\n", key, value)
		}
		fmt.Printf(gettext.Gettext("Aliases:\n"))
		for _, alias := range info.Aliases {
			fmt.Printf("    - %s\n", alias.Name)
		}
		return nil

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

		fingerprint, err := d.PostImage(imagefile, properties, publicImage, addAliases)
		if err != nil {
			return err
		}

		fmt.Printf(gettext.Gettext("Image imported with fingerprint: %s\n"), fingerprint)

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

		images, err := d.ListImages()
		if err != nil {
			return err
		}

		return showImages(images)

	case "edit":
		if len(args) < 2 {
			return errArgs
		}
		remote, inName := config.ParseRemoteAndContainer(args[1])
		if inName == "" {
			return errArgs
		}
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		image := dereferenceAlias(d, inName)
		if image == "" {
			image = inName
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
		f, err := ioutil.TempFile("", "lxd_lxc_image_")
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
		if len(args) < 2 {
			return errArgs
		}

		remote, inName := config.ParseRemoteAndContainer(args[1])
		if inName == "" {
			return errArgs
		}
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		image := dereferenceAlias(d, inName)

		target := "."
		if len(args) > 2 {
			target = args[2]
		}
		_, outfile, err := d.ExportImage(image, target)
		if err != nil {
			return err
		}

		if target != "-" {
			fmt.Printf("Output is in %s\n", outfile)
		}
		return nil
	default:
		return fmt.Errorf(gettext.Gettext("Unknown image command %s"), args[0])
	}
}

func fromUrl(url string, prefix string) string {
	offset := len(prefix)
	if len(url) < offset+1 {
		return ""
	}
	return url[offset:]
}

func dereferenceAlias(d *lxd.Client, inName string) string {
	result := d.GetAlias(inName)
	if result == "" {
		return inName
	}
	return result
}

func shortestAlias(list shared.ImageAliases) string {
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

func findDescription(props map[string]string) string {
	for k, v := range props {
		if k == "description" {
			return v
		}
	}
	return ""
}

func arch_to_string(arch int) string {
	switch arch {
	case 1:
		return "i686"
	case 2:
		return "x86_64"
	case 3:
		return "armv7l"
	case 4:
		return "aarch64"
	case 5:
		return "ppc"
	case 6:
		return "ppc64"
	case 7:
		return "ppc64le"
	default:
		return "x86_64"
	}
}

func showImages(images []shared.ImageInfo) error {
	data := [][]string{}
	for _, image := range images {
		shortest := shortestAlias(image.Aliases)
		if len(image.Aliases) > 1 {
			shortest = fmt.Sprintf("%s (%d more)", shortest, len(image.Aliases)-1)
		}
		fp := image.Fingerprint[0:12]
		public := "no"
		description := findDescription(image.Properties)
		if image.Public == 1 {
			public = "yes"
		}
		const layout = "Jan 2, 2006 at 3:04pm (MST)"
		uploaded := time.Unix(image.UploadDate, 0).Format(layout)
		arch := arch_to_string(image.Architecture)
		data = append(data, []string{shortest, fp, public, description, arch, uploaded})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ALIAS", "FINGERPRINT", "PUBLIC", "DESCRIPTION", "ARCH", "UPLOAD DATE"})

	for _, v := range data {
		table.Append(v)
	}
	table.Render()

	return nil
}
