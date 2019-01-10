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
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type imageColumn struct {
	Name string
	Data func(api.Image) string
}

type cmdImage struct {
	global *cmdGlobal
}

func (c *cmdImage) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("image")
	cmd.Short = i18n.G("Manage images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage images

In LXD containers are created from images. Those images were themselves
either generated from an existing container or downloaded from an image
server.

When using remote images, LXD will automatically cache images for you
and remove them upon expiration.

The image unique identifier is the hash (sha-256) of its representation
as a compressed tarball (or for split images, the concatenation of the
metadata and rootfs tarballs).

Images can be referenced by their full hash, shortest unique partial
hash or alias name (if one is set).`))

	// Alias
	imageAliasCmd := cmdImageAlias{global: c.global, image: c}
	cmd.AddCommand(imageAliasCmd.Command())

	// Copy
	imageCopyCmd := cmdImageCopy{global: c.global, image: c}
	cmd.AddCommand(imageCopyCmd.Command())

	// Delete
	imageDeleteCmd := cmdImageDelete{global: c.global, image: c}
	cmd.AddCommand(imageDeleteCmd.Command())

	// Edit
	imageEditCmd := cmdImageEdit{global: c.global, image: c}
	cmd.AddCommand(imageEditCmd.Command())

	// Export
	imageExportCmd := cmdImageExport{global: c.global, image: c}
	cmd.AddCommand(imageExportCmd.Command())

	// Import
	imageImportCmd := cmdImageImport{global: c.global, image: c}
	cmd.AddCommand(imageImportCmd.Command())

	// Info
	imageInfoCmd := cmdImageInfo{global: c.global, image: c}
	cmd.AddCommand(imageInfoCmd.Command())

	// List
	imageListCmd := cmdImageList{global: c.global, image: c}
	cmd.AddCommand(imageListCmd.Command())

	// Refresh
	imageRefreshCmd := cmdImageRefresh{global: c.global, image: c}
	cmd.AddCommand(imageRefreshCmd.Command())

	// Show
	imageShowCmd := cmdImageShow{global: c.global, image: c}
	cmd.AddCommand(imageShowCmd.Command())

	return cmd
}

func (c *cmdImage) dereferenceAlias(d lxd.ImageServer, inName string) string {
	if inName == "" {
		inName = "default"
	}

	result, _, _ := d.GetImageAlias(inName)
	if result == nil {
		return inName
	}

	return result.Target
}

// Copy
type cmdImageCopy struct {
	global *cmdGlobal
	image  *cmdImage

	flagAliases     []string
	flagPublic      bool
	flagCopyAliases bool
	flagAutoUpdate  bool
}

func (c *cmdImageCopy) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("copy [<remote>:]<image> <remote>:")
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy images between servers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy images between servers

The auto-update flag instructs the server to keep this image up to date.
It requires the source to be an alias and for it to be public.`))

	cmd.Flags().BoolVar(&c.flagPublic, "public", false, i18n.G("Make image public"))
	cmd.Flags().BoolVar(&c.flagCopyAliases, "copy-aliases", false, i18n.G("Copy aliases from source"))
	cmd.Flags().BoolVar(&c.flagAutoUpdate, "auto-update", false, i18n.G("Keep the image up to date after initial copy"))
	cmd.Flags().StringArrayVar(&c.flagAliases, "alias", nil, i18n.G("New aliases to add to the image")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageCopy) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse source remote
	remoteName, name, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	sourceServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Parse destination remote
	resources, err := c.global.ParseServers(args[1])
	if err != nil {
		return err
	}

	destinationServer := resources[0].server

	if resources[0].name != "" {
		return fmt.Errorf(i18n.G("Can't provide a name for the target image"))
	}

	// Copy the image
	var imgInfo *api.Image
	var fp string
	if conf.Remotes[remoteName].Protocol == "simplestreams" && !c.flagCopyAliases && len(c.flagAliases) == 0 {
		// All simplestreams images are always public, so unless we
		// need the aliases list too or the real fingerprint, we can skip the otherwise very expensive
		// alias resolution and image info retrieval step.
		imgInfo = &api.Image{}
		imgInfo.Fingerprint = name
		imgInfo.Public = true
	} else {
		// Resolve any alias and then grab the image information from the source
		image := c.image.dereferenceAlias(sourceServer, name)
		imgInfo, _, err = sourceServer.GetImage(image)
		if err != nil {
			return err
		}

		// Store the fingerprint for use when creating aliases later (as imgInfo.Fingerprint may be overridden)
		fp = imgInfo.Fingerprint
	}

	if imgInfo.Public && imgInfo.Fingerprint != name && !strings.HasPrefix(imgInfo.Fingerprint, name) {
		// If dealing with an alias, set the imgInfo fingerprint to match the provided alias (needed for auto-update)
		imgInfo.Fingerprint = name
	}

	copyArgs := lxd.ImageCopyArgs{
		AutoUpdate: c.flagAutoUpdate,
		Public:     c.flagPublic,
	}

	// Do the copy
	op, err := destinationServer.CopyImage(sourceServer, *imgInfo, &copyArgs)
	if err != nil {
		return err
	}

	// Register progress handler
	progress := utils.ProgressRenderer{
		Format: i18n.G("Copying the image: %s"),
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = utils.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done(i18n.G("Image copied successfully!"))

	// Ensure aliases
	aliases := make([]api.ImageAlias, len(c.flagAliases))
	for i, entry := range c.flagAliases {
		aliases[i].Name = entry
	}

	if c.flagCopyAliases {
		// Also add the original aliases
		for _, alias := range imgInfo.Aliases {
			aliases = append(aliases, alias)
		}
	}

	err = ensureImageAliases(destinationServer, aliases, fp)
	return err
}

