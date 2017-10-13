package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

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

type imageColumn struct {
	Name string
	Data func(api.Image) string
}

type imageCmd struct {
	addAliases  aliasList
	publicImage bool
	copyAliases bool
	autoUpdate  bool
	format      string
	columnsRaw  string
}

func (c *imageCmd) showByDefault() bool {
	return true
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

func (c *imageCmd) usage() string {
	return i18n.G(
		`Usage: lxc image <subcommand> [options]

Manipulate container images.

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


lxc image import <tarball>|<dir> [<rootfs tarball>|<URL>] [<remote>:] [--public] [--created-at=ISO-8601] [--expires-at=ISO-8601] [--fingerprint=FINGERPRINT] [--alias=ALIAS...] [prop=value]
    Import an image tarball (or tarballs) or an image directory into the LXD image store.
    Directory import is only available on Linux and must be performed as root.

lxc image copy [<remote>:]<image> <remote>: [--alias=ALIAS...] [--copy-aliases] [--public] [--auto-update]
    Copy an image from one LXD daemon to another over the network.

    The auto-update flag instructs the server to keep this image up to
    date. It requires the source to be an alias and for it to be public.

lxc image delete [<remote>:]<image> [[<remote>:]<image>...]
    Delete one or more images from the LXD image store.

lxc image refresh [<remote>:]<image> [[<remote>:]<image>...]
    Refresh one or more images from its parent remote.

lxc image export [<remote>:]<image> [target]
    Export an image from the LXD image store into a distributable tarball.

    The output target is optional and defaults to the working directory.
    The target may be an existing directory, file name, or "-" to specify
    stdout.  The target MUST be a directory when exporting a split image.
    If the target is a directory, the image's name (each part's name for
    split images) as found in the database will be used for the exported
    image.  If the target is a file (not a directory and not stdout), then
    the appropriate extension will be appended to the provided file name
    based on the algorithm used to compress the image.

lxc image info [<remote>:]<image>
    Print everything LXD knows about a given image.

lxc image list [<remote>:] [filter] [--format csv|json|table|yaml] [-c <columns>]
    List images in the LXD image store. Filters may be of the
    <key>=<value> form for property based filtering, or part of the image
    hash or part of the image alias name.

    The -c option takes a (optionally comma-separated) list of arguments that
    control which image attributes to output when displaying in table or csv
    format.

    Default column layout is: lfpdasu

    Column shorthand chars:

        l - Shortest image alias (and optionally number of other aliases)

        L - Newline-separated list of all image aliases

        f - Fingerprint

        p - Whether image is public

        d - Description

        a - Architecture

        s - Size

        u - Upload date

lxc image show [<remote>:]<image>
    Yaml output of the user modifiable properties of an image.

lxc image edit [<remote>:]<image>
    Edit image, either by launching external editor or reading STDIN.
    Example: lxc image edit <image> # launch editor
             cat image.yaml | lxc image edit <image> # read from image.yaml

lxc image alias create [<remote>:]<alias> <fingerprint>
    Create a new alias for an existing image.

lxc image alias rename [<remote>:]<alias> <new-name>
    Rename an alias.

lxc image alias delete [<remote>:]<alias>
    Delete an alias.

lxc image alias list [<remote>:] [filter]
    List the aliases. Filters may be part of the image hash or part of the image alias name.`)
}

func (c *imageCmd) flags() {
	gnuflag.StringVar(&c.columnsRaw, "c", "lfpdasu", i18n.G("Columns"))
	gnuflag.StringVar(&c.columnsRaw, "columns", "lfpdasu", i18n.G("Columns"))
	gnuflag.BoolVar(&c.publicImage, "public", false, i18n.G("Make image public"))
	gnuflag.BoolVar(&c.copyAliases, "copy-aliases", false, i18n.G("Copy aliases from source"))
	gnuflag.BoolVar(&c.autoUpdate, "auto-update", false, i18n.G("Keep the image up to date after initial copy"))
	gnuflag.Var(&c.addAliases, "alias", i18n.G("New alias to define at target"))
	gnuflag.StringVar(&c.format, "format", "table", i18n.G("Format (csv|json|table|yaml)"))
}

func (c *imageCmd) aliasColumnData(image api.Image) string {
	shortest := c.shortestAlias(image.Aliases)
	if len(image.Aliases) > 1 {
		shortest = fmt.Sprintf(i18n.G("%s (%d more)"), shortest, len(image.Aliases)-1)
	}

	return shortest
}

func (c *imageCmd) aliasesColumnData(image api.Image) string {
	aliases := []string{}
	for _, alias := range image.Aliases {
		aliases = append(aliases, alias.Name)
	}
	sort.Strings(aliases)
	return strings.Join(aliases, "\n")
}

func (c *imageCmd) fingerprintColumnData(image api.Image) string {
	return image.Fingerprint[0:12]
}

func (c *imageCmd) publicColumnData(image api.Image) string {
	if image.Public {
		return i18n.G("yes")
	}
	return i18n.G("no")
}

func (c *imageCmd) descriptionColumnData(image api.Image) string {
	return c.findDescription(image.Properties)
}

func (c *imageCmd) architectureColumnData(image api.Image) string {
	return image.Architecture
}

func (c *imageCmd) sizeColumnData(image api.Image) string {
	return fmt.Sprintf("%.2fMB", float64(image.Size)/1024.0/1024.0)
}

func (c *imageCmd) uploadDateColumnData(image api.Image) string {
	return image.UploadedAt.UTC().Format("Jan 2, 2006 at 3:04pm (MST)")
}

func (c *imageCmd) parseColumns() ([]imageColumn, error) {
	columnsShorthandMap := map[rune]imageColumn{
		'l': {i18n.G("ALIAS"), c.aliasColumnData},
		'L': {i18n.G("ALIASES"), c.aliasesColumnData},
		'f': {i18n.G("FINGERPRINT"), c.fingerprintColumnData},
		'p': {i18n.G("PUBLIC"), c.publicColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'a': {i18n.G("ARCH"), c.architectureColumnData},
		's': {i18n.G("SIZE"), c.sizeColumnData},
		'u': {i18n.G("UPLOAD DATE"), c.uploadDateColumnData},
	}

	columnList := strings.Split(c.columnsRaw, ",")

	columns := []imageColumn{}
	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf("Empty column entry (redundant, leading or trailing command) in '%s'", c.columnsRaw)
		}

		for _, columnRune := range columnEntry {
			if column, ok := columnsShorthandMap[columnRune]; ok {
				columns = append(columns, column)
			} else {
				return nil, fmt.Errorf("Unknown column shorthand char '%c' in '%s'", columnRune, columnEntry)
			}
		}
	}

	return columns, nil
}

