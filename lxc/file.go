package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/termios"
	"github.com/lxc/lxd/shared/units"
)

// DirMode represents the file mode for creating dirs on `lxc file pull/push`.
const DirMode = 0755

type cmdFile struct {
	global *cmdGlobal

	flagUID  int
	flagGID  int
	flagMode string

	flagMkdir     bool
	flagRecursive bool
}

func fileGetWrapper(server lxd.InstanceServer, inst string, path string) (buf io.ReadCloser, resp *lxd.InstanceFileResponse, err error) {
	// Signal handling
	chSignal := make(chan os.Signal, 1)
	signal.Notify(chSignal, os.Interrupt)

	// Operation handling
	chDone := make(chan bool)
	go func() {
		buf, resp, err = server.GetInstanceFile(inst, path)
		close(chDone)
	}()

	count := 0
	for {
		select {
		case <-chDone:
			return buf, resp, err
		case <-chSignal:
			count++

			if count == 3 {
				return nil, nil, fmt.Errorf(i18n.G("User signaled us three times, exiting. The remote operation will keep running"))
			}

			fmt.Println(i18n.G("Early server side processing of file transfer requests cannot be canceled (interrupt two more times to force)"))
		}
	}
}

func (c *cmdFile) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("file")
	cmd.Short = i18n.G("Manage files in instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage files in instances`))

	// Delete
	fileDeleteCmd := cmdFileDelete{global: c.global, file: c}
	cmd.AddCommand(fileDeleteCmd.Command())

	// Pull
	filePullCmd := cmdFilePull{global: c.global, file: c}
	cmd.AddCommand(filePullCmd.Command())

	// Push
	filePushCmd := cmdFilePush{global: c.global, file: c}
	cmd.AddCommand(filePushCmd.Command())

	// Edit
	fileEditCmd := cmdFileEdit{global: c.global, file: c, filePull: &filePullCmd, filePush: &filePushCmd}
	cmd.AddCommand(fileEditCmd.Command())

	// Mount
	fileMountCmd := cmdFileMount{global: c.global, file: c}
	cmd.AddCommand(fileMountCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Delete.
type cmdFileDelete struct {
	global *cmdGlobal
	file   *cmdFile
}

func (c *cmdFileDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<instance>/<path> [[<remote>:]<instance>/<path>...]"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete files in instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete files in instances`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdFileDelete) Run(cmd *cobra.Command, args []string) error {
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
		pathSpec := strings.SplitN(resource.name, "/", 2)
		if len(pathSpec) != 2 {
			return fmt.Errorf(i18n.G("Invalid path %s"), resource.name)
		}

		// Delete the file
		err = resource.server.DeleteInstanceFile(pathSpec[0], pathSpec[1])
		if err != nil {
			return err
		}
	}

	return nil
}

// Edit.
type cmdFileEdit struct {
	global   *cmdGlobal
	file     *cmdFile
	filePull *cmdFilePull
	filePush *cmdFilePush
}

func (c *cmdFileEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<instance>/<path>"))
	cmd.Short = i18n.G("Edit files in instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit files in instances`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdFileEdit) Run(cmd *cobra.Command, args []string) error {
	c.filePush.noModeChange = true

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		return c.filePush.Run(cmd, append([]string{os.Stdin.Name()}, args[0]))
	}

	// Create temp file
	f, err := os.CreateTemp("", "lxd_file_edit_")
	if err != nil {
		return fmt.Errorf(i18n.G("Unable to create a temporary file: %v"), err)
	}

	fname := f.Name()
	_ = f.Close()
	_ = os.Remove(fname)

	// Tell pull/push that they're called from edit.
	c.filePull.edit = true
	c.filePush.edit = true

	// Extract current value
	defer func() { _ = os.Remove(fname) }()
	err = c.filePull.Run(cmd, append([]string{args[0]}, fname))
	if err != nil {
		return err
	}

	// Spawn the editor
	_, err = shared.TextEditor(fname, []byte{})
	if err != nil {
		return err
	}

	// Push the result
	err = c.filePush.Run(cmd, append([]string{fname}, args[0]))
	if err != nil {
		return err
	}

	return nil
}

// Pull.
type cmdFilePull struct {
	global *cmdGlobal
	file   *cmdFile

	edit bool
}

func (c *cmdFilePull) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("pull", i18n.G("[<remote>:]<instance>/<path> [[<remote>:]<instance>/<path>...] <target path>"))
	cmd.Short = i18n.G("Pull files from instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Pull files from instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc file pull foo/etc/hosts .
   To pull /etc/hosts from the instance and write it to the current directory.`))

	cmd.Flags().BoolVarP(&c.file.flagMkdir, "create-dirs", "p", false, i18n.G("Create any directories necessary"))
	cmd.Flags().BoolVarP(&c.file.flagRecursive, "recursive", "r", false, i18n.G("Recursively transfer files"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdFilePull) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Determine the target
	target := filepath.Clean(args[len(args)-1])
	if !c.edit {
		target = shared.HostPathFollow(target)
	}

	targetIsDir := false
	sb, err := os.Stat(target)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	/*
	 * If the path exists, just use it. If it doesn't exist, it might be a
	 * directory in one of three cases:
	 *   1. Someone explicitly put "/" at the end
	 *   2. Someone provided more than one source. In this case the target
	 *      should be a directory so we can save all the files into it.
	 *   3. We are dealing with recursive copy
	 */
	if err == nil {
		targetIsDir = sb.IsDir()
		if !targetIsDir && len(args)-1 > 1 {
			return fmt.Errorf(i18n.G("More than one file to download, but target is not a directory"))
		}
	} else if strings.HasSuffix(args[len(args)-1], string(os.PathSeparator)) || len(args)-1 > 1 {
		err := os.MkdirAll(target, DirMode)
		if err != nil {
			return err
		}

		targetIsDir = true
	} else if c.file.flagMkdir {
		err := os.MkdirAll(filepath.Dir(target), DirMode)
		if err != nil {
			return err
		}
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[:len(args)-1]...)
	if err != nil {
		return err
	}

	for _, resource := range resources {
		pathSpec := strings.SplitN(resource.name, "/", 2)
		if len(pathSpec) != 2 {
			return fmt.Errorf(i18n.G("Invalid source %s"), resource.name)
		}

		buf, resp, err := fileGetWrapper(resource.server, pathSpec[0], pathSpec[1])
		if err != nil {
			return err
		}

		// Deal with recursion
		if resp.Type == "directory" {
			if c.file.flagRecursive {
				if !shared.PathExists(target) {
					err := os.MkdirAll(target, DirMode)
					if err != nil {
						return err
					}

					targetIsDir = true
				}

				err := c.file.recursivePullFile(resource.server, pathSpec[0], pathSpec[1], target)
				if err != nil {
					return err
				}

				continue
			} else {
				return fmt.Errorf(i18n.G("Can't pull a directory without --recursive"))
			}
		}

		var targetPath string
		if targetIsDir {
			targetPath = path.Join(target, path.Base(pathSpec[1]))
		} else {
			targetPath = target
		}

		logger.Infof("Pulling %s from %s (%s)", targetPath, pathSpec[1], resp.Type)

		if resp.Type == "symlink" {
			linkTarget, err := io.ReadAll(buf)
			if err != nil {
				return err
			}

			// Follow the symlink
			if targetPath == "-" || c.file.flagRecursive {
				i := 0
				for {
					newPath := strings.TrimSuffix(string(linkTarget), "\n")
					if !strings.HasPrefix(newPath, "/") {
						newPath = filepath.Clean(filepath.Join(filepath.Dir(pathSpec[1]), newPath))
					}

					buf, resp, err = resource.server.GetInstanceFile(pathSpec[0], newPath)
					if err != nil {
						return err
					}

					if resp.Type != "symlink" {
						break
					}

					i++
					if i > 255 {
						return fmt.Errorf("Too many links")
					}

					// Update link target for next iteration.
					linkTarget, err = io.ReadAll(buf)
					if err != nil {
						return err
					}
				}
			} else {
				err = os.Symlink(strings.TrimSpace(string(linkTarget)), targetPath)
				if err != nil {
					return err
				}

				continue
			}
		}

		var f *os.File
		if targetPath == "-" {
			f = os.Stdout
		} else {
			f, err = os.Create(targetPath)
			if err != nil {
				return err
			}

			defer func() { _ = f.Close() }()

			err = os.Chmod(targetPath, os.FileMode(resp.Mode))
			if err != nil {
				return err
			}
		}

		progress := cli.ProgressRenderer{
			Format: fmt.Sprintf(i18n.G("Pulling %s from %s: %%s"), targetPath, pathSpec[1]),
			Quiet:  c.global.flagQuiet,
		}

		writer := &ioprogress.ProgressWriter{
			WriteCloser: f,
			Tracker: &ioprogress.ProgressTracker{
				Handler: func(bytesReceived int64, speed int64) {
					if targetPath == "-" {
						return
					}

					progress.UpdateProgress(ioprogress.ProgressData{
						Text: fmt.Sprintf("%s (%s/s)",
							units.GetByteSizeString(bytesReceived, 2),
							units.GetByteSizeString(speed, 2))})
				},
			},
		}

		_, err = io.Copy(writer, buf)
		if err != nil {
			progress.Done("")
			return err
		}

		err = f.Close()
		if err != nil {
			progress.Done("")
			return err
		}

		progress.Done("")
	}

	return nil
}

// Push.
type cmdFilePush struct {
	global *cmdGlobal
	file   *cmdFile

	edit         bool
	noModeChange bool
}

func (c *cmdFilePush) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("push", i18n.G("<source path>... [<remote>:]<instance>/<path>"))
	cmd.Short = i18n.G("Push files into instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Push files into instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc file push /etc/hosts foo/etc/hosts
   To push /etc/hosts into the instance "foo".`))

	cmd.Flags().BoolVarP(&c.file.flagRecursive, "recursive", "r", false, i18n.G("Recursively transfer files"))
	cmd.Flags().BoolVarP(&c.file.flagMkdir, "create-dirs", "p", false, i18n.G("Create any directories necessary"))
	cmd.Flags().IntVar(&c.file.flagUID, "uid", -1, i18n.G("Set the file's uid on push")+"``")
	cmd.Flags().IntVar(&c.file.flagGID, "gid", -1, i18n.G("Set the file's gid on push")+"``")
	cmd.Flags().StringVar(&c.file.flagMode, "mode", "", i18n.G("Set the file's perms on push")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdFilePush) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse the destination
	target := args[len(args)-1]
	pathSpec := strings.SplitN(target, "/", 2)

	if len(pathSpec) != 2 {
		return fmt.Errorf(i18n.G("Invalid target %s"), target)
	}

	targetIsDir := strings.HasSuffix(target, "/")
	// re-add leading / that got stripped by the SplitN
	targetPath := "/" + pathSpec[1]
	// clean various /./, /../, /////, etc. that users add (#2557)
	targetPath = path.Clean(targetPath)

	// normalization may reveal that path is still a dir, e.g. /.
	if strings.HasSuffix(targetPath, "/") {
		targetIsDir = true
	}

	// Parse remote
	resources, err := c.global.ParseServers(pathSpec[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Make a list of paths to transfer
	sourcefilenames := []string{}
	for _, fname := range args[:len(args)-1] {
		if !c.edit {
			sourcefilenames = append(sourcefilenames, shared.HostPathFollow(filepath.Clean(fname)))
		} else {
			sourcefilenames = append(sourcefilenames, filepath.Clean(fname))
		}
	}

	// Determine the target mode
	mode := os.FileMode(DirMode)
	if c.file.flagMode != "" {
		if len(c.file.flagMode) == 3 {
			c.file.flagMode = "0" + c.file.flagMode
		}

		m, err := strconv.ParseInt(c.file.flagMode, 0, 0)
		if err != nil {
			return err
		}

		mode = os.FileMode(m)
	}

	// Recursive calls
	if c.file.flagRecursive {
		// Quick checks.
		if c.file.flagUID != -1 || c.file.flagGID != -1 || c.file.flagMode != "" {
			return fmt.Errorf(i18n.G("Can't supply uid/gid/mode in recursive mode"))
		}

		// Create needed paths if requested
		if c.file.flagMkdir {
			f, err := os.Open(sourcefilenames[0])
			if err != nil {
				return err
			}

			finfo, err := f.Stat()
			_ = f.Close()
			if err != nil {
				return err
			}

			mode, uid, gid := shared.GetOwnerMode(finfo)

			err = c.file.recursiveMkdir(resource.server, resource.name, targetPath, &mode, int64(uid), int64(gid))
			if err != nil {
				return err
			}
		}

		// Transfer the files
		for _, fname := range sourcefilenames {
			err := c.file.recursivePushFile(resource.server, resource.name, fname, targetPath)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Determine the target uid
	uid := 0
	if c.file.flagUID >= 0 {
		uid = c.file.flagUID
	}

	// Determine the target gid
	gid := 0
	if c.file.flagGID >= 0 {
		gid = c.file.flagGID
	}

	if (len(sourcefilenames) > 1) && !targetIsDir {
		return fmt.Errorf(i18n.G("Missing target directory"))
	}

	// Make sure all of the files are accessible by us before trying to push any of them
	var files []*os.File
	for _, f := range sourcefilenames {
		var file *os.File
		if f == "-" {
			file = os.Stdin
		} else {
			file, err = os.Open(f)
			if err != nil {
				return err
			}
		}

		defer func() { _ = file.Close() }()
		files = append(files, file)
	}

	// Push the files
	for _, f := range files {
		fpath := targetPath
		if targetIsDir {
			fpath = path.Join(fpath, path.Base(f.Name()))
		}

		// Create needed paths if requested
		if c.file.flagMkdir {
			finfo, err := f.Stat()
			if err != nil {
				return err
			}

			_, dUID, dGID := shared.GetOwnerMode(finfo)
			if c.file.flagUID == -1 || c.file.flagGID == -1 {
				if c.file.flagUID == -1 {
					uid = dUID
				}

				if c.file.flagGID == -1 {
					gid = dGID
				}
			}

			err = c.file.recursiveMkdir(resource.server, resource.name, path.Dir(fpath), nil, int64(uid), int64(gid))
			if err != nil {
				return err
			}
		}

		// Transfer the files
		args := lxd.InstanceFileArgs{
			UID:  -1,
			GID:  -1,
			Mode: -1,
		}

		if !c.noModeChange {
			if c.file.flagMode == "" || c.file.flagUID == -1 || c.file.flagGID == -1 {
				finfo, err := f.Stat()
				if err != nil {
					return err
				}

				fMode, fUID, fGID := shared.GetOwnerMode(finfo)
				if err != nil {
					return err
				}

				if c.file.flagMode == "" {
					mode = fMode
				}

				if c.file.flagUID == -1 {
					uid = fUID
				}

				if c.file.flagGID == -1 {
					gid = fGID
				}
			}

			args.UID = int64(uid)
			args.GID = int64(gid)
			args.Mode = int(mode.Perm())
		}

		args.Type = "file"

		fstat, err := f.Stat()
		if err != nil {
			return err
		}

		progress := cli.ProgressRenderer{
			Format: fmt.Sprintf(i18n.G("Pushing %s to %s: %%s"), f.Name(), fpath),
			Quiet:  c.global.flagQuiet,
		}

		args.Content = shared.NewReadSeeker(&ioprogress.ProgressReader{
			ReadCloser: f,
			Tracker: &ioprogress.ProgressTracker{
				Length: fstat.Size(),
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{
						Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2)),
					})
				},
			},
		}, f)

		logger.Infof("Pushing %s to %s (%s)", f.Name(), fpath, args.Type)
		err = resource.server.CreateInstanceFile(resource.name, fpath, args)
		if err != nil {
			progress.Done("")
			return err
		}

		progress.Done("")
	}

	return nil
}