// Delete
type cmdImageDelete struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("delete [<remote>:]<image> [[<remote>:]<image>...]")
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete images`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageDelete) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args...)
	if err != nil {
		return err
	}

	for _, resource := range resources {
		if resource.name == "" {
			return fmt.Errorf(i18n.G("Image identifier missing"))
		}

		image := c.image.dereferenceAlias(resource.server, resource.name)
		op, err := resource.server.DeleteImage(image)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	return nil
}

// Edit
type cmdImageEdit struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("edit [<remote>:]<image>")
	cmd.Short = i18n.G("Edit image properties")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit image properties`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc image edit <image>
    Launch a text editor to edit the properties

lxc image edit <image> < image.yaml
    Load the image properties from a YAML file`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageEdit) helpTemplate() string {
	return i18n.G(
		`### This is a yaml representation of the image properties.
### Any line starting with a '# will be ignored.
###
### Each property is represented by a single line:
### An example would be:
###  description: My custom image`)
}

func (c *cmdImageEdit) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Image identifier missing: %s"), args[0])
	}

	// Resolve any aliases
	image := c.image.dereferenceAlias(resource.server, resource.name)
	if image == "" {
		image = resource.name
	}

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

		return resource.server.UpdateImage(image, newdata, "")
	}

	// Extract the current value
	imgInfo, etag, err := resource.server.GetImage(image)
	if err != nil {
		return err
	}

	brief := imgInfo.Writable()
	data, err := yaml.Marshal(&brief)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.ImagePut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateImage(image, newdata, etag)
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

// Export
type cmdImageExport struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageExport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("export [<remote>:]<image> [<target>]")
	cmd.Short = i18n.G("Export and download images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Export and download images

The output target is optional and defaults to the working directory.`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageExport) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Parse remote
	remoteName, name, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Resolve aliases
	fingerprint := c.image.dereferenceAlias(remoteServer, name)

	// Default target is current directory
	target := "."
	targetMeta := fingerprint
	if len(args) > 1 {
		target = args[1]
		if shared.IsDir(args[1]) {
			targetMeta = filepath.Join(args[1], targetMeta)
		} else {
			targetMeta = args[1]
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
	progress := utils.ProgressRenderer{
		Format: i18n.G("Exporting the image: %s"),
		Quiet:  c.global.flagQuiet,
	}

	req := lxd.ImageFileRequest{
		MetaFile:        io.WriteSeeker(dest),
		RootfsFile:      io.WriteSeeker(destRootfs),
		ProgressHandler: progress.UpdateProgress,
	}

	// Download the image
	resp, err := remoteServer.GetImageFile(fingerprint, req)
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
	} else if resp.RootfsSize == 0 && len(args) > 1 {
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
}

// Import
type cmdImageImport struct {
	global *cmdGlobal
	image  *cmdImage

	flagPublic  bool
	flagAliases []string
}

func (c *cmdImageImport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("import <tarball>|<directory>|<URL> [<rootfs tarball>] [<remote>:] [key=value...]")
	cmd.Short = i18n.G("Import images into the image store")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Import image into the image store

Directory import is only available on Linux and must be performed as root.`))

	cmd.Flags().BoolVar(&c.flagPublic, "public", false, i18n.G("Make image public"))
	cmd.Flags().StringArrayVar(&c.flagAliases, "alias", nil, i18n.G("New aliases to add to the image")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageImport) packImageDir(path string) (string, error) {
	// Sanity checks
	if os.Geteuid() == -1 {
		return "", fmt.Errorf(i18n.G("Directory import is not available on this platform"))
	} else if os.Geteuid() != 0 {
		return "", fmt.Errorf(i18n.G("Must run as root to import from directory"))
	}

	outFile, err := ioutil.TempFile("", "lxd_image_")
	if err != nil {
		return "", err
	}
	defer outFile.Close()

	outFileName := outFile.Name()
	shared.RunCommand("tar", "-C", path, "--numeric-owner", "--xattrs", "-cJf", outFileName, "rootfs", "templates", "metadata.yaml")

	return outFileName, nil
}

