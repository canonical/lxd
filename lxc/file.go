package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/termios"
)

type cmdFile struct {
	global *cmdGlobal

	flagUID  int
	flagGID  int
	flagMode string

	flagMkdir     bool
	flagRecursive bool
}

func (c *cmdFile) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("file")
	cmd.Short = i18n.G("Manage files in containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage files in containers`))

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

	return cmd
}

// Delete
type cmdFileDelete struct {
	global *cmdGlobal
	file   *cmdFile
}

func (c *cmdFileDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("delete [<remote>:]<container>/<path> [[<remote>:]<container>/<path>...]")
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete files in containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete files in containers`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdFileDelete) Run(cmd *cobra.Command, args []string) error {
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
		pathSpec := strings.SplitN(resource.name, "/", 2)
		if len(pathSpec) != 2 {
			return fmt.Errorf(i18n.G("Invalid path %s"), resource.name)
		}

		// Delete the file
		err = resource.server.DeleteContainerFile(pathSpec[0], pathSpec[1])
		if err != nil {
			return err
		}
	}

	return nil
}

// Edit
type cmdFileEdit struct {
	global   *cmdGlobal
	file     *cmdFile
	filePull *cmdFilePull
	filePush *cmdFilePush
}

func (c *cmdFileEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("edit [<remote>:]<container>/<path>")
	cmd.Short = i18n.G("Edit files in containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit files in containers`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdFileEdit) Run(cmd *cobra.Command, args []string) error {
	c.filePush.noModeChange = true

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		return c.filePush.Run(cmd, append([]string{os.Stdin.Name()}, args[0]))
	}

	// Create temp file
	f, err := ioutil.TempFile("", "lxd_file_edit_")
	if err != nil {
		return fmt.Errorf(i18n.G("Unable to create a temporary file: %v"), err)
	}
	fname := f.Name()
	f.Close()
	os.Remove(fname)
	defer os.Remove(shared.HostPath(fname))

	// Extract current value
	err = c.filePull.Run(cmd, append([]string{args[0]}, fname))
	if err != nil {
		return err
	}

	// Spawn the editor
	_, err = shared.TextEditor(shared.HostPath(fname), []byte{})
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

// Pull
type cmdFilePull struct {
	global *cmdGlobal
	file   *cmdFile
}

