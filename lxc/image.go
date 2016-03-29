package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/codegangsta/cli"

	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

var commandImage = cli.Command{
	Name:  "image",
	Usage: i18n.G("Manipulate container images."),
	Description: i18n.G(`Manipulate container images.

   In LXD containers are created from images. Those images were themselves
   either generated from an existing container or downloaded from an image
   server.

   When using remote images, LXD will automatically cache images for you
   and remove them upon expiration.

   The image unique identifier is the hash (sha-256) of its representation
   as a compressed tarball (or for split images, the concatenation of the
   metadata and rootfs tarballs).

   Images can be referenced by their full hash, shortest unique partial
   hash or alias name (if one is set).


   lxc image import <tarball> [rootfs tarball|URL] [remote:] [--public] [--alias=ALIAS]... [prop=value]...
   Import an image tarball (or tarballs) into the LXD image store.

   lxc image copy [remote:]<image> <remote>: [--alias=ALIAS].. [--copy-aliases] [--public] [--auto-update]
   Copy an image from one LXD daemon to another over the network.

   lxc image delete [remote:]<image>
   Delete an image from the LXD image store.

   lxc image export [remote:]<image>
   Export an image from the LXD image store into a distributable tarball.

   lxc image info [remote:]<image>
   Print everything LXD knows about a given image.

   lxc image list [remote:] [filter]
   List images in the LXD image store. Filters may be of the
   <key>=<value> form for property based filtering, or part of the image
   hash or part of the image alias name.

   lxc image show [remote:]<image>
   Yaml output of the user modifiable properties of an image.

   lxc image edit [remote:]<image>
   Edit image, either by launching external editor or reading STDIN.
   Example: lxc image edit <image> # launch editor
	   			 cat image.yml | lxc image edit <image> # read from image.yml

   lxc image alias create [remote:]<alias> <fingerprint>
   Create a new alias for an existing image.

   lxc image alias delete [remote:]<alias>
   Delete an alias.

   lxc image alias list [remote:]
   List the aliases.`),

	Flags: commandGlobalFlags,
	Subcommands: []cli.Command{

		cli.Command{
			Name:      "import",
			ArgsUsage: i18n.G("<tarball> [rootfs tarball|URL] [remote:] [--public] [--alias=ALIAS]... [prop=value]..."),
			Usage:     i18n.G("Import an image tarball (or tarballs) into the LXD image store."),

			Flags: commandGlobalFlagsWrapper(
				cli.BoolFlag{
					Name:  "public",
					Usage: i18n.G("Make image public."),
				},
				cli.StringSliceFlag{
					Name:  "alias",
					Usage: i18n.G("An alias for this image."),
				},
			),
			Action: commandWrapper(commandActionImageImport),
		},

		cli.Command{
			Name:      "copy",
			ArgsUsage: i18n.G("[remote:]<image> <remote>: [--alias=ALIAS].. [--copy-aliases] [--public] [--auto-update]"),
			Usage:     i18n.G("Copy an image to another destination."),

			Flags: commandGlobalFlagsWrapper(
				cli.StringSliceFlag{
					Name:  "alias",
					Usage: i18n.G("An alias for this image."),
				},
				cli.BoolFlag{
					Name:  "copy-aliases",
					Usage: i18n.G("Also copy aliases."),
				},
				cli.BoolFlag{
					Name:  "public",
					Usage: i18n.G("Make image public."),
				},
				cli.BoolFlag{
					Name:  "auto-update",
					Usage: i18n.G("Keep the image up to date after initial copy."),
				},
			),
			Action: commandWrapper(commandActionImageCopy),
		},

		cli.Command{
			Name:      "delete",
			ArgsUsage: i18n.G("[remote:]<image>"),
			Usage:     i18n.G("Delete an image."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionImageDelete),
		},

		cli.Command{
			Name:      "export",
			ArgsUsage: i18n.G("[remote:]<image>"),
			Usage:     i18n.G("Export an image."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionImageExport),
		},

		cli.Command{
			Name:      "info",
			ArgsUsage: i18n.G("[remote:]<image>"),
			Usage:     i18n.G("Get informations form an image."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionImageInfo),
		},

		cli.Command{
			Name:      "list",
			ArgsUsage: i18n.G("[resource] [filters] [-c columns] [--fast]"),
			Usage:     i18n.G("List images."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionImageList),
		},

		cli.Command{
			Name:      "show",
			ArgsUsage: i18n.G("[remote:]<image>"),
			Usage:     i18n.G("Show an image."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionImageShow),
		},

		cli.Command{
			Name:      "edit",
			ArgsUsage: i18n.G("[remote:]<image>"),
			Usage:     i18n.G("Edit an image."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionImageEdit),
		},

		cli.Command{
			Name:  "alias",
			Usage: i18n.G("Manipulate aliases."),

			Subcommands: []cli.Command{

				cli.Command{
					Name:      "create",
					ArgsUsage: i18n.G("<alias> <target>"),
					Usage:     i18n.G("Create an alias."),
					Flags:     commandGlobalFlags,
					Action:    commandWrapper(commandActionImageAliasCreate),
				},

				cli.Command{
					Name:      "delete",
					ArgsUsage: i18n.G("<alias>"),
					Usage:     i18n.G("Delete an alias."),
					Flags:     commandGlobalFlags,
					Action:    commandWrapper(commandActionImageAliasDelete),
				},

				cli.Command{
					Name:      "list",
					ArgsUsage: i18n.G("[remote:]"),
					Usage:     i18n.G("List aliases."),
					Flags:     commandGlobalFlags,
					Action:    commandWrapper(commandActionImageAliasList),
				},
			},
		},
	},
}