func (c *cmdImageImport) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Import the image
	var imageFile string
	var rootfsFile string
	var properties []string
	var remote string

	for _, arg := range args {
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
		remote = conf.DefaultRemote
	}

	if imageFile == "" {
		imageFile = args[0]
	}

	if shared.PathExists(shared.HostPath(filepath.Clean(imageFile))) {
		imageFile = shared.HostPath(filepath.Clean(imageFile))
	}

	if rootfsFile != "" && shared.PathExists(shared.HostPath(filepath.Clean(rootfsFile))) {
		rootfsFile = shared.HostPath(filepath.Clean(rootfsFile))
	}

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	if strings.HasPrefix(imageFile, "http://") {
		return fmt.Errorf(i18n.G("Only https:// is supported for remote image import"))
	}

	createArgs := &lxd.ImageCreateArgs{}
	image := api.ImagesPost{}
	image.Public = c.flagPublic

	// Handle aliases
	aliases := []api.ImageAlias{}
	for _, entry := range c.flagAliases {
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

	progress := utils.ProgressRenderer{
		Format: i18n.G("Transferring image: %s"),
		Quiet:  c.global.flagQuiet,
	}

	if strings.HasPrefix(imageFile, "https://") {
		image.Source = &api.ImagesPostSource{}
		image.Source.Type = "url"
		image.Source.Mode = "pull"
		image.Source.Protocol = "direct"
		image.Source.URL = imageFile
		createArgs = nil
	} else {
		var meta io.ReadCloser
		var rootfs io.ReadCloser

		// Open meta
		if shared.IsDir(imageFile) {
			imageFile, err = c.packImageDir(imageFile)
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

		createArgs = &lxd.ImageCreateArgs{
			MetaFile:        meta,
			MetaName:        filepath.Base(imageFile),
			RootfsFile:      rootfs,
			RootfsName:      filepath.Base(rootfsFile),
			ProgressHandler: progress.UpdateProgress,
		}
		image.Filename = createArgs.MetaName
	}

	// Start the transfer
	op, err := d.CreateImage(image, createArgs)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = utils.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}
	opAPI := op.Get()

	// Get the fingerprint
	fingerprint := opAPI.Metadata["fingerprint"].(string)
	progress.Done(fmt.Sprintf(i18n.G("Image imported with fingerprint: %s"), fingerprint))

	// Add the aliases
	if len(c.flagAliases) > 0 {
		aliases := make([]api.ImageAlias, len(c.flagAliases))
		for i, entry := range c.flagAliases {
			aliases[i].Name = entry
		}
		err = ensureImageAliases(d, aliases, fingerprint)
		if err != nil {
			return err
		}
	}

	return nil
}

// Info
type cmdImageInfo struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageInfo) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("info [<remote>:]<image>")
	cmd.Short = i18n.G("Show useful information about images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show useful information about images`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageInfo) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remoteName, name, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Render info
	image := c.image.dereferenceAlias(remoteServer, name)
	info, _, err := remoteServer.GetImage(image)
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
}

// List
type cmdImageList struct {
	global *cmdGlobal
	image  *cmdImage

	flagFormat  string
	flagColumns string
}

