package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/termios"
)

type imageColumn struct {
	Name string
	Data func(api.Image) string
}

type cmdImage struct {
	global *cmdGlobal
}

func (c *cmdImage) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("image")
	cmd.Short = "Manage images"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

In LXD instances are created from images. Those images were themselves
either generated from an existing instance or downloaded from an image
server.

When using remote images, LXD will automatically cache images for you
and remove them upon expiration.

The image unique identifier is the hash (sha-256) of its representation
as a compressed tarball (or for split images, the concatenation of the
metadata and rootfs tarballs).

Images can be referenced by their full hash, shortest unique partial
hash or alias name (if one is set).`)

	// Alias
	imageAliasCmd := cmdImageAlias{global: c.global, image: c}
	cmd.AddCommand(imageAliasCmd.command())

	// Copy
	imageCopyCmd := cmdImageCopy{global: c.global, image: c}
	cmd.AddCommand(imageCopyCmd.command())

	// Delete
	imageDeleteCmd := cmdImageDelete{global: c.global, image: c}
	cmd.AddCommand(imageDeleteCmd.command())

	// Edit
	imageEditCmd := cmdImageEdit{global: c.global, image: c}
	cmd.AddCommand(imageEditCmd.command())

	// Export
	imageExportCmd := cmdImageExport{global: c.global, image: c}
	cmd.AddCommand(imageExportCmd.command())

	// Import
	imageImportCmd := cmdImageImport{global: c.global, image: c}
	cmd.AddCommand(imageImportCmd.command())

	// Info
	imageInfoCmd := cmdImageInfo{global: c.global, image: c}
	cmd.AddCommand(imageInfoCmd.command())

	// List
	imageListCmd := cmdImageList{global: c.global, image: c}
	cmd.AddCommand(imageListCmd.command())

	// Refresh
	imageRefreshCmd := cmdImageRefresh{global: c.global, image: c}
	cmd.AddCommand(imageRefreshCmd.command())

	// Show
	imageShowCmd := cmdImageShow{global: c.global, image: c}
	cmd.AddCommand(imageShowCmd.command())

	// Get-property
	imageGetPropCmd := cmdImageGetProp{global: c.global, image: c}
	cmd.AddCommand(imageGetPropCmd.command())

	// Set-property
	imageSetPropCmd := cmdImageSetProp{global: c.global, image: c}
	cmd.AddCommand(imageSetPropCmd.command())

	// Unset-property
	imageUnsetPropCmd := cmdImageUnsetProp{global: c.global, image: c, imageSetProp: &imageSetPropCmd}
	cmd.AddCommand(imageUnsetPropCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// dereferenceAlias resolves an alias (or a fingerprint) to an image and returns the image and its etag.
func (c *cmdImage) dereferenceAlias(d lxd.ImageServer, imageType string, inName string) (image *api.Image, etag string, err error) {
	if inName == "" {
		inName = "default"
	}

	result, _, err := d.GetImageAliasType(imageType, inName)
	if err != nil {
		// Maybe that inName is a fingerprint and can't be found as an alias
		image, etag, errImage := d.GetImage(inName)
		if errImage != nil {
			return nil, "", fmt.Errorf("Failed fetching fingerprint %q: %w", inName, errImage)
		}

		return image, etag, nil
	}

	// Alias could be resolved, return its image
	image, etag, err = d.GetImage(result.Target)
	if err != nil {
		return nil, "", fmt.Errorf("Failed fetching fingerprint %q for alias %q: %w", result.Target, inName, err)
	}

	return image, etag, nil
}

// Copy.
type cmdImageCopy struct {
	global *cmdGlobal
	image  *cmdImage

	flagAliases       []string
	flagPublic        bool
	flagCopyAliases   bool
	flagAutoUpdate    bool
	flagVM            bool
	flagMode          string
	flagTargetProject string
	flagProfile       []string
}

func (c *cmdImageCopy) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("copy", "[<remote>:]<image> <remote>:")
	cmd.Aliases = []string{"cp"}
	cmd.Short = "Copy image between servers"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

The auto-update flag instructs the server to keep this image up to date.
It requires the source to be an alias and for it to be public.`)

	cmd.Flags().BoolVar(&c.flagPublic, "public", false, "Make image public")
	cmd.Flags().BoolVar(&c.flagCopyAliases, "copy-aliases", false, "Copy aliases from source")
	cmd.Flags().BoolVar(&c.flagAutoUpdate, "auto-update", false, "Keep the image up to date after initial copy")
	cmd.Flags().StringArrayVar(&c.flagAliases, "alias", nil, cli.FormatStringFlagLabel("New aliases to add to the image"))
	cmd.Flags().BoolVar(&c.flagVM, "vm", false, "Copy virtual machine images")
	cmd.Flags().StringVar(&c.flagMode, "mode", "pull", cli.FormatStringFlagLabel("Transfer mode. One of pull (default), push or relay"))
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", cli.FormatStringFlagLabel("Copy to a project different from the source"))
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, cli.FormatStringFlagLabel("Profile to apply to the new image"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete, false)
		}

		if len(args) == 1 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageCopy) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	if c.flagMode != "pull" && c.flagAutoUpdate {
		return errors.New("Auto update is only available in pull mode")
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
		return errors.New("Can't provide a name for the target image")
	}

	// Resolve image type
	imageType := ""
	if c.flagVM {
		imageType = "virtual-machine"
	}

	if c.flagTargetProject != "" {
		destinationServer = destinationServer.UseProject(c.flagTargetProject)
	}

	// Copy the image
	var imgInfo *api.Image
	var fp string

	// Resolve any alias and then grab the image information from the source
	imgInfo, _, err = c.image.dereferenceAlias(sourceServer, imageType, name)
	if err != nil {
		return err
	}

	// Store the fingerprint for use when creating aliases later (as imgInfo.Fingerprint may be overridden)
	fp = imgInfo.Fingerprint

	if imgInfo.Public && imgInfo.Fingerprint != name && !strings.HasPrefix(imgInfo.Fingerprint, name) {
		// If dealing with an alias, set the imgInfo fingerprint to match the provided alias (needed for auto-update)
		imgInfo.Fingerprint = name
	}

	copyArgs := lxd.ImageCopyArgs{
		AutoUpdate: c.flagAutoUpdate,
		Public:     c.flagPublic,
		Type:       imageType,
		Mode:       c.flagMode,
		Profiles:   c.flagProfile,
	}

	// Do the copy
	op, err := destinationServer.CopyImage(sourceServer, *imgInfo, &copyArgs)
	if err != nil {
		return err
	}

	// Register progress handler
	progress := cli.ProgressRenderer{
		Format: "Copying the image: %s",
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("Image copied successfully!")

	// Ensure aliases
	aliases := make([]api.ImageAlias, len(c.flagAliases))
	for i, entry := range c.flagAliases {
		aliases[i].Name = entry
	}

	if c.flagCopyAliases {
		// Also add the original aliases
		aliases = append(aliases, imgInfo.Aliases...)
	}

	err = ensureImageAliases(destinationServer, aliases, fp)
	if err != nil {
		return err
	}

	return nil
}