func commandActionImageImport(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	var public = context.Bool("public")
	var alias = context.StringSlice("alias")

	if len(args) < 1 {
		return errArgs
	}

	var fingerprint string
	var imageFile string
	var rootfsFile string
	var properties []string
	var remote string

	for _, arg := range args {
		split := strings.Split(arg, "=")
		if len(split) == 1 || shared.PathExists(arg) {
			if strings.HasSuffix(arg, ":") {
				remote = config.ParseRemote(arg)
			} else {
				if imageFile == "" {
					imageFile = args[0]
				} else {
					rootfsFile = arg
				}
			}
		} else {
			properties = append(properties, arg)
		}
	}

	if remote == "" {
		remote = config.DefaultRemote
	}

	if imageFile == "" {
		return errArgs
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	handler := func(percent int) {
		fmt.Printf(i18n.G("Transferring image: %d%%")+"\r", percent)
		if percent == 100 {
			fmt.Printf("\n")
		}
	}

	if strings.HasPrefix(imageFile, "https://") {
		fingerprint, err = d.PostImageURL(imageFile, public, alias)
	} else if strings.HasPrefix(imageFile, "http://") {
		return fmt.Errorf(i18n.G("Only https:// is supported for remote image import."))
	} else {
		fingerprint, err = d.PostImage(imageFile, rootfsFile, properties, public, alias, handler)
	}

	if err != nil {
		return err
	}
	fmt.Printf(i18n.G("Image imported with fingerprint: %s")+"\n", fingerprint)

	return nil
}

func commandActionImageCopy(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	var public = context.Bool("public")
	var alias = context.StringSlice("alias")
	var copyAliases = context.Bool("copy-aliases")

	/* [<remote>:]<image> [<remote>:]<image> */
	if len(args) != 2 {
		return errArgs
	}
	remote, inName := config.ParseRemoteAndContainer(args[0])
	if inName == "" {
		return errArgs
	}
	destRemote, outName := config.ParseRemoteAndContainer(args[1])
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

	progressHandler := func(progress string) {
		fmt.Printf(i18n.G("Copying the image: %s")+"\r", progress)
	}

	err = d.CopyImage(inName, dest, copyAliases, alias, public, context.Bool("auto-update"), progressHandler)
	if err == nil {
		fmt.Println(i18n.G("Image copied successfully!"))
	}
	return err
}

func commandActionImageDelete(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	/* [<remote>:]<image> */
	if len(args) < 1 {
		return errArgs
	}
	remote, inName := config.ParseRemoteAndContainer(args[0])
	if inName == "" {
		inName = "default"
	}
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}
	var cmd = &imageCmd{}
	image := cmd.dereferenceAlias(d, inName)
	err = d.DeleteImage(image)
	return err
}