func (c *cmdImageList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("list [<remote>:] [<filter>...]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List images

Filters may be of the <key>=<value> form for property based filtering,
or part of the image hash or part of the image alias name.

The -c option takes a (optionally comma-separated) list of arguments
that control which image attributes to output when displaying in table
or csv format.

Default column layout is: lfpdasu

Column shorthand chars:

    l - Shortest image alias (and optionally number of other aliases)
    L - Newline-separated list of all image aliases
    f - Fingerprint (short)
    F - Fingerprint (long)
    p - Whether image is public
    d - Description
    a - Architecture
    s - Size
    u - Upload date`))

	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", "lfpdasu", i18n.G("Columns")+"``")
	cmd.Flags().StringVar(&c.flagFormat, "format", "table", i18n.G("Format (csv|json|table|yaml)")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageList) parseColumns() ([]imageColumn, error) {
	columnsShorthandMap := map[rune]imageColumn{
		'l': {i18n.G("ALIAS"), c.aliasColumnData},
		'L': {i18n.G("ALIASES"), c.aliasesColumnData},
		'f': {i18n.G("FINGERPRINT"), c.fingerprintColumnData},
		'F': {i18n.G("FINGERPRINT"), c.fingerprintFullColumnData},
		'p': {i18n.G("PUBLIC"), c.publicColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'a': {i18n.G("ARCH"), c.architectureColumnData},
		's': {i18n.G("SIZE"), c.sizeColumnData},
		'u': {i18n.G("UPLOAD DATE"), c.uploadDateColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []imageColumn{}
	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing command) in '%s'"), c.flagColumns)
		}

		for _, columnRune := range columnEntry {
			if column, ok := columnsShorthandMap[columnRune]; ok {
				columns = append(columns, column)
			} else {
				return nil, fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
			}
		}
	}

	return columns, nil
}

func (c *cmdImageList) aliasColumnData(image api.Image) string {
	shortest := c.shortestAlias(image.Aliases)
	if len(image.Aliases) > 1 {
		shortest = fmt.Sprintf(i18n.G("%s (%d more)"), shortest, len(image.Aliases)-1)
	}

	return shortest
}

func (c *cmdImageList) aliasesColumnData(image api.Image) string {
	aliases := []string{}
	for _, alias := range image.Aliases {
		aliases = append(aliases, alias.Name)
	}
	sort.Strings(aliases)
	return strings.Join(aliases, "\n")
}

func (c *cmdImageList) fingerprintColumnData(image api.Image) string {
	return image.Fingerprint[0:12]
}

func (c *cmdImageList) fingerprintFullColumnData(image api.Image) string {
	return image.Fingerprint
}

func (c *cmdImageList) publicColumnData(image api.Image) string {
	if image.Public {
		return i18n.G("yes")
	}
	return i18n.G("no")
}

func (c *cmdImageList) descriptionColumnData(image api.Image) string {
	return c.findDescription(image.Properties)
}

func (c *cmdImageList) architectureColumnData(image api.Image) string {
	return image.Architecture
}

func (c *cmdImageList) sizeColumnData(image api.Image) string {
	return fmt.Sprintf("%.2fMB", float64(image.Size)/1024.0/1024.0)
}

func (c *cmdImageList) uploadDateColumnData(image api.Image) string {
	return image.UploadedAt.UTC().Format("Jan 2, 2006 at 3:04pm (MST)")
}

func (c *cmdImageList) shortestAlias(list []api.ImageAlias) string {
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

func (c *cmdImageList) findDescription(props map[string]string) string {
	for k, v := range props {
		if k == "description" {
			return v
		}
	}
	return ""
}

func (c *cmdImageList) imageShouldShow(filters []string, state *api.Image) bool {
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
				list := cmdList{}
				list.global = c.global
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

func (c *cmdImageList) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 0, -1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	remoteName, name, err := c.global.conf.ParseRemote(remote)
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Process the filters
	filters := []string{}
	if name != "" {
		filters = append(filters, name)
	}

	if len(args) > 1 {
		filters = append(filters, args[1:]...)
	}

	// Process the columns
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	var images []api.Image
	allImages, err := remoteServer.GetImages()
	if err != nil {
		return err
	}

	for _, image := range allImages {
		if !c.imageShouldShow(filters, &image) {
			continue
		}

		images = append(images, image)
	}

	// Render the table
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

		sort.Sort(stringList(data))
		return data
	}

	switch c.flagFormat {
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
		return fmt.Errorf(i18n.G("Invalid format %q"), c.flagFormat)
	}

	return nil
}

// Refresh
type cmdImageRefresh struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageRefresh) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("refresh [<remote>:]<image> [[<remote>:]<image>...]")
	cmd.Short = i18n.G("Refresh images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Refresh images`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageRefresh) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	for _, resource := range resources {
		if resource.name == "" {
			return fmt.Errorf(i18n.G("Image identifier missing"))
		}

		image := c.image.dereferenceAlias(resource.server, resource.name)
		progress := utils.ProgressRenderer{
			Format: i18n.G("Refreshing the image: %s"),
			Quiet:  c.global.flagQuiet,
		}

		op, err := resource.server.RefreshImage(image)
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
		opAPI := op.Get()

		// Check if refreshed
		refreshed := false
		flag, ok := opAPI.Metadata["refreshed"]
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
}

// Show
type cmdImageShow struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("show [<remote>:]<image>")
	cmd.Short = i18n.G("Show image properties")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show image properties`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageShow) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remoteName, name, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Show properties
	image := c.image.dereferenceAlias(remoteServer, name)
	info, _, err := remoteServer.GetImage(image)
	if err != nil {
		return err
	}

	properties := info.Writable()
	data, err := yaml.Marshal(&properties)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