func (c *cmdFilePull) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("pull [<remote>:]<container>/<path> [[<remote>:]<container>/<path>...] <target path>")
	cmd.Short = i18n.G("Pull files from containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Pull files from containers`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc file pull foo/etc/hosts .
   To pull /etc/hosts from the container and write it to the current directory.`))

	cmd.Flags().BoolVarP(&c.file.flagMkdir, "create-dirs", "p", false, i18n.G("Create any directories necessary"))
	cmd.Flags().BoolVarP(&c.file.flagRecursive, "recursive", "r", false, i18n.G("Recursively transfer files"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdFilePull) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Determine the target
	target := shared.HostPath(filepath.Clean(args[len(args)-1]))
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
		err := os.MkdirAll(target, 0755)
		if err != nil {
			return err
		}
		targetIsDir = true
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

		buf, resp, err := resource.server.GetContainerFile(pathSpec[0], pathSpec[1])
		if err != nil {
			return err
		}

		// Deal with recursion
		if resp.Type == "directory" {
			if c.file.flagRecursive {
				if !shared.PathExists(target) {
					err := os.MkdirAll(target, 0755)
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
			linkTarget, err := ioutil.ReadAll(buf)
			if err != nil {
				return err
			}

			// Follow the symlink
			if targetPath == "-" || c.file.flagRecursive {
				for {
					newPath := strings.TrimSuffix(string(linkTarget), "\n")
					if !strings.HasPrefix(newPath, "/") {
						newPath = filepath.Clean(filepath.Join(filepath.Dir(pathSpec[1]), newPath))
					}

					buf, resp, err = resource.server.GetContainerFile(pathSpec[0], newPath)
					if err != nil {
						return err
					}

					if resp.Type != "symlink" {
						break
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
			defer f.Close()

			err = os.Chmod(targetPath, os.FileMode(resp.Mode))
			if err != nil {
				return err
			}
		}

		progress := utils.ProgressRenderer{
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
							shared.GetByteSizeString(bytesReceived, 2),
							shared.GetByteSizeString(speed, 2))})
				},
			},
		}

		_, err = io.Copy(writer, buf)
		if err != nil {
			progress.Done("")
			return err
		}
		progress.Done("")
	}

	return nil
}

// Push
type cmdFilePush struct {
	global *cmdGlobal
	file   *cmdFile

	noModeChange bool
}

func (c *cmdFilePush) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("push <source path> [<remote>:]<container>/<path> [[<remote>:]<container>/<path>...]")
	cmd.Short = i18n.G("Push files into containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Push files into containers`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc file push /etc/hosts foo/etc/hosts
   To push /etc/hosts into the container "foo".`))

	cmd.Flags().BoolVarP(&c.file.flagRecursive, "recursive", "r", false, i18n.G("Recursively transfer files"))
	cmd.Flags().BoolVarP(&c.file.flagMkdir, "create-dirs", "p", false, i18n.G("Create any directories necessary"))
	cmd.Flags().IntVar(&c.file.flagUID, "uid", -1, i18n.G("Set the file's uid on push")+"``")
	cmd.Flags().IntVar(&c.file.flagGID, "gid", -1, i18n.G("Set the file's gid on push")+"``")
	cmd.Flags().StringVar(&c.file.flagMode, "mode", "", i18n.G("Set the file's perms on push")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdFilePush) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		sourcefilenames = append(sourcefilenames, shared.HostPath(filepath.Clean(fname)))
	}

	// Determine the target mode
	mode := os.FileMode(0755)
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
		// Sanity checks
		if c.file.flagUID != -1 || c.file.flagGID != -1 || c.file.flagMode != "" {
			return fmt.Errorf(i18n.G("Can't supply uid/gid/mode in recursive mode"))
		}

		// Create needed paths if requested
		if c.file.flagMkdir {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			finfo, err := f.Stat()
			f.Close()
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

		defer file.Close()
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
		args := lxd.ContainerFileArgs{
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

		progress := utils.ProgressRenderer{
			Format: fmt.Sprintf(i18n.G("Pushing %s to %s: %%s"), f.Name(), fpath),
			Quiet:  c.global.flagQuiet,
		}

		args.Content = shared.NewReadSeeker(&ioprogress.ProgressReader{
			ReadCloser: f,
			Tracker: &ioprogress.ProgressTracker{
				Length: fstat.Size(),
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{
						Text: fmt.Sprintf("%d%% (%s/s)", percent, shared.GetByteSizeString(speed, 2)),
					})
				},
			},
		}, f)

		logger.Infof("Pushing %s to %s (%s)", f.Name(), fpath, args.Type)
		err = resource.server.CreateContainerFile(resource.name, fpath, args)
		if err != nil {
			progress.Done("")
			return err
		}
		progress.Done("")
	}

	return nil
}

func (c *cmdFile) recursivePullFile(d lxd.ContainerServer, container string, p string, targetDir string) error {
	buf, resp, err := d.GetContainerFile(container, p)
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

			err := c.recursivePullFile(d, container, nextP, target)
			if err != nil {
				return err
			}
		}
	} else if resp.Type == "file" {
		f, err := os.Create(target)
		if err != nil {
			return err
		}
		defer f.Close()

		err = os.Chmod(target, os.FileMode(resp.Mode))
		if err != nil {
			return err
		}

		progress := utils.ProgressRenderer{
			Format: fmt.Sprintf(i18n.G("Pulling %s from %s: %%s"), p, target),
			Quiet:  c.global.flagQuiet,
		}

		writer := &ioprogress.ProgressWriter{
			WriteCloser: f,
			Tracker: &ioprogress.ProgressTracker{
				Handler: func(bytesReceived int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{
						Text: fmt.Sprintf("%s (%s/s)",
							shared.GetByteSizeString(bytesReceived, 2),
							shared.GetByteSizeString(speed, 2))})
				},
			},
		}

		_, err = io.Copy(writer, buf)
		if err != nil {
			progress.Done("")
			return err
		}
		progress.Done("")
	} else if resp.Type == "symlink" {
		linkTarget, err := ioutil.ReadAll(buf)
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

func (c *cmdFile) recursivePushFile(d lxd.ContainerServer, container string, source string, target string) error {
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
		args := lxd.ContainerFileArgs{
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
			readCloser = ioutil.NopCloser(args.Content)
		} else {
			// File handling
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()

			args.Type = "file"
			args.Content = f
			readCloser = f
		}

		progress := utils.ProgressRenderer{
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
								shared.GetByteSizeString(speed, 2))})
					},
				},
			}, args.Content)
		}

		logger.Infof("Pushing %s to %s (%s)", p, targetPath, args.Type)
		err = d.CreateContainerFile(container, targetPath, args)
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

func (c *cmdFile) recursiveMkdir(d lxd.ContainerServer, container string, p string, mode *os.FileMode, uid int64, gid int64) error {
	/* special case, every container has a /, we don't need to do anything */
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
		_, resp, err := d.GetContainerFile(container, cur)
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
		args := lxd.ContainerFileArgs{
			UID:  uid,
			GID:  gid,
			Mode: modeArg,
			Type: "directory",
		}

		logger.Infof("Creating %s (%s)", cur, args.Type)
		err := d.CreateContainerFile(container, cur, args)
		if err != nil {
			return err
		}
	}

	return nil
}