func commandActionImageInfo(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 1 {
		return errArgs
	}
	remote, inName := config.ParseRemoteAndContainer(args[0])
	if inName == "" {
		inName = "default"
	}
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	var cmd = &imageCmd{}
	image := cmd.dereferenceAlias(d, inName)

	info, err := d.GetImageInfo(image)
	if err != nil {
		return err
	}

	public := i18n.G("no")

	if info.Public {
		public = i18n.G("yes")
	}

	autoUpdate := i18n.G("disabled")
	if info.AutoUpdate {
		autoUpdate = i18n.G("enabled")
	}

	fmt.Printf(i18n.G("Fingerprint: %s")+"\n", info.Fingerprint)
	fmt.Printf(i18n.G("Size: %.2fMB")+"\n", float64(info.Size)/1024.0/1024.0)
	fmt.Printf(i18n.G("Architecture: %s")+"\n", info.Architecture)
	fmt.Printf(i18n.G("Public: %s")+"\n", public)
	fmt.Printf(i18n.G("Timestamps:") + "\n")
	const layout = "2006/01/02 15:04 UTC"
	if info.CreationDate.UTC().Unix() != 0 {
		fmt.Printf("    "+i18n.G("Created: %s")+"\n", info.CreationDate.UTC().Format(layout))
	}
	fmt.Printf("    "+i18n.G("Uploaded: %s")+"\n", info.UploadDate.UTC().Format(layout))
	if info.ExpiryDate.UTC().Unix() != 0 {
		fmt.Printf("    "+i18n.G("Expires: %s")+"\n", info.ExpiryDate.UTC().Format(layout))
	} else {
		fmt.Printf("    " + i18n.G("Expires: never") + "\n")
	}
	fmt.Println(i18n.G("Properties:"))
	for key, value := range info.Properties {
		fmt.Printf("    %s: %s\n", key, value)
	}
	fmt.Println(i18n.G("Aliases:"))
	for _, alias := range info.Aliases {
		fmt.Printf("    - %s\n", alias.Name)
	}
	fmt.Printf(i18n.G("Auto update: %s")+"\n", autoUpdate)
	if info.Source != nil {
		fmt.Println(i18n.G("Source:"))
		fmt.Printf("    Server: %s\n", info.Source.Server)
		fmt.Printf("    Protocol: %s\n", info.Source.Protocol)
		fmt.Printf("    Alias: %s\n", info.Source.Alias)
	}
	return nil
}