func (c *imageCmd) doImageAlias(conf *config.Config, args []string) error {
	var remote string
	var err error

	switch args[1] {
	case "list":
		filters := []string{}

		if len(args) > 2 {
			result := strings.SplitN(args[2], ":", 2)
			if len(result) == 1 {
				filters = append(filters, args[2])
				remote, _, err = conf.ParseRemote("")
				if err != nil {
					return err
				}
			} else {
				remote, _, err = conf.ParseRemote(args[2])
				if err != nil {
					return err
				}
			}
		} else {
			remote, _, err = conf.ParseRemote("")
			if err != nil {
				return err
			}
		}

		if len(args) > 3 {
			for _, filter := range args[3:] {
				filters = append(filters, filter)
			}
		}

		d, err := conf.GetImageServer(remote)
		if err != nil {
			return err
		}

		resp, err := d.GetImageAliases()
		if err != nil {
			return err
		}

		c.showAliases(resp, filters)

		return nil
	case "create":
		/* alias create [<remote>:]<alias> <target> */
		if len(args) < 4 {
			return errArgs
		}

		remote, name, err := conf.ParseRemote(args[2])
		if err != nil {
			return err
		}

		d, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		alias := api.ImageAliasesPost{}
		alias.Name = name
		alias.Target = args[3]

		return d.CreateImageAlias(alias)
	case "rename":
		/* alias rename [<remote>:]<alias> <newname> */
		if len(args) < 4 {
			return errArgs
		}
		remote, alias, err := conf.ParseRemote(args[2])
		if err != nil {
			return err
		}

		d, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		return d.RenameImageAlias(alias, api.ImageAliasesEntryPost{Name: args[3]})
	case "delete":
		/* alias delete [<remote>:]<alias> */
		if len(args) < 3 {
			return errArgs
		}

		remote, alias, err := conf.ParseRemote(args[2])
		if err != nil {
			return err
		}

		d, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		return d.DeleteImageAlias(alias)
	}
	return errArgs
}

