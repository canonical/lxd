package main

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdExport struct {
	global *cmdGlobal

	flagInstanceOnly         bool
	flagOptimizedStorage     bool
	flagCompressionAlgorithm string
}

func (c *cmdExport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("export", i18n.G("[<remote>:]<instance> [target] [--instance-only] [--optimized-storage]"))
	cmd.Short = i18n.G("Export instance backups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Export instances as backup tarballs.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc export u1 backup0.tar.gz
    Download a backup tarball of the u1 instance.`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagInstanceOnly, "instance-only", false,
		i18n.G("Whether or not to only backup the instance (without snapshots)"))
	cmd.Flags().BoolVar(&c.flagOptimizedStorage, "optimized-storage", false,
		i18n.G("Use storage driver optimized format (can only be restored on a similar pool)"))
	cmd.Flags().StringVar(&c.flagCompressionAlgorithm, "compression", "", i18n.G("Compression algorithm to use (`none` for uncompressed)"))

	return cmd
}

func (c *cmdExport) Run(cmd *cobra.Command, args []string) error {
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

	op, err := d.CreateInstanceBackup(name, req)
	if err != nil {
		return errors.Wrap(err, "Create instance backup")
	}

	// Watch the background operation
	progress := utils.ProgressRenderer{
		Format: i18n.G("Backing up instance: %s"),
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait until backup is done
	err = utils.CancelableWait(op, &progress)
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
	backupName := strings.TrimPrefix(op.Get().Resources["backups"][0],
		"/1.0/backups/")

	defer func() {
		// Delete backup after we're done
		op, err = d.DeleteInstanceBackup(name, backupName)
		if err == nil {
			op.Wait()
		}
	}()

	var targetName string
	if len(args) > 1 {
		targetName = args[1]
	} else {
		targetName = "backup.tar.gz"
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
		defer target.Close()
	}

	// Prepare the download request
	progress = utils.ProgressRenderer{
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
		os.Remove(targetName)
		progress.Done("")
		return errors.Wrap(err, "Fetch instance backup file")
	}

	progress.Done(i18n.G("Backup exported successfully!"))
	return nil
}
