package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/chai2010/gettext-go/gettext"
	"github.com/olekukonko/tablewriter"
	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
	"github.com/lxc/lxd/shared"
)

type imageCmd struct{}

func (c *imageCmd) showByDefault() bool {
	return true
}

var imageEditHelp string = gettext.Gettext(
	"### This is a yaml representation of the image properties.\n" +
		"### Any line starting with a '# will be ignored.\n" +
		"###\n" +
		"### Each property is represented by a single line:\n" +
		"### An example would be:\n" +
		"###  description: My custom image\n")

func (c *imageCmd) usage() string {
	return gettext.Gettext(
		"Manipulate container images\n" +
			"\n" +
			"lxc image import <tarball> [rootfs tarball] [target] [--public] [--created-at=ISO-8601] [--expires-at=ISO-8601] [--fingerprint=FINGERPRINT] [prop=value]\n" +
			"\n" +
			"lxc image copy [remote:]<image> <remote>: [--alias=ALIAS].. [--copy-alias]\n" +
			"lxc image delete [remote:]<image>\n" +
			"lxc image edit [remote:]<image>\n" +
			"lxc image export [remote:]<image>\n" +
			"lxc image info [remote:]<image>\n" +
			"lxc image list [remote:] [filter]\n" +
			"lxc image show [remote:]<image>\n" +
			"\n" +
			"Lists the images at specified remote, or local images.\n" +
			"Filters are not yet supported.\n" +
			"\n" +
			"lxc image alias create <alias> <target>\n" +
			"lxc image alias delete <alias>\n" +
			"lxc remote add images images.linuxcontainers.org\n" +
			"lxc image alias list images:\n" +
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

		showAliases(resp)

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
		arch, _ := shared.ArchitectureName(info.Architecture)
		fmt.Printf(gettext.Gettext("Architecture: %s\n"), arch)
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

		var imageFile string
		var rootfsFile string
		var properties []string
		var remote string

		for _, arg := range args[1:] {
			split := strings.Split(arg, "=")
			if len(split) == 1 || shared.PathExists(arg) {
				if strings.HasSuffix(arg, ":") {
					remote = config.ParseRemote(arg)
				} else {
					if imageFile == "" {
						imageFile = args[1]
					} else {
						rootfsFile = arg
					}
				}
			} else {
				properties = append(properties, arg)
			}
		}

		if imageFile == "" {
			return errArgs
		}

		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		fingerprint, err := d.PostImage(imageFile, rootfsFile, properties, publicImage, addAliases)
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

		if !terminal.IsTerminal(syscall.Stdin) {
			contents, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				return err
			}

			newdata := shared.ImageProperties{}
			err = yaml.Unmarshal(contents, &newdata)
			if err != nil {
				return err
			}
			return d.PutImageProperties(image, newdata)
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
		if err = f.Chmod(0600); err != nil {
			f.Close()
			os.Remove(fname)
			return err
		}
		f.Write([]byte(imageEditHelp))
		f.Write(data)
		f.Close()
		defer os.Remove(fname)

		for {
			cmdParts := strings.Fields(editor)
			cmd := exec.Command(cmdParts[0], append(cmdParts[1:], fname)...)
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
				fmt.Fprintf(os.Stderr, gettext.Gettext("YAML parse error %v\n"), err)
				fmt.Printf("Press enter to play again ")
				_, err := os.Stdin.Read(make([]byte, 1))
				if err != nil {
					return err
				}

				continue
			}
			err = d.PutImageProperties(image, newdata)
			break
		}

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

	case "show":
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

		properties := info.Properties

		data, err := yaml.Marshal(&properties)
		fmt.Printf("%s", data)
		return err

	default:
		return fmt.Errorf(gettext.Gettext("Unknown image command %s"), args[0])
	}
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
		arch, _ := shared.ArchitectureName(image.Architecture)
		data = append(data, []string{shortest, fp, public, description, arch, uploaded})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetColWidth(50)
	table.SetHeader([]string{"ALIAS", "FINGERPRINT", "PUBLIC", "DESCRIPTION", "ARCH", "UPLOAD DATE"})

	for _, v := range data {
		table.Append(v)
	}
	table.Render()

	return nil
}

func showAliases(aliases []shared.ImageAlias) error {
	data := [][]string{}
	for _, alias := range aliases {
		data = append(data, []string{alias.Description, alias.Name[0:12]})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ALIAS", "FINGERPRINT"})

	for _, v := range data {
		table.Append(v)
	}
	table.Render()

	return nil
}