func (c *cmdFile) recursivePullFile(d lxd.InstanceServer, inst string, p string, targetDir string) error {
	buf, resp, err := d.GetInstanceFile(inst, p)
	if err != nil {
		return err
	}

	target := filepath.Join(targetDir, filepath.Base(p))
	logger.Infof("Pulling %s from %s (%s)", target, p, resp.Type)

	if resp.Type == "directory" {
		err := os.Mkdir(target, os.FileMode(resp.Mode))
		if err != nil {
			return err
		}

		for _, ent := range resp.Entries {
			nextP := path.Join(p, ent)

			err := c.recursivePullFile(d, inst, nextP, target)
			if err != nil {
				return err
			}
		}
	} else if resp.Type == "file" {
		f, err := os.Create(target)
		if err != nil {
			return err
		}

		defer func() { _ = f.Close() }()

		err = os.Chmod(target, os.FileMode(resp.Mode))
		if err != nil {
			return err
		}

		progress := cli.ProgressRenderer{
			Format: fmt.Sprintf(i18n.G("Pulling %s from %s: %%s"), p, target),
			Quiet:  c.global.flagQuiet,
		}

		writer := &ioprogress.ProgressWriter{
			WriteCloser: f,
			Tracker: &ioprogress.ProgressTracker{
				Handler: func(bytesReceived int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{
						Text: fmt.Sprintf("%s (%s/s)",
							units.GetByteSizeString(bytesReceived, 2),
							units.GetByteSizeString(speed, 2))})
				},
			},
		}

		_, err = io.Copy(writer, buf)
		if err != nil {
			progress.Done("")
			return err
		}

		err = f.Close()
		if err != nil {
			progress.Done("")
			return err
		}

		progress.Done("")
	} else if resp.Type == "symlink" {
		linkTarget, err := io.ReadAll(buf)
		if err != nil {
			return err
		}

		err = os.Symlink(strings.TrimSpace(string(linkTarget)), target)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf(i18n.G("Unknown file type '%s'"), resp.Type)
	}

	return nil
}