// Delete.
type cmdImageDelete struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", "[<remote>:]<image> [[<remote>:]<image>...]")
	cmd.Aliases = []string{"rm"}
	cmd.Short = "Delete images"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpImages(toComplete, true)
	}

	return cmd
}

func (c *cmdImageDelete) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
			return errors.New("Image identifier missing")
		}

		image, _, err := c.image.dereferenceAlias(resource.server, "", resource.name)
		if err != nil {
			return err
		}

		op, err := resource.server.DeleteImage(image.Fingerprint)
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

// Edit.
type cmdImageEdit struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", "[<remote>:]<image>")
	cmd.Short = "Edit image properties"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc image edit <image>
    Launch a text editor to edit the properties

lxc image edit <image> < image.yaml
    Load the image properties from a YAML file`)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete, true)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageEdit) helpTemplate() string {
	return `### This is a YAML representation of the image properties.
### Any line starting with a '# will be ignored.
###
### Each property is represented by a single line:
### An example would be:
###  description: My custom image`
}

func (c *cmdImageEdit) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return fmt.Errorf("Image identifier missing: %s", args[0])
	}

	// Resolve any aliases
	image, etag, err := c.image.dereferenceAlias(resource.server, "", resource.name)
	if err != nil {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ImagePut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateImage(image.Fingerprint, newdata, "")
	}

	brief := image.Writable()
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
			err = resource.server.UpdateImage(image.Fingerprint, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, "Config parsing error: %s\n", err)
			fmt.Println("Press enter to open the editor again or ctrl+c to abort change")

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

