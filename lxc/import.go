package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/units"
)

type cmdImport struct {
	global *cmdGlobal

	flagStorage string
}

func (c *cmdImport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("import", i18n.G("[<remote>:] <backup file>"))
	cmd.Short = i18n.G("Import instance backups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Import backups of instances including their snapshots.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc import backup0.tar.gz
    Create a new instance using backup0.tar.gz as the source.`))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", i18n.G("Storage pool name")+"``")

	return cmd
}

func (c *cmdImport) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 1 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	file, err := os.Open(shared.HostPathFollow(args[len(args)-1]))
	if err != nil {
		return err
	}
	defer file.Close()

	fstat, err := file.Stat()
	if err != nil {
		return err
	}

	progress := utils.ProgressRenderer{
		Format: i18n.G("Importing instance: %s"),
		Quiet:  c.global.flagQuiet,
	}

	createArgs := lxd.InstanceBackupArgs{
		BackupFile: &ioprogress.ProgressReader{
			ReadCloser: file,
			Tracker: &ioprogress.ProgressTracker{
				Length: fstat.Size(),
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2))})
				},
			},
		},
		PoolName: c.flagStorage,
	}

	op, err := resource.server.CreateInstanceFromBackup(createArgs)
	if err != nil {
		return err
	}

	// Wait for operation to finish
	err = utils.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	return nil
}