func commandActionImageExport(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 1 {
		return errArgs
	}

	remote, inName := config.ParseRemoteAndContainer(args[0])
	if inName == "" {
		inName = "default"
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	var cmd = &imageCmd{}
	image := cmd.dereferenceAlias(d, inName)

	target := "."
	if len(args) > 1 {
		target = args[1]
	}
	outfile, err := d.ExportImage(image, target)
	if err != nil {
		return err
	}

	if target != "-" {
		fmt.Printf(i18n.G("Output is in %s")+"\n", outfile)
	}
	return nil
}

func commandActionImageList(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	filters := []string{}

	var remote string

	if len(args) > 0 {
		result := strings.SplitN(args[0], ":", 2)
		if len(result) == 1 {
			filters = append(filters, args[0])
			remote, _ = config.ParseRemoteAndContainer("")
		} else {
			remote, _ = config.ParseRemoteAndContainer(args[0])
		}
	} else {
		remote, _ = config.ParseRemoteAndContainer("")
	}

	if len(args) > 1 {
		for _, filter := range args[1:] {
			filters = append(filters, filter)
		}
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	images, err := d.ListImages()
	if err != nil {
		return err
	}

	var cmd = &imageCmd{}
	return cmd.showImages(images, filters)
}

func commandActionImageShow(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 1 {
		return errArgs
	}

	remote, inName := config.ParseRemoteAndContainer(args[0])
	if inName == "" {
		inName = "default"
	}
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	var cmd = &imageCmd{}
	image := cmd.dereferenceAlias(d, inName)
	info, err := d.GetImageInfo(image)
	if err != nil {
		return err
	}

	properties := info.Brief()

	data, err := yaml.Marshal(&properties)
	fmt.Printf("%s", data)
	return err
}

func commandActionImageEdit(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 1 {
		return errArgs
	}

	remote, inName := config.ParseRemoteAndContainer(args[0])
	if inName == "" {
		inName = "default"
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	var cmd = &imageCmd{}
	image := cmd.dereferenceAlias(d, inName)
	if image == "" {
		image = inName
	}

	return cmd.doImageEdit(d, image)
}

func commandActionImageAliasCreate(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	/* [<remote>:]<alias> <target> */
	if len(args) < 2 {
		return errArgs
	}
	remote, alias := config.ParseRemoteAndContainer(args[0])
	target := args[1]
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}
	/* TODO - what about description? */
	err = d.PostAlias(alias, alias, target)
	return err
}

func commandActionImageAliasDelete(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	/* [<remote>:]<alias> */
	if len(args) < 1 {
		return errArgs
	}
	remote, alias := config.ParseRemoteAndContainer(args[0])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}
	err = d.DeleteAlias(alias)
	return err
}

func commandActionImageAliasList(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	var remote string

	/* alias list [<remote>:] */
	if len(args) > 1 {
		remote, _ = config.ParseRemoteAndContainer(args[0])
	} else {
		remote, _ = config.ParseRemoteAndContainer("")
	}
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	resp, err := d.ListAliases()
	if err != nil {
		return err
	}

	var cmd = &imageCmd{}
	cmd.showAliases(resp)

	return nil
}

type sortImage [][]string

func (a sortImage) Len() int {
	return len(a)
}

func (a sortImage) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a sortImage) Less(i, j int) bool {
	if a[i][0] == a[j][0] {
		if a[i][3] == "" {
			return false
		}

		if a[j][3] == "" {
			return true
		}

		return a[i][3] < a[j][3]
	}

	if a[i][0] == "" {
		return false
	}

	if a[j][0] == "" {
		return true
	}

	return a[i][0] < a[j][0]
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

type imageCmd struct {
	addAliases  aliasList
	publicImage bool
	copyAliases bool
}

func (c *imageCmd) imageEditHelp() string {
	return i18n.G(
		`### This is a yaml representation of the image properties.
### Any line starting with a '# will be ignored.
###
### Each property is represented by a single line:
### An example would be:
###  description: My custom image`)
}

func (c *imageCmd) dereferenceAlias(d *lxd.Client, inName string) string {
	result := d.GetAlias(inName)
	if result == "" {
		return inName
	}
	return result
}

func (c *imageCmd) shortestAlias(list []shared.ImageAlias) string {
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

func (c *imageCmd) findDescription(props map[string]string) string {
	for k, v := range props {
		if k == "description" {
			return v
		}
	}
	return ""
}

func (c *imageCmd) showImages(images []shared.ImageInfo, filters []string) error {
	data := [][]string{}
	for _, image := range images {
		if !c.imageShouldShow(filters, &image) {
			continue
		}

		shortest := c.shortestAlias(image.Aliases)
		if len(image.Aliases) > 1 {
			shortest = fmt.Sprintf(i18n.G("%s (%d more)"), shortest, len(image.Aliases)-1)
		}
		fp := image.Fingerprint[0:12]
		public := i18n.G("no")
		description := c.findDescription(image.Properties)

		if image.Public {
			public = i18n.G("yes")
		}

		const layout = "Jan 2, 2006 at 3:04pm (MST)"
		uploaded := image.UploadDate.UTC().Format(layout)
		size := fmt.Sprintf("%.2fMB", float64(image.Size)/1024.0/1024.0)
		data = append(data, []string{shortest, fp, public, description, image.Architecture, size, uploaded})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("ALIAS"),
		i18n.G("FINGERPRINT"),
		i18n.G("PUBLIC"),
		i18n.G("DESCRIPTION"),
		i18n.G("ARCH"),
		i18n.G("SIZE"),
		i18n.G("UPLOAD DATE")})
	sort.Sort(sortImage(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (c *imageCmd) showAliases(aliases shared.ImageAliases) error {
	data := [][]string{}
	for _, alias := range aliases {
		data = append(data, []string{alias.Name, alias.Target[0:12], alias.Description})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("ALIAS"),
		i18n.G("FINGERPRINT"),
		i18n.G("DESCRIPTION")})
	sort.Sort(sortImage(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (c *imageCmd) doImageEdit(client *lxd.Client, image string) error {
	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := shared.BriefImageInfo{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}
		return client.PutImageInfo(image, newdata)
	}

	// Extract the current value
	config, err := client.GetImageInfo(image)
	if err != nil {
		return err
	}

	brief := config.Brief()
	data, err := yaml.Marshal(&brief)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.imageEditHelp()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := shared.BriefImageInfo{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.PutImageInfo(image, newdata)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to start the editor again"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = shared.TextEditor("", content)
			if err != nil {
				return err
			}
			continue
		}
		break
	}
	return nil
}

func (c *imageCmd) imageShouldShow(filters []string, state *shared.ImageInfo) bool {
	if len(filters) == 0 {
		return true
	}

	for _, filter := range filters {
		found := false
		if strings.Contains(filter, "=") {
			membs := strings.SplitN(filter, "=", 2)

			key := membs[0]
			var value string
			if len(membs) < 2 {
				value = ""
			} else {
				value = membs[1]
			}

			for configKey, configValue := range state.Properties {
				list := listCmd{}
				if list.dotPrefixMatch(key, configKey) {
					//try to test filter value as a regexp
					regexpValue := value
					if !(strings.Contains(value, "^") || strings.Contains(value, "$")) {
						regexpValue = "^" + regexpValue + "$"
					}
					r, err := regexp.Compile(regexpValue)
					//if not regexp compatible use original value
					if err != nil {
						if value == configValue {
							found = true
							break
						}
					} else if r.MatchString(configValue) == true {
						found = true
						break
					}
				}
			}
		} else {
			for _, alias := range state.Aliases {
				if strings.Contains(alias.Name, filter) {
					found = true
					break
				}
			}
			if strings.Contains(state.Fingerprint, filter) {
				found = true
			}
		}

		if !found {
			return false
		}
	}

	return true
}