func (c *cmdFile) recursivePushFile(d lxd.InstanceServer, inst string, source string, target string) error {
	source = filepath.Clean(source)
	sourceDir, _ := filepath.Split(source)
	sourceLen := len(sourceDir)

	sendFile := func(p string, fInfo os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to walk path for %s: %s"), p, err)
		}

		// Detect unsupported files
		if !fInfo.Mode().IsRegular() && !fInfo.Mode().IsDir() && fInfo.Mode()&os.ModeSymlink != os.ModeSymlink {
			return fmt.Errorf(i18n.G("'%s' isn't a supported file type"), p)
		}

		// Prepare for file transfer
		targetPath := path.Join(target, filepath.ToSlash(p[sourceLen:]))
		mode, uid, gid := shared.GetOwnerMode(fInfo)
		args := lxd.InstanceFileArgs{
			UID:  int64(uid),
			GID:  int64(gid),
			Mode: int(mode.Perm()),
		}

		var readCloser io.ReadCloser

		if fInfo.IsDir() {
			// Directory handling
			args.Type = "directory"
		} else if fInfo.Mode()&os.ModeSymlink == os.ModeSymlink {
			// Symlink handling
			symlinkTarget, err := os.Readlink(p)
			if err != nil {
				return err
			}

			args.Type = "symlink"
			args.Content = bytes.NewReader([]byte(symlinkTarget))
			readCloser = io.NopCloser(args.Content)
		} else {
			// File handling
			f, err := os.Open(p)
			if err != nil {
				return err
			}

			defer func() { _ = f.Close() }()

			args.Type = "file"
			args.Content = f
			readCloser = f
		}

		progress := cli.ProgressRenderer{
			Format: fmt.Sprintf(i18n.G("Pushing %s to %s: %%s"), p, targetPath),
			Quiet:  c.global.flagQuiet,
		}

		if args.Type != "directory" {
			contentLength, err := args.Content.Seek(0, io.SeekEnd)
			if err != nil {
				return err
			}

			_, err = args.Content.Seek(0, io.SeekStart)
			if err != nil {
				return err
			}

			args.Content = shared.NewReadSeeker(&ioprogress.ProgressReader{
				ReadCloser: readCloser,
				Tracker: &ioprogress.ProgressTracker{
					Length: contentLength,
					Handler: func(percent int64, speed int64) {
						progress.UpdateProgress(ioprogress.ProgressData{
							Text: fmt.Sprintf("%d%% (%s/s)", percent,
								units.GetByteSizeString(speed, 2))})
					},
				},
			}, args.Content)
		}

		logger.Infof("Pushing %s to %s (%s)", p, targetPath, args.Type)
		err = d.CreateInstanceFile(inst, targetPath, args)
		if err != nil {
			if args.Type != "directory" {
				progress.Done("")
			}

			return err
		}

		if args.Type != "directory" {
			progress.Done("")
		}

		return nil
	}

	return filepath.Walk(source, sendFile)
}

