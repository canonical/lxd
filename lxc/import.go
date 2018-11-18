package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/ioprogress"
)

type cmdImport struct {
	global *cmdGlobal
}

func (c *cmdImport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("import [<remote>:] <backup file>")
	cmd.Short = i18n.G("Import container backups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Import backups of containers including their snapshots.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc import backup0.tar.gz
    Create a new container using backup0.tar.gz as the source.`))

	cmd.RunE = c.Run

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

	file, err := os.Open(shared.HostPath(args[len(args)-1]))
	if err != nil {
		return err
	}
	defer file.Close()

	fstat, err := file.Stat()
	if err != nil {
		return err
	}

	progress := utils.ProgressRenderer{
		Format: i18n.G("Importing container: %s"),
		Quiet:  c.global.flagQuiet,
	}

	createArgs := lxd.ContainerBackupArgs{}
	createArgs.BackupFile = &ioprogress.ProgressReader{
		ReadCloser: file,
		Tracker: &ioprogress.ProgressTracker{
			Length: fstat.Size(),
			Handler: func(percent int64, speed int64) {
				progress.UpdateProgress(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, shared.GetByteSizeString(speed, 2))})
			},
		},
	}

	op, err := resource.server.CreateContainerFromBackup(createArgs)
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
