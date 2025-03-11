package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdExport struct {
	global *cmdGlobal

	flagInstanceOnly         bool
	flagOptimizedStorage     bool
	flagCompressionAlgorithm string
	flagExportVersion        string
}

func (c *cmdExport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("export", i18n.G("[<remote>:]<instance> [target] [--instance-only] [--optimized-storage]"))
	cmd.Short = i18n.G("Export instance backups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Export instances as backup tarballs.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc export u1 backup0.tar.gz
    Download a backup tarball of the u1 instance.`))

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagInstanceOnly, "instance-only", false,
		i18n.G("Whether or not to only backup the instance (without snapshots)"))
	cmd.Flags().BoolVar(&c.flagOptimizedStorage, "optimized-storage", false,
		i18n.G("Use storage driver optimized format (can only be restored on a similar pool)"))
	cmd.Flags().StringVar(&c.flagCompressionAlgorithm, "compression", "", i18n.G("Compression algorithm to use (none for uncompressed)")+"``")
	cmd.Flags().StringVar(&c.flagExportVersion, "export-version", "",
		i18n.G("Use a different metadata format version than the latest one supported by the server")+"``")

	return cmd
}

func (c *cmdExport) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Connect to LXD
	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	instanceOnly := c.flagInstanceOnly

	req := api.InstanceBackupsPost{
		Name:                 "",
		ExpiresAt:            time.Now().Add(24 * time.Hour),
		ContainerOnly:        instanceOnly,
		InstanceOnly:         instanceOnly,
		OptimizedStorage:     c.flagOptimizedStorage,
		CompressionAlgorithm: c.flagCompressionAlgorithm,
	}

	backupVersionSupported := d.HasExtension("backup_metadata_version")

	// Don't allow explicitly setting 0 as it will implicitly create a backup using version 1.
	if c.flagExportVersion == "0" {
		return fmt.Errorf(i18n.G("Invalid export version %q"), "0")
	}

	// In case no version is set, default to 0 so we can convert it to an uint32.
	if c.flagExportVersion == "" {
		c.flagExportVersion = "0"
	}

	versionUint, err := strconv.ParseUint(c.flagExportVersion, 10, 32)
	if err != nil {
		return fmt.Errorf(i18n.G("Invalid export version %q: %w"), c.flagExportVersion, err)
	}

	versionUint32 := uint32(versionUint)

	// If the server supports setting the backup version, set the selected version.
	// If supported but the version is not set, the server picks its default version.
	// If unsupported but the version is set to 1, the field isn't set so its up to the server to pick the old version.
	if backupVersionSupported {
		if versionUint32 != 0 {
			req.Version = versionUint32
		}
	} else if !backupVersionSupported && versionUint32 > api.BackupMetadataVersion1 {
		// Any version beyond 1 isn't supported by an older server without the backup_metadata_version extension.
		return errors.New(i18n.G("The server doesn't support setting the metadata format version"))
	}

	op, err := d.CreateInstanceBackup(name, req)
	if err != nil {
		return fmt.Errorf("Create instance backup: %w", err)
	}

	var targetName string
	if len(args) > 1 {
		targetName = args[1]
	} else {
		targetName = name + ".backup"
	}

	var target *os.File
	if targetName == "-" {
		target = os.Stdout
		c.global.flagQuiet = true
	} else {
		target, err = os.Create(shared.HostPathFollow(targetName))
		if err != nil {
			return err
		}

		defer func() { _ = target.Close() }()
	}

	// Watch the background operation
	progress := cli.ProgressRenderer{
		Format: i18n.G("Backing up instance: %s"),
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait until backup is done
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	err = op.Wait()
	if err != nil {
		return err
	}

	// Get name of backup
	uStr := op.Get().Resources["backups"][0]
	u, err := url.Parse(uStr)
	if err != nil {
		return fmt.Errorf("Invalid URL %q: %w", uStr, err)
	}

	backupName, err := url.PathUnescape(path.Base(u.EscapedPath()))
	if err != nil {
		return fmt.Errorf("Invalid backup name segment in path %q: %w", u.EscapedPath(), err)
	}

	defer func() {
		// Delete backup after we're done
		op, err = d.DeleteInstanceBackup(name, backupName)
		if err == nil {
			_ = op.Wait()
		}
	}()

	// Prepare the download request
	progress = cli.ProgressRenderer{
		Format: i18n.G("Exporting the backup: %s"),
		Quiet:  c.global.flagQuiet,
	}

	backupFileRequest := lxd.BackupFileRequest{
		BackupFile:      io.WriteSeeker(target),
		ProgressHandler: progress.UpdateProgress,
	}

	// Export tarball
	_, err = d.GetInstanceBackupFile(name, backupName, &backupFileRequest)
	if err != nil {
		_ = os.Remove(targetName)
		progress.Done("")
		return fmt.Errorf("Fetch instance backup file: %w", err)
	}

	// Detect backup file type and rename file accordingly
	if len(args) <= 1 {
		_, err := target.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}

		_, ext, _, err := shared.DetectCompressionFile(target)
		if err != nil {
			return err
		}

		err = os.Rename(shared.HostPathFollow(targetName), shared.HostPathFollow(name+ext))
		if err != nil {
			return fmt.Errorf("Failed to rename export file: %w", err)
		}
	}

	err = target.Close()
	if err != nil {
		return fmt.Errorf("Failed to close export file: %w", err)
	}

	progress.Done(i18n.G("Backup exported successfully!"))
	return nil
}