func (c *cmdFile) recursiveMkdir(d lxd.InstanceServer, inst string, p string, mode *os.FileMode, uid int64, gid int64) error {
	/* special case, every instance has a /, we don't need to do anything */
	if p == "/" {
		return nil
	}

	// Remove trailing "/" e.g. /A/B/C/. Otherwise we will end up with an
	// empty array entry "" which will confuse the Mkdir() loop below.
	pclean := filepath.Clean(p)
	parts := strings.Split(pclean, "/")
	i := len(parts)

	for ; i >= 1; i-- {
		cur := filepath.Join(parts[:i]...)
		_, resp, err := d.GetInstanceFile(inst, cur)
		if err != nil {
			continue
		}

		if resp.Type != "directory" {
			return fmt.Errorf(i18n.G("%s is not a directory"), cur)
		}

		i++
		break
	}

	for ; i <= len(parts); i++ {
		cur := filepath.Join(parts[:i]...)
		if cur == "" {
			continue
		}

		cur = "/" + cur

		modeArg := -1
		if mode != nil {
			modeArg = int(mode.Perm())
		}

		args := lxd.InstanceFileArgs{
			UID:  uid,
			GID:  gid,
			Mode: modeArg,
			Type: "directory",
		}

		logger.Infof("Creating %s (%s)", cur, args.Type)
		err := d.CreateInstanceFile(inst, cur, args)
		if err != nil {
			return err
		}
	}

	return nil
}