func (c *imageCmd) run(conf *config.Config, args []string) error {
	var remote string

	if len(args) < 1 {
		return errUsage
	}

	switch args[0] {
	case "alias":
		if len(args) < 2 {
			return errArgs
		}
		return c.doImageAlias(conf, args)

	case "copy":
		/* copy [<remote>:]<image> [<remote>:]<image> */
		if len(args) != 3 {
			return errArgs
		}

		remote, inName, err := conf.ParseRemote(args[1])
		if err != nil {
			return err
		}

		destRemote, outName, err := conf.ParseRemote(args[2])
		if err != nil {
			return err
		}

		if outName != "" {
			return errArgs
		}

		d, err := conf.GetImageServer(remote)
		if err != nil {
			return err
		}

		dest, err := conf.GetContainerServer(destRemote)
		if err != nil {
			return err
		}

		var imgInfo *api.Image
		var fp string
		if conf.Remotes[remote].Protocol == "simplestreams" && !c.copyAliases && len(c.addAliases) == 0 {
			// All simplestreams images are always public, so unless we
			// need the aliases list too or the real fingerprint, we can skip the otherwise very expensive
			// alias resolution and image info retrieval step.
			imgInfo = &api.Image{}
			imgInfo.Fingerprint = inName
			imgInfo.Public = true
		} else {
			// Resolve any alias and then grab the image information from the source
			image := c.dereferenceAlias(d, inName)
			imgInfo, _, err = d.GetImage(image)
			if err != nil {
				return err
			}

			// Store the fingerprint for use when creating aliases later (as imgInfo.Fingerprint may be overridden)
			fp = imgInfo.Fingerprint
		}

		if imgInfo.Public && imgInfo.Fingerprint != inName && !strings.HasPrefix(imgInfo.Fingerprint, inName) {
			// If dealing with an alias, set the imgInfo fingerprint to match the provided alias (needed for auto-update)
			imgInfo.Fingerprint = inName
		}

		args := lxd.ImageCopyArgs{
			AutoUpdate: c.autoUpdate,
			Public:     c.publicImage,
		}

		// Do the copy
		op, err := dest.CopyImage(d, *imgInfo, &args)
		if err != nil {
			return err
		}

		// Register progress handler
		progress := ProgressRenderer{Format: i18n.G("Copying the image: %s")}
		_, err = op.AddHandler(progress.UpdateOp)
		if err != nil {
			progress.Done("")
			return err
		}

		// Wait for operation to finish
		err = cancelableWait(op, &progress)
		if err != nil {
			progress.Done("")
			return err
		}

		progress.Done(i18n.G("Image copied successfully!"))

		// Ensure aliases
		aliases := make([]api.ImageAlias, len(c.addAliases))
		for i, entry := range c.addAliases {
			aliases[i].Name = entry
		}
		if c.copyAliases {
			// Also add the original aliases
			for _, alias := range imgInfo.Aliases {
				aliases = append(aliases, alias)
			}
		}
		err = ensureImageAliases(dest, aliases, fp)
		return err

	case "delete":
		/* delete [<remote>:]<image> [<remote>:][<image>...] */
		if len(args) < 2 {
			return errArgs
		}

		for _, arg := range args[1:] {
			var err error
			remote, inName, err := conf.ParseRemote(arg)
			if err != nil {
				return err
			}

			d, err := conf.GetContainerServer(remote)
			if err != nil {
				return err
			}

			image := c.dereferenceAlias(d, inName)
			op, err := d.DeleteImage(image)
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return err
			}
		}

		return nil

	case "refresh":
		/* refresh [<remote>:]<image> [<remote>:][<image>...] */
		if len(args) < 2 {
			return errArgs
		}

		for _, arg := range args[1:] {
			remote, inName, err := conf.ParseRemote(arg)
			if err != nil {
				return err
			}

			d, err := conf.GetContainerServer(remote)
			if err != nil {
				return err
			}

			image := c.dereferenceAlias(d, inName)
			progress := ProgressRenderer{Format: i18n.G("Refreshing the image: %s")}
			op, err := d.RefreshImage(image)
			if err != nil {
				return err
			}

			// Register progress handler
			_, err = op.AddHandler(progress.UpdateOp)
			if err != nil {
				return err
			}

			// Wait for the refresh to happen
			err = op.Wait()
			if err != nil {
				return err
			}

			// Check if refreshed
			refreshed := false
			flag, ok := op.Metadata["refreshed"]
			if ok {
				refreshed = flag.(bool)
			}

			if refreshed {
				progress.Done(i18n.G("Image refreshed successfully!"))
			} else {
				progress.Done(i18n.G("Image already up to date."))
			}
		}

		return nil

	case "info":
		if len(args) < 2 {
			return errArgs
		}

		remote, inName, err := conf.ParseRemote(args[1])
		if err != nil {
			return err
		}

		d, err := conf.GetImageServer(remote)
		if err != nil {
			return err
		}

		image := c.dereferenceAlias(d, inName)
		info, _, err := d.GetImage(image)
		if err != nil {
			return err
		}

		public := i18n.G("no")
		if info.Public {
			public = i18n.G("yes")
		}

		cached := i18n.G("no")
		if info.Cached {
			cached = i18n.G("yes")
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
		if shared.TimeIsSet(info.CreatedAt) {
			fmt.Printf("    "+i18n.G("Created: %s")+"\n", info.CreatedAt.UTC().Format(layout))
		}
		fmt.Printf("    "+i18n.G("Uploaded: %s")+"\n", info.UploadedAt.UTC().Format(layout))
		if shared.TimeIsSet(info.ExpiresAt) {
			fmt.Printf("    "+i18n.G("Expires: %s")+"\n", info.ExpiresAt.UTC().Format(layout))
		} else {
			fmt.Printf("    " + i18n.G("Expires: never") + "\n")
		}
		if shared.TimeIsSet(info.LastUsedAt) {
			fmt.Printf("    "+i18n.G("Last used: %s")+"\n", info.LastUsedAt.UTC().Format(layout))
		} else {
			fmt.Printf("    " + i18n.G("Last used: never") + "\n")
		}
		fmt.Println(i18n.G("Properties:"))
		for key, value := range info.Properties {
			fmt.Printf("    %s: %s\n", key, value)
		}
		fmt.Println(i18n.G("Aliases:"))
		for _, alias := range info.Aliases {
			if alias.Description != "" {
				fmt.Printf("    - %s (%s)\n", alias.Name, alias.Description)
			} else {
				fmt.Printf("    - %s\n", alias.Name)
			}
		}
		fmt.Printf(i18n.G("Cached: %s")+"\n", cached)
		fmt.Printf(i18n.G("Auto update: %s")+"\n", autoUpdate)
		if info.UpdateSource != nil {
			fmt.Println(i18n.G("Source:"))
			fmt.Printf("    Server: %s\n", info.UpdateSource.Server)
			fmt.Printf("    Protocol: %s\n", info.UpdateSource.Protocol)
			fmt.Printf("    Alias: %s\n", info.UpdateSource.Alias)
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
			if len(split) == 1 || shared.PathExists(shared.HostPath(arg)) {
				if strings.HasSuffix(arg, ":") {
					var err error
					remote, _, err = conf.ParseRemote(arg)
					if err != nil {
						return err
					}
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

		if remote == "" {
			remote = conf.DefaultRemote
		}

		if imageFile == "" {
			imageFile = args[1]
			properties = properties[1:]
		}
		imageFile = shared.HostPath(filepath.Clean(imageFile))

		d, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		if strings.HasPrefix(imageFile, "http://") {
			return fmt.Errorf(i18n.G("Only https:// is supported for remote image import."))
		}

		var args *lxd.ImageCreateArgs
		image := api.ImagesPost{}
		image.Public = c.publicImage

		// Handle aliases
		aliases := []api.ImageAlias{}
		for _, entry := range c.addAliases {
			alias := api.ImageAlias{}
			alias.Name = entry
			aliases = append(aliases, alias)
		}

		// Handle properties
		for _, entry := range properties {
			fields := strings.SplitN(entry, "=", 2)
			if len(fields) < 2 {
				return fmt.Errorf(i18n.G("Bad property: %s"), entry)
			}

			if image.Properties == nil {
				image.Properties = map[string]string{}
			}

			image.Properties[strings.TrimSpace(fields[0])] = strings.TrimSpace(fields[1])
		}

		progress := ProgressRenderer{Format: i18n.G("Transferring image: %s")}
		if strings.HasPrefix(imageFile, "https://") {
			image.Source = &api.ImagesPostSource{}
			image.Source.Type = "url"
			image.Source.Mode = "pull"
			image.Source.Protocol = "direct"
			image.Source.URL = imageFile
		} else {
			var meta io.ReadCloser
			var rootfs io.ReadCloser

			// Open meta
			if shared.IsDir(imageFile) {
				imageFile, err = packImageDir(imageFile)
				if err != nil {
					return err
				}
				// remove temp file
				defer os.Remove(imageFile)

			}
			meta, err = os.Open(imageFile)
			if err != nil {
				return err
			}
			defer meta.Close()

			// Open rootfs
			if rootfsFile != "" {
				rootfs, err = os.Open(rootfsFile)
				if err != nil {
					return err
				}
				defer rootfs.Close()
			}

			args = &lxd.ImageCreateArgs{
				MetaFile:        meta,
				MetaName:        filepath.Base(imageFile),
				RootfsFile:      rootfs,
				RootfsName:      filepath.Base(rootfsFile),
				ProgressHandler: progress.UpdateProgress,
			}
			image.Filename = args.MetaName
		}

		// Start the transfer
		op, err := d.CreateImage(image, args)
		if err != nil {
			progress.Done("")
			return err
		}

		err = op.Wait()
		if err != nil {
			progress.Done("")
			return err
		}

		// Get the fingerprint
		fingerprint := op.Metadata["fingerprint"].(string)
		progress.Done(fmt.Sprintf(i18n.G("Image imported with fingerprint: %s"), fingerprint))

		// Add the aliases
		if len(c.addAliases) > 0 {
			aliases := make([]api.ImageAlias, len(c.addAliases))
			for i, entry := range c.addAliases {
				aliases[i].Name = entry
			}
			err = ensureImageAliases(d, aliases, fingerprint)
			if err != nil {
				return err
			}
		}
		return nil

	case "list":
		columns, err := c.parseColumns()
		if err != nil {
			return err
		}

		filters := []string{}
		if len(args) > 1 {
			result := strings.SplitN(args[1], ":", 2)
			if len(result) == 1 {
				filters = append(filters, args[1])

				remote, _, err = conf.ParseRemote("")
				if err != nil {
					return err
				}
			} else {
				var filter string
				remote, filter, err = conf.ParseRemote(args[1])
				if err != nil {
					return err
				}

				if filter != "" {
					filters = append(filters, filter)
				}
			}
		} else {
			remote, _, err = conf.ParseRemote("")
			if err != nil {
				return err
			}
		}

		if len(args) > 2 {
			for _, filter := range args[2:] {
				filters = append(filters, filter)
			}
		}

		d, err := conf.GetImageServer(remote)
		if err != nil {
			return err
		}

		var images []api.Image
		allImages, err := d.GetImages()
		if err != nil {
			return err
		}

		for _, image := range allImages {
			if !c.imageShouldShow(filters, &image) {
				continue
			}

			images = append(images, image)
		}

		return c.showImages(images, filters, columns)

	case "edit":
		if len(args) < 2 {
			return errArgs
		}

		remote, inName, err := conf.ParseRemote(args[1])
		if err != nil {
			return err
		}

		d, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		image := c.dereferenceAlias(d, inName)
		if image == "" {
			image = inName
		}

		return c.doImageEdit(d, image)

	case "export":
		if len(args) < 2 {
			return errArgs
		}

		remote, inName, err := conf.ParseRemote(args[1])
		if err != nil {
			return err
		}

		d, err := conf.GetImageServer(remote)
		if err != nil {
			return err
		}

		// Resolve aliases
		fingerprint := c.dereferenceAlias(d, inName)

		// Default target is current directory
		target := "."
		targetMeta := fingerprint
		if len(args) > 2 {
			target = args[2]
			if shared.IsDir(args[2]) {
				targetMeta = filepath.Join(args[2], targetMeta)
			} else {
				targetMeta = args[2]
			}
		}
		targetMeta = shared.HostPath(targetMeta)
		targetRootfs := targetMeta + ".root"

		// Prepare the files
		dest, err := os.Create(targetMeta)
		if err != nil {
			return err
		}
		defer dest.Close()

		destRootfs, err := os.Create(targetRootfs)
		if err != nil {
			return err
		}
		defer destRootfs.Close()

		// Prepare the download request
		progress := ProgressRenderer{Format: i18n.G("Exporting the image: %s")}
		req := lxd.ImageFileRequest{
			MetaFile:        io.WriteSeeker(dest),
			RootfsFile:      io.WriteSeeker(destRootfs),
			ProgressHandler: progress.UpdateProgress,
		}

		// Download the image
		resp, err := d.GetImageFile(fingerprint, req)
		if err != nil {
			os.Remove(targetMeta)
			os.Remove(targetRootfs)
			progress.Done("")
			return err
		}

		// Cleanup
		if resp.RootfsSize == 0 {
			err := os.Remove(targetRootfs)
			if err != nil {
				os.Remove(targetMeta)
				os.Remove(targetRootfs)
				progress.Done("")
				return err
			}
		}

		// Rename files
		if shared.IsDir(target) {
			if resp.MetaName != "" {
				err := os.Rename(targetMeta, shared.HostPath(filepath.Join(target, resp.MetaName)))
				if err != nil {
					os.Remove(targetMeta)
					os.Remove(targetRootfs)
					progress.Done("")
					return err
				}
			}

			if resp.RootfsSize > 0 && resp.RootfsName != "" {
				err := os.Rename(targetRootfs, shared.HostPath(filepath.Join(target, resp.RootfsName)))
				if err != nil {
					os.Remove(targetMeta)
					os.Remove(targetRootfs)
					progress.Done("")
					return err
				}
			}
		} else if resp.RootfsSize == 0 && len(args) > 2 {
			if resp.MetaName != "" {
				extension := strings.SplitN(resp.MetaName, ".", 2)[1]
				err := os.Rename(targetMeta, fmt.Sprintf("%s.%s", targetMeta, extension))
				if err != nil {
					os.Remove(targetMeta)
					progress.Done("")
					return err
				}
			}
		}

		progress.Done(i18n.G("Image exported successfully!"))
		return nil

	case "show":
		if len(args) < 2 {
			return errArgs
		}

		remote, inName, err := conf.ParseRemote(args[1])
		if err != nil {
			return err
		}

		d, err := conf.GetImageServer(remote)
		if err != nil {
			return err
		}

		image := c.dereferenceAlias(d, inName)
		info, _, err := d.GetImage(image)
		if err != nil {
			return err
		}

		properties := info.Writable()

		data, err := yaml.Marshal(&properties)
		fmt.Printf("%s", data)
		return err

	default:
		return errArgs
	}
}

func (c *imageCmd) dereferenceAlias(d lxd.ImageServer, inName string) string {
	if inName == "" {
		inName = "default"
	}

	result, _, _ := d.GetImageAlias(inName)
	if result == nil {
		return inName
	}

	return result.Target
}

func (c *imageCmd) shortestAlias(list []api.ImageAlias) string {
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

func (c *imageCmd) showImages(images []api.Image, filters []string, columns []imageColumn) error {
	tableData := func() [][]string {
		data := [][]string{}
		for _, image := range images {
			if !c.imageShouldShow(filters, &image) {
				continue
			}

			row := []string{}
			for _, column := range columns {
				row = append(row, column.Data(image))
			}
			data = append(data, row)
		}

		sort.Sort(StringList(data))
		return data
	}

	switch c.format {
	case listFormatCSV:
		w := csv.NewWriter(os.Stdout)
		w.WriteAll(tableData())
		if err := w.Error(); err != nil {
			return err
		}
	case listFormatTable:

		table := tablewriter.NewWriter(os.Stdout)
		table.SetAutoWrapText(false)
		table.SetAlignment(tablewriter.ALIGN_LEFT)
		table.SetRowLine(true)
		headers := []string{}
		for _, column := range columns {
			headers = append(headers, column.Name)
		}
		table.SetHeader(headers)
		table.AppendBulk(tableData())
		table.Render()
	case listFormatJSON:
		data := make([]*api.Image, len(images))
		for i := range images {
			data[i] = &images[i]
		}
		enc := json.NewEncoder(os.Stdout)
		err := enc.Encode(data)
		if err != nil {
			return err
		}
	case listFormatYAML:
		data := make([]*api.Image, len(images))
		for i := range images {
			data[i] = &images[i]
		}

		out, err := yaml.Marshal(data)
		if err != nil {
			return err
		}
		fmt.Printf("%s", out)
	default:
		return fmt.Errorf("invalid format %q", c.format)
	}

	return nil
}

func (c *imageCmd) showAliases(aliases []api.ImageAliasesEntry, filters []string) error {
	data := [][]string{}
	for _, alias := range aliases {
		if !c.aliasShouldShow(filters, &alias) {
			continue
		}

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
	sort.Sort(StringList(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (c *imageCmd) doImageEdit(client lxd.ContainerServer, image string) error {
	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ImagePut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return client.UpdateImage(image, newdata, "")
	}

	// Extract the current value
	imgInfo, etag, err := client.GetImage(image)
	if err != nil {
		return err
	}

	brief := imgInfo.Writable()
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
		newdata := api.ImagePut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.UpdateImage(image, newdata, etag)
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

func (c *imageCmd) imageShouldShow(filters []string, state *api.Image) bool {
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

func (c *imageCmd) aliasShouldShow(filters []string, state *api.ImageAliasesEntry) bool {
	if len(filters) == 0 {
		return true
	}

	for _, filter := range filters {
		if strings.Contains(state.Name, filter) || strings.Contains(state.Target, filter) {
			return true
		}
	}

	return false
}

// Package the image from the specified directory, if running as root.  Return
// the image filename
func packImageDir(path string) (string, error) {
	switch os.Geteuid() {
	case 0:
	case -1:
		return "", fmt.Errorf(
			i18n.G("Directory import is not available on this platform"))
	default:
		return "", fmt.Errorf(i18n.G("Must run as root to import from directory"))
	}

	outFile, err := ioutil.TempFile("", "lxd_image_")
	if err != nil {
		return "", err
	}
	defer outFile.Close()
	outFileName := outFile.Name()

	shared.RunCommand("tar", "-C", path, "--numeric-owner", "-cJf", outFileName, "rootfs", "templates", "metadata.yaml")
	return outFileName, nil
}