// Export.
type cmdImageExport struct {
	global *cmdGlobal
	image  *cmdImage

	flagVM bool
}

func (c *cmdImageExport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("export", "[<remote>:]<image> [<target>]")
	cmd.Short = "Export and download images"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

The output target is optional and defaults to the working directory.`)

	cmd.Flags().BoolVar(&c.flagVM, "vm", false, "Query virtual machine images")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageExport) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
	imageType := ""
	if c.flagVM {
		imageType = "virtual-machine"
	}

	image, _, err := c.image.dereferenceAlias(remoteServer, imageType, name)
	if err != nil {
		return err
	}

	// Default target is current directory
	target := "."
	targetMeta := image.Fingerprint
	if len(args) > 1 {
		target = args[1]
		if shared.IsDir(shared.HostPathFollow(args[1])) {
			targetMeta = filepath.Join(args[1], targetMeta)
		} else {
			targetMeta = args[1]
		}
	}
	targetMeta = shared.HostPathFollow(targetMeta)
	targetRootfs := targetMeta + ".root"

	// Prepare the files
	dest, err := os.Create(targetMeta)
	if err != nil {
		return err
	}

	defer func() { _ = dest.Close() }()

	destRootfs, err := os.Create(targetRootfs)
	if err != nil {
		return err
	}

	defer func() { _ = destRootfs.Close() }()

	// Prepare the download request
	progress := cli.ProgressRenderer{
		Format: "Exporting the image: %s",
		Quiet:  c.global.flagQuiet,
	}

	req := lxd.ImageFileRequest{
		MetaFile:        io.WriteSeeker(dest),
		RootfsFile:      io.WriteSeeker(destRootfs),
		ProgressHandler: progress.UpdateProgress,
	}

	// Download the image
	resp, err := remoteServer.GetImageFile(image.Fingerprint, req)
	if err != nil {
		_ = os.Remove(targetMeta)
		_ = os.Remove(targetRootfs)
		progress.Done("")
		return err
	}

	// Truncate down to size
	if resp.RootfsSize > 0 {
		err = destRootfs.Truncate(resp.RootfsSize)
		if err != nil {
			return err
		}
	}

	err = dest.Truncate(resp.MetaSize)
	if err != nil {
		return err
	}

	// Cleanup
	if resp.RootfsSize == 0 {
		err := os.Remove(targetRootfs)
		if err != nil {
			_ = os.Remove(targetMeta)
			_ = os.Remove(targetRootfs)
			progress.Done("")
			return err
		}
	}

	// Rename files
	if shared.IsDir(shared.HostPathFollow(target)) {
		if resp.MetaName != "" {
			err := os.Rename(targetMeta, shared.HostPathFollow(filepath.Join(target, resp.MetaName)))
			if err != nil {
				_ = os.Remove(targetMeta)
				_ = os.Remove(targetRootfs)
				progress.Done("")
				return err
			}
		}

		if resp.RootfsSize > 0 && resp.RootfsName != "" {
			err := os.Rename(targetRootfs, shared.HostPathFollow(filepath.Join(target, resp.RootfsName)))
			if err != nil {
				_ = os.Remove(targetMeta)
				_ = os.Remove(targetRootfs)
				progress.Done("")
				return err
			}
		}
	} else if resp.RootfsSize == 0 && len(args) > 1 {
		_, extension, _ := strings.Cut(resp.MetaName, ".")
		if extension != "" {
			err := os.Rename(targetMeta, targetMeta+"."+extension)
			if err != nil {
				_ = os.Remove(targetMeta)
				progress.Done("")
				return err
			}
		}
	}

	progress.Done("Image exported successfully!")
	return nil
}

// Import.
type cmdImageImport struct {
	global *cmdGlobal
	image  *cmdImage

	flagPublic  bool
	flagAliases []string
}

func (c *cmdImageImport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("import", "<tarball>|<directory>|<URL> [<rootfs tarball>] [<remote>:] [key=value...]")
	cmd.Short = "Import image into the image store"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

Directory import is only available on Linux and must be performed as root.

Descriptive properties can be set by providing key=value pairs. Example: os=Ubuntu release=noble variant=cloud.`)

	cmd.Flags().BoolVar(&c.flagPublic, "public", false, "Make image public")
	cmd.Flags().StringArrayVar(&c.flagAliases, "alias", nil, cli.FormatStringFlagLabel("New aliases to add to the image"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		if len(args) == 1 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageImport) packImageDir(path string) (string, error) {
	// Quick checks.
	if os.Geteuid() == -1 {
		return "", errors.New("Directory import is not available on this platform")
	} else if os.Geteuid() != 0 {
		return "", errors.New("Must run as root to import from directory")
	}

	outFile, err := os.CreateTemp("", "lxd_image_")
	if err != nil {
		return "", err
	}

	defer func() { _ = outFile.Close() }()

	outFileName := outFile.Name()
	_, err = shared.RunCommand(context.TODO(), "tar", "-C", path, "--numeric-owner", "--restrict", "--force-local", "--xattrs", "-cJf", outFileName, "rootfs", "templates", "metadata.yaml")
	if err != nil {
		return "", err
	}

	return outFileName, outFile.Close()
}

func (c *cmdImageImport) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
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
		if len(split) == 1 || shared.PathExists(shared.HostPathFollow(arg)) {
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

	if shared.PathExists(shared.HostPathFollow(filepath.Clean(imageFile))) {
		imageFile = shared.HostPathFollow(filepath.Clean(imageFile))
	}

	if rootfsFile != "" && shared.PathExists(shared.HostPathFollow(filepath.Clean(rootfsFile))) {
		rootfsFile = shared.HostPathFollow(filepath.Clean(rootfsFile))
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	if strings.HasPrefix(imageFile, "http://") {
		return errors.New("Only https:// is supported for remote image import")
	}

	var createArgs *lxd.ImageCreateArgs
	image := api.ImagesPost{}
	image.Public = c.flagPublic

	// Handle properties
	for _, entry := range properties {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			return fmt.Errorf("Bad property: %s", entry)
		}

		if image.Properties == nil {
			image.Properties = map[string]string{}
		}

		image.Properties[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	progress := cli.ProgressRenderer{
		Format: "Transferring image: %s",
		Quiet:  c.global.flagQuiet,
	}

	imageType := "container"
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
			defer func() { _ = os.Remove(imageFile) }()
		}

		meta, err = os.Open(imageFile)
		if err != nil {
			return err
		}

		defer func() { _ = meta.Close() }()

		// Open rootfs
		if rootfsFile != "" {
			rootfs, err = os.Open(rootfsFile)
			if err != nil {
				return err
			}

			defer func() { _ = rootfs.Close() }()

			_, ext, _, err := shared.DetectCompressionFile(rootfs)
			if err != nil {
				return err
			}

			_, err = rootfs.(*os.File).Seek(0, io.SeekStart)
			if err != nil {
				return err
			}

			if ext == ".qcow2" {
				imageType = "virtual-machine"
			}
		}

		createArgs = &lxd.ImageCreateArgs{
			MetaFile:        meta,
			MetaName:        filepath.Base(imageFile),
			RootfsFile:      rootfs,
			RootfsName:      filepath.Base(rootfsFile),
			ProgressHandler: progress.UpdateProgress,
			Type:            imageType,
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
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	opAPI := op.Get()

	// Get the fingerprint
	fingerprint, ok := opAPI.Metadata["fingerprint"].(string)
	if !ok {
		return fmt.Errorf(`Invalid type %T for "fingerprint" key in operation metadata`, fingerprint)
	}

	progress.Done("Image imported with fingerprint: " + fingerprint)

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

// Info.
type cmdImageInfo struct {
	global *cmdGlobal
	image  *cmdImage

	flagVM bool
}

func (c *cmdImageInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", "[<remote>:]<image>")
	cmd.Short = "Show useful information about image"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.Flags().BoolVar(&c.flagVM, "vm", false, "Query virtual machine images")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageInfo) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
	imageType := ""
	if c.flagVM {
		imageType = "virtual-machine"
	}

	info, _, err := c.image.dereferenceAlias(remoteServer, imageType, name)
	if err != nil {
		return err
	}

	public := "no"
	if info.Public {
		public = "yes"
	}

	cached := "no"
	if info.Cached {
		cached = "yes"
	}

	autoUpdate := "disabled"
	if info.AutoUpdate {
		autoUpdate = "enabled"
	}

	imgType := "container"
	if info.Type != "" {
		imgType = info.Type
	}

	fmt.Printf("Fingerprint: %s\n", info.Fingerprint)
	fmt.Printf("Size: %.2fMiB\n", float64(info.Size)/1024.0/1024.0)
	fmt.Printf("Architecture: %s\n", info.Architecture)
	fmt.Printf("Type: %s\n", imgType)
	fmt.Printf("Public: %s\n", public)
	fmt.Print("Timestamps:\n")

	const layout = "2006/01/02 15:04 UTC"
	if shared.TimeIsSet(info.CreatedAt) {
		fmt.Printf("    Created: %s\n", info.CreatedAt.UTC().Format(layout))
	}

	fmt.Printf("    Uploaded: %s\n", info.UploadedAt.UTC().Format(layout))

	if shared.TimeIsSet(info.ExpiresAt) {
		fmt.Printf("    Expires: %s\n", info.ExpiresAt.UTC().Format(layout))
	} else {
		fmt.Print("    Expires: never\n")
	}

	if shared.TimeIsSet(info.LastUsedAt) {
		fmt.Printf("    Last used: %s\n", info.LastUsedAt.UTC().Format(layout))
	} else {
		fmt.Print("    Last used: never\n")
	}

	fmt.Println("Properties:")
	for key, value := range info.Properties {
		fmt.Printf("    %s: %s\n", key, value)
	}

	fmt.Println("Aliases:")
	for _, alias := range info.Aliases {
		if alias.Description != "" {
			fmt.Printf("    - %s (%s)\n", alias.Name, alias.Description)
		} else {
			fmt.Printf("    - %s\n", alias.Name)
		}
	}

	fmt.Printf("Cached: %s\n", cached)
	fmt.Printf("Auto update: %s\n", autoUpdate)

	if info.UpdateSource != nil {
		fmt.Println("Source:")
		fmt.Printf("    Server: %s\n", info.UpdateSource.Server)
		fmt.Printf("    Protocol: %s\n", info.UpdateSource.Protocol)
		fmt.Printf("    Alias: %s\n", info.UpdateSource.Alias)
	}

	if len(info.Profiles) == 0 {
		fmt.Print("Profiles: []\n")
	} else {
		fmt.Println("Profiles:")
		for _, name := range info.Profiles {
			fmt.Printf("    - %s\n", name)
		}
	}

	return nil
}

// List.
type cmdImageList struct {
	global *cmdGlobal
	image  *cmdImage

	flagFormat      string
	flagColumns     string
	flagAllProjects bool
}

func (c *cmdImageList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:] [<filter>...]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List images"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

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
    e - Project
    a - Architecture
    s - Size
    u - Upload date
    t - Type`)

	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultImagesColumns, cli.FormatStringFlagLabel("Columns"))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, "Display images from all projects")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpRemotes(toComplete, ":", false, imageServerRemoteCompletionFilters(*c.global.conf)...)
	}

	return cmd
}

const defaultImagesColumns = "lfpdatsu"
const defaultImagesColumnsAllProjects = "elfpdatsu"

func (c *cmdImageList) parseColumns() ([]imageColumn, error) {
	columnsShorthandMap := map[rune]imageColumn{
		'a': {"ARCHITECTURE", c.architectureColumnData},
		'd': {"DESCRIPTION", c.descriptionColumnData},
		'e': {"PROJECT", c.projectColumnData},
		'f': {"FINGERPRINT", c.fingerprintColumnData},
		'F': {"FINGERPRINT", c.fingerprintFullColumnData},
		'l': {"ALIAS", c.aliasColumnData},
		'L': {"ALIASES", c.aliasesColumnData},
		'p': {"PUBLIC", c.publicColumnData},
		's': {"SIZE", c.sizeColumnData},
		't': {"TYPE", c.typeColumnData},
		'u': {"UPLOAD DATE", c.uploadDateColumnData},
	}

	// Add project column if --all-projects flag specified and custom columns are not specified.
	if c.flagAllProjects && c.flagColumns == defaultImagesColumns {
		c.flagColumns = defaultImagesColumnsAllProjects
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []imageColumn{}

	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf("Empty column entry (redundant, leading or trailing command) in %q", c.flagColumns)
		}

		for _, columnRune := range columnEntry {
			column, ok := columnsShorthandMap[columnRune]
			if !ok {
				return nil, fmt.Errorf("Unknown column shorthand char '%c' in %q", columnRune, columnEntry)
			}

			columns = append(columns, column)
		}
	}

	return columns, nil
}

func (c *cmdImageList) aliasColumnData(image api.Image) string {
	shortest := c.shortestAlias(image.Aliases)
	if len(image.Aliases) > 1 {
		shortest = fmt.Sprintf("%s (%d more)", shortest, len(image.Aliases)-1)
	}

	return shortest
}

func (c *cmdImageList) aliasesColumnData(image api.Image) string {
	aliases := make([]string, 0, len(image.Aliases))
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
		return "yes"
	}

	return "no"
}

func (c *cmdImageList) descriptionColumnData(image api.Image) string {
	return c.findDescription(image.Properties)
}

func (c *cmdImageList) projectColumnData(image api.Image) string {
	return image.Project
}

func (c *cmdImageList) architectureColumnData(image api.Image) string {
	return image.Architecture
}

func (c *cmdImageList) sizeColumnData(image api.Image) string {
	return fmt.Sprintf("%.2fMiB", float64(image.Size)/1024.0/1024.0)
}

func (c *cmdImageList) typeColumnData(image api.Image) string {
	if image.Type == "" {
		return "CONTAINER"
	}

	return strings.ToUpper(image.Type)
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

	m := structToMap(state)

	for _, filter := range filters {
		found := false
		if strings.Contains(filter, "=") {
			key, value, _ := strings.Cut(filter, "=")

			for configKey, configValue := range state.Properties {
				list := cmdList{}
				list.global = c.global
				if list.dotPrefixMatch(key, configKey) {
					// Try to test filter value as a regexp.
					regexpValue := value
					if !strings.Contains(value, "^") && !strings.Contains(value, "$") {
						regexpValue = "^" + regexpValue + "$"
					}

					r, err := regexp.Compile(regexpValue)
					// If not regexp compatible use original value.
					if err != nil {
						if value == configValue {
							found = true
							break
						}
					} else if r.MatchString(configValue) {
						found = true
						break
					}
				}
			}

			val, ok := m[key]
			if ok && fmt.Sprint(val) == value {
				found = true
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

func (c *cmdImageList) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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

	serverFilters, clientFilters := getServerSupportedFilters(filters, api.Image{})

	var allImages []api.Image
	if c.flagAllProjects {
		instanceServer, ok := remoteServer.(lxd.InstanceServer)
		if !ok {
			return errors.New("--all-projects flag is not supported for this server")
		}

		allImages, err = instanceServer.GetImagesAllProjectsWithFilter(serverFilters)
		if err != nil {
			allImages, err = instanceServer.GetImagesAllProjects()
			if err != nil {
				return err
			}

			clientFilters = filters
		}
	} else {
		allImages, err = remoteServer.GetImagesWithFilter(serverFilters)
		if err != nil {
			allImages, err = remoteServer.GetImages()
			if err != nil {
				return err
			}

			clientFilters = filters
		}
	}

	images := make([]api.Image, 0, len(allImages))
	for _, image := range allImages {
		if !c.imageShouldShow(clientFilters, &image) {
			continue
		}

		images = append(images, image)
	}

	// Render the table
	data := [][]string{}
	for _, image := range images {
		if !c.imageShouldShow(clientFilters, &image) {
			continue
		}

		row := []string{}
		for _, column := range columns {
			row = append(row, column.Data(image))
		}

		data = append(data, row)
	}

	sort.Sort(cli.StringList(data))

	rawData := make([]*api.Image, len(images))
	for i := range images {
		rawData[i] = &images[i]
	}

	headers := []string{}
	for _, column := range columns {
		headers = append(headers, column.Name)
	}

	return cli.RenderTable(c.flagFormat, headers, data, rawData)
}

// Refresh.
type cmdImageRefresh struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageRefresh) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("refresh", "[<remote>:]<image> [[<remote>:]<image>...]")
	cmd.Short = "Refresh images"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete, false)
		}

		return c.global.cmpImages(toComplete, true)
	}

	return cmd
}

func (c *cmdImageRefresh) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
			return errors.New("Image identifier missing")
		}

		image, _, err := c.image.dereferenceAlias(resource.server, "", resource.name)
		if err != nil {
			return err
		}

		progress := cli.ProgressRenderer{
			Format: "Refreshing the image: %s",
			Quiet:  c.global.flagQuiet,
		}

		op, err := resource.server.RefreshImage(image.Fingerprint)
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
			refreshed, ok = flag.(bool)
			if !ok {
				return fmt.Errorf(`Invalid type %T for "refreshed" key in operation metadata`, flag)
			}
		}

		if refreshed {
			progress.Done("Image refreshed successfully!")
		} else {
			progress.Done("Image already up to date.")
		}
	}

	return nil
}

// Show.
type cmdImageShow struct {
	global *cmdGlobal
	image  *cmdImage

	flagVM bool
}

func (c *cmdImageShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", "[<remote>:]<image>")
	cmd.Short = "Show image properties"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.Flags().BoolVar(&c.flagVM, "vm", false, "Query virtual machine images")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageShow) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
	imageType := ""
	if c.flagVM {
		imageType = "virtual-machine"
	}

	image, _, err := c.image.dereferenceAlias(remoteServer, imageType, name)
	if err != nil {
		return err
	}

	properties := image.Writable()
	data, err := yaml.Marshal(&properties)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

type cmdImageGetProp struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageGetProp) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get-property", "[<remote>:]<image> <key>")
	cmd.Short = "Get image property"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete, false)
		}

		if len(args) == 1 {
			// individual image prop could complete here
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageGetProp) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
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

	// Get the corresponding property
	image, _, err := c.image.dereferenceAlias(remoteServer, "", name)
	if err != nil {
		return err
	}

	prop, propFound := image.Properties[args[1]]
	if !propFound {
		return errors.New("Property not found")
	}

	fmt.Println(prop)

	return nil
}

type cmdImageSetProp struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageSetProp) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set-property", "[<remote>:]<image> <key> <value>")
	cmd.Short = "Set image property"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete, true)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageSetProp) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
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
		return fmt.Errorf("Image identifier missing: %s", args[0])
	}

	// Show properties
	image, etag, err := c.image.dereferenceAlias(resource.server, "", resource.name)
	if err != nil {
		return err
	}

	properties := image.Writable()
	properties.Properties[args[1]] = args[2]

	// Update image
	err = resource.server.UpdateImage(image.Fingerprint, properties, etag)
	if err != nil {
		return err
	}

	return nil
}

type cmdImageUnsetProp struct {
	global       *cmdGlobal
	image        *cmdImage
	imageSetProp *cmdImageSetProp
}

func (c *cmdImageUnsetProp) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset-property", "[<remote>:]<image> <key>")
	cmd.Short = "Unset image property"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpImages(toComplete, true)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageUnsetProp) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	args = append(args, "")
	return c.imageSetProp.run(cmd, args)
}

func structToMap(data any) map[string]any {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil
	}

	mapData := make(map[string]any)

	err = json.Unmarshal(dataBytes, &mapData)
	if err != nil {
		return nil
	}

	return mapData
}