// Mount.
type cmdFileMount struct {
	global *cmdGlobal
	file   *cmdFile

	flagListen   string
	flagAuthNone bool
	flagAuthUser string
}

func (c *cmdFileMount) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("mount", i18n.G("[<remote>:]<instance>[/<path>] [<target path>]"))
	cmd.Short = i18n.G("Mount files from instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Mount files from instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc file mount foo/root fooroot
   To mount /root from the instance foo onto the local fooroot directory.`))

	cmd.RunE = c.Run
	cmd.Flags().StringVar(&c.flagListen, "listen", "", i18n.G("Setup SSH SFTP listener on address:port instead of mounting"))
	cmd.Flags().BoolVar(&c.flagAuthNone, "no-auth", false, i18n.G("Disable authentication when using SSH SFTP listener"))
	cmd.Flags().StringVar(&c.flagAuthUser, "auth-user", "", i18n.G("Set authentication user when using SSH SFTP listener"))

	return cmd
}

func (c *cmdFileMount) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	var targetPath string

	// Determine the target if specified.
	if len(args) >= 2 {
		targetPath = shared.HostPathFollow(filepath.Clean(args[len(args)-1]))
		sb, err := os.Stat(targetPath)
		if err != nil {
			return err
		}

		if !sb.IsDir() {
			return fmt.Errorf(i18n.G("Target path must be a directory"))
		}
	}

	// Check which mode we should operate in. If target path is provided we use sshfs mode.
	if targetPath != "" && c.flagListen != "" {
		return fmt.Errorf(i18n.G("Target path and --listen flag cannot be used together"))
	}

	instSpec := strings.SplitN(resource.name, "/", 2)

	// Check instance path is provided in sshfs mode.
	if len(instSpec) < 2 && targetPath != "" {
		return fmt.Errorf(i18n.G("Invalid instance path: %q"), resource.name)
	}

	// Check instance path isn't provided in listener mode.
	if len(instSpec) > 1 && targetPath == "" {
		return fmt.Errorf(i18n.G("Instance path cannot be used in SSH SFTP listener mode"))
	}

	instName := instSpec[0]

	// Look for sshfs command if no SSH SFTP listener mode specified and a target mount path was specified.
	if c.flagListen == "" && targetPath != "" {
		sshfsPath, err := exec.LookPath("sshfs")
		if err != nil {
			// If sshfs command not found, then advise user of the --listen flag.
			return fmt.Errorf(i18n.G("sshfs not found. Try SSH SFTP mode using the --listen flag"))
		}

		// Setup sourcePath with leading / to ensure we reference the instance path from / location.
		instPath := filepath.Join(string(filepath.Separator), filepath.Clean(instSpec[1]))

		// If sshfs command is found, use it to mount the SFTP connection to the targetPath.
		return c.sshfsMount(cmd.Context(), resource, instName, instPath, sshfsPath, targetPath)
	}

	// If SSH SFTP listener specified or no target mount path specified, then use SSH SFTP server.
	return c.sshSFTPServer(cmd.Context(), instName, resource)
}

// sshfsMount mounts the instance's filesystem using sshfs by piping the instance's SFTP connection to sshfs.
func (c *cmdFileMount) sshfsMount(ctx context.Context, resource remoteResource, instName string, instPath string, sshfsPath string, targetPath string) error {
	sftpConn, err := resource.server.GetInstanceFileSFTPConn(instName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed connecting to instance SFTP: %w"), err)
	}

	defer func() { _ = sftpConn.Close() }()

	// Use the format "lxd.<instance_name>" as the source "host" (although not used for communication)
	// so that the mount can be seen to be associated with LXD and the instance in the local mount table.
	sourceURL := fmt.Sprintf("lxd.%s:%s", instName, instPath)

	sshfsCmd := exec.Command(sshfsPath, "-o", "slave", sourceURL, targetPath)

	// Setup pipes.
	stdin, err := sshfsCmd.StdinPipe()
	if err != nil {
		return err
	}

	stdout, err := sshfsCmd.StdoutPipe()
	if err != nil {
		return err
	}

	sshfsCmd.Stderr = os.Stderr

	err = sshfsCmd.Start()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed starting sshfs: %w"), err)
	}

	fmt.Printf(i18n.G("sshfs mounting %q on %q")+"\n", fmt.Sprintf("%s%s", instName, instPath), targetPath)
	fmt.Println(i18n.G("Press ctrl+c to finish"))

	ctx, cancel := context.WithCancel(ctx)
	chSignal := make(chan os.Signal, 1)
	signal.Notify(chSignal, os.Interrupt)
	go func() {
		select {
		case <-chSignal:
		case <-ctx.Done():
		}

		cancel()                                  // Prevents error output when the io.Copy functions finish.
		_ = sshfsCmd.Process.Signal(os.Interrupt) // This will cause sshfs to unmount.
		_ = stdin.Close()
	}()

	go func() {
		_, err := io.Copy(stdin, sftpConn)
		if ctx.Err() == nil {
			if err != nil {
				fmt.Fprintf(os.Stderr, i18n.G("I/O copy from instance to sshfs failed: %v")+"\n", err)
			} else {
				fmt.Println(i18n.G("Instance disconnected"))
			}
		}
		cancel() // Ask sshfs to end.
	}()

	_, err = io.Copy(sftpConn, stdout)
	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, i18n.G("I/O copy from sshfs to instance failed: %v")+"\n", err)
	}

	cancel() // Ask sshfs to end.

	err = sshfsCmd.Wait()
	if err != nil {
		return err
	}

	fmt.Println(i18n.G("sshfs has stopped"))

	return sftpConn.Close()
}

// sshSFTPServer runs an SSH server listening on a random port of 127.0.0.1.
// It provides an unauthenticated SFTP server connected to the instance's filesystem.
func (c *cmdFileMount) sshSFTPServer(ctx context.Context, instName string, resource remoteResource) error {
	// Check instance exists.
	_, _, err := resource.server.GetInstance(instName)
	if err != nil {
		return err
	}

	randString := func(length int) string {
		var chars = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0987654321")
		randStr := make([]rune, length)
		for i := range randStr {
			randStr[i] = chars[rand.Intn(len(chars))]
		}

		return string(randStr)
	}

	// Setup an SSH SFTP server.
	config := &ssh.ServerConfig{}

	var authUser, authPass string

	if c.flagAuthNone {
		config.NoClientAuth = true
	} else {
		if c.flagAuthUser != "" {
			authUser = c.flagAuthUser
		} else {
			authUser = randString(8)
		}

		authPass = randString(8)
		config.PasswordCallback = func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == authUser && string(pass) == authPass {
				return nil, nil
			}

			return nil, fmt.Errorf("Password rejected for %q", c.User())
		}
	}

	// Generate random host key.
	_, privKey, err := shared.GenerateMemCert(false, false)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed generating SSH host key: %w"), err)
	}

	private, err := ssh.ParsePrivateKey(privKey)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed parsing SSH host key: %w"), err)
	}

	config.AddHostKey(private)

	listenAddr := c.flagListen
	if listenAddr == "" {
		listenAddr = "127.0.0.1:0" // Listen on a random local port if not specified.
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to listen for connection: %w"), err)
	}

	fmt.Printf("SSH SFTP listening on %v\n", listener.Addr())

	if config.PasswordCallback != nil {
		fmt.Printf("Login with username %q and password %q\n", authUser, authPass)
	} else {
		fmt.Printf("Login without username and password\n")
	}

	for {
		// Wait for new SSH connections.
		nConn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to accept incoming connection: %w"), err)
		}

		// Handle each SSH connection in its own go routine.
		go func() {
			fmt.Printf(i18n.G("SSH client connected %q")+"\n", nConn.RemoteAddr())
			defer fmt.Printf(i18n.G("SSH client disconnected %q")+"\n", nConn.RemoteAddr())
			defer func() { _ = nConn.Close() }()

			// Before use, a handshake must be performed on the incoming net.Conn.
			_, chans, reqs, err := ssh.NewServerConn(nConn, config)
			if err != nil {
				fmt.Fprintf(os.Stderr, i18n.G("Failed SSH handshake with client %q: %v")+"\n", nConn.RemoteAddr(), err)
				return
			}

			// The incoming Request channel must be serviced.
			go ssh.DiscardRequests(reqs)

			// Service the incoming Channel requests.
			for newChannel := range chans {
				localChannel := newChannel

				// Channels have a type, depending on the application level protocol intended.
				// In the case of an SFTP session, this is "subsystem" with a payload string of
				// "<length=4>sftp"
				if localChannel.ChannelType() != "session" {
					_ = localChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
					fmt.Fprintf(os.Stderr, i18n.G("Unknown channel type for client %q: %s")+"\n", nConn.RemoteAddr(), localChannel.ChannelType())
					continue
				}

				// Accept incoming channel request.
				channel, requests, err := localChannel.Accept()
				if err != nil {
					fmt.Fprintf(os.Stderr, i18n.G("Failed accepting channel client %q: %v")+"\n", err)
					return
				}

				// Sessions have out-of-band requests such as "shell", "pty-req" and "env".
				// Here we handle only the "subsystem" request.
				go func(in <-chan *ssh.Request) {
					for req := range in {
						ok := false
						switch req.Type {
						case "subsystem":
							if string(req.Payload[4:]) == "sftp" {
								ok = true
							}
						}

						_ = req.Reply(ok, nil)
					}
				}(requests)

				// Handle each channel in its own go routine.
				go func() {
					defer func() { _ = channel.Close() }()

					// Connect to the instance's SFTP server.
					sftpConn, err := resource.server.GetInstanceFileSFTPConn(instName)
					if err != nil {
						fmt.Fprintf(os.Stderr, i18n.G("Failed connecting to instance SFTP for client %q: %v")+"\n", nConn.RemoteAddr(), err)
						return
					}

					defer func() { _ = sftpConn.Close() }()

					// Copy SFTP data between client and remote instance.
					ctx, cancel := context.WithCancel(ctx)
					go func() {
						_, err := io.Copy(channel, sftpConn)
						if ctx.Err() == nil {
							if err != nil {
								fmt.Fprintf(os.Stderr, i18n.G("I/O copy from instance to SSH failed: %v")+"\n", err)
							} else {
								fmt.Printf(i18n.G("Instance disconnected for client %q")+"\n", nConn.RemoteAddr())
							}
						}
						cancel() // Prevents error output when other io.Copy finishes.
						_ = channel.Close()
					}()

					_, err = io.Copy(sftpConn, channel)
					if err != nil && ctx.Err() == nil {
						fmt.Fprintf(os.Stderr, i18n.G("I/O copy from SSH to instance failed: %v")+"\n", err)
					}

					cancel() // Prevents error output when other io.Copy finishes.
					_ = sftpConn.Close()
				}()
			}
		}()
	}
}
