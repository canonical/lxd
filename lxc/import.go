package main

import (
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/units"
)

type cmdImport struct {
	global *cmdGlobal

	flagStorage string
	flagDevice  []string
}

func (c *cmdImport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("import", "[<remote>:] <backup file> [<instance name>]")
	cmd.Short = "Import instance backups"
	cmd.Long = cli.FormatSection("Description", `Import backups of instances including their snapshots.`)
	cmd.Example = cli.FormatSection("", `lxc import backup0.tar.gz
    Create a new instance using backup0.tar.gz as the source.`)

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", cli.FormatStringFlagLabel("Storage pool name"))
	cmd.Flags().StringArrayVarP(&c.flagDevice, "device", "d", nil, cli.FormatStringFlagLabel("New key/value to apply to a specific device"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		files, directive := c.global.cmpLocalFiles(toComplete, []string{".tar.gz", ".tar.xz"})
		if len(args) == 0 {
			remotes, _ := c.global.cmpRemotes(toComplete, ":", false, instanceServerRemoteCompletionFilters(*c.global.conf)...)
			return append(files, remotes...), directive
		}

		return files, directive
	}

	return cmd
}

func (c *cmdImport) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 3)
	if exit {
		return err
	}

	srcFilePosition := 0

	// Parse remote (identify 1st argument is remote by looking for a colon at the end).
	remote := ""
	if len(args) > 1 && strings.HasSuffix(args[0], ":") {
		remote = args[0]
		srcFilePosition = 1
	}

	// Parse source file (this could be 1st or 2nd argument depending on whether a remote is specified first).
	srcFile := ""
	if len(args) >= srcFilePosition+1 {
		srcFile = args[srcFilePosition]
	}

	// Parse instance name.
	instanceName := ""
	if len(args) >= srcFilePosition+2 {
		instanceName = args[srcFilePosition+1]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	var file *os.File
	if srcFile == "-" {
		file = os.Stdin
		c.global.flagQuiet = true
	} else {
		file, err = os.Open(shared.HostPathFollow(srcFile))
		if err != nil {
			return err
		}

		defer func() { _ = file.Close() }()
	}

	fstat, err := file.Stat()
	if err != nil {
		return err
	}

	progress := cli.ProgressRenderer{
		Format: "Importing instance: %s",
		Quiet:  c.global.flagQuiet,
	}

	deviceMap, err := parseDeviceOverrides(c.flagDevice)
	if err != nil {
		return err
	}

	createArgs := lxd.InstanceBackupArgs{
		BackupFile: &ioprogress.ProgressReader{
			ReadCloser: file,
			Tracker: &ioprogress.ProgressTracker{
				Length: fstat.Size(),
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{Text: strconv.FormatInt(percent, 10) + "% (" + units.GetByteSizeString(speed, 2) + "/s)"})
				},
			},
		},
		PoolName: c.flagStorage,
		Name:     instanceName,
		Devices:  deviceMap,
	}

	op, err := resource.server.CreateInstanceFromBackup(createArgs)
	if err != nil {
		return err
	}

	// Wait for operation to finish.
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	return nil
}
