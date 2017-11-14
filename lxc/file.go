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

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/termios"
)

type fileCmd struct {
	uid  int
	gid  int
	mode string

	recursive bool

	mkdirs bool
}

func (c *fileCmd) showByDefault() bool {
	return true
}

func (c *fileCmd) usage() string {
	return i18n.G(
		`Usage: lxc file <subcommand> [options]

Manage files in containers.

lxc file pull [-r|--recursive] [<remote>:]<container>/<path> [[<remote>:]<container>/<path>...] <target path>
    Pull files from containers.

lxc file push [-r|--recursive] [-p|--create-dirs] [--uid=UID] [--gid=GID] [--mode=MODE] <source path> [<source path>...] [<remote>:]<container>/<path>
    Push files into containers.

lxc file delete [<remote>:]<container>/<path> [[<remote>:]<container>/<path>...]
    Delete files in containers.

lxc file edit [<remote>:]<container>/<path>
    Edit files in containers using the default text editor.

*Examples*
lxc file push /etc/hosts foo/etc/hosts
   To push /etc/hosts into the container "foo".

lxc file pull foo/etc/hosts .
   To pull /etc/hosts from the container and write it to the current directory.`)
}

func (c *fileCmd) flags() {
	gnuflag.IntVar(&c.uid, "uid", -1, i18n.G("Set the file's uid on push"))
	gnuflag.IntVar(&c.gid, "gid", -1, i18n.G("Set the file's gid on push"))
	gnuflag.StringVar(&c.mode, "mode", "", i18n.G("Set the file's perms on push"))
	gnuflag.BoolVar(&c.recursive, "recursive", false, i18n.G("Recursively push or pull files"))
	gnuflag.BoolVar(&c.recursive, "r", false, i18n.G("Recursively push or pull files"))
	gnuflag.BoolVar(&c.mkdirs, "create-dirs", false, i18n.G("Create any directories necessary"))
	gnuflag.BoolVar(&c.mkdirs, "p", false, i18n.G("Create any directories necessary"))
}

func (c *fileCmd) recursivePullFile(d lxd.ContainerServer, container string, p string, targetDir string) error {
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

		_, err = io.Copy(f, buf)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf(i18n.G("Unknown file type '%s'"), resp.Type)
	}

	return nil
}

func (c *fileCmd) recursivePushFile(d lxd.ContainerServer, container string, source string, target string) error {
	source = filepath.Clean(source)
	sourceDir, _ := filepath.Split(source)
	sourceLen := len(sourceDir)

	sendFile := func(p string, fInfo os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to walk path for %s: %s"), p, err)
		}

		// Detect unsupported files
		if !fInfo.Mode().IsRegular() && !fInfo.Mode().IsDir() && fInfo.Mode()&os.ModeSymlink != os.ModeSymlink {
			return fmt.Errorf(i18n.G("'%s' isn't a supported file type."), p)
		}

		// Prepare for file transfer
		targetPath := path.Join(target, filepath.ToSlash(p[sourceLen:]))
		mode, uid, gid := shared.GetOwnerMode(fInfo)
		args := lxd.ContainerFileArgs{
			UID:  int64(uid),
			GID:  int64(gid),
			Mode: int(mode.Perm()),
		}

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
		} else {
			// File handling
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()

			args.Type = "file"
			args.Content = f
		}

		logger.Infof("Pushing %s to %s (%s)", p, targetPath, args.Type)
		return d.CreateContainerFile(container, targetPath, args)
	}

	return filepath.Walk(source, sendFile)
}

func (c *fileCmd) recursiveMkdir(d lxd.ContainerServer, container string, p string, mode *os.FileMode, uid int64, gid int64) error {
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

func (c *fileCmd) push(conf *config.Config, send_file_perms bool, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	target := args[len(args)-1]
	pathSpec := strings.SplitN(target, "/", 2)

	if len(pathSpec) != 2 {
		return fmt.Errorf(i18n.G("Invalid target %s"), target)
	}

	remote, container, err := conf.ParseRemote(pathSpec[0])
	if err != nil {
		return err
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

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	var sourcefilenames []string
	for _, fname := range args[:len(args)-1] {
		if !strings.HasPrefix(fname, "--") {
			sourcefilenames = append(sourcefilenames, shared.HostPath(filepath.Clean(fname)))
		}
	}

	mode := os.FileMode(0755)
	if c.mode != "" {
		if len(c.mode) == 3 {
			c.mode = "0" + c.mode
		}

		m, err := strconv.ParseInt(c.mode, 0, 0)
		if err != nil {
			return err
		}
		mode = os.FileMode(m)
	}

	if c.recursive {
		if c.uid != -1 || c.gid != -1 || c.mode != "" {
			return fmt.Errorf(i18n.G("can't supply uid/gid/mode in recursive mode"))
		}

		if c.mkdirs {
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

			err = c.recursiveMkdir(d, container, targetPath, &mode, int64(uid), int64(gid))
			if err != nil {
				return err
			}
		}

		for _, fname := range sourcefilenames {
			err := c.recursivePushFile(d, container, fname, targetPath)
			if err != nil {
				return err
			}
		}

		return nil
	}

	uid := 0
	if c.uid >= 0 {
		uid = c.uid
	}

	gid := 0
	if c.gid >= 0 {
		gid = c.gid
	}

	if (len(sourcefilenames) > 1) && !targetIsDir {
		return errArgs
	}

	/* Make sure all of the files are accessible by us before trying to
	 * push any of them. */
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

	for _, f := range files {
		fpath := targetPath
		if targetIsDir {
			fpath = path.Join(fpath, path.Base(f.Name()))
		}

		if c.mkdirs {
			finfo, err := f.Stat()
			if err != nil {
				return err
			}

			_, dUid, dGid := shared.GetOwnerMode(finfo)
			if c.uid == -1 || c.gid == -1 {
				if c.uid == -1 {
					uid = dUid
				}

				if c.gid == -1 {
					gid = dGid
				}
			}

			err = c.recursiveMkdir(d, container, path.Dir(fpath), nil, int64(uid), int64(gid))
			if err != nil {
				return err
			}
		}

		args := lxd.ContainerFileArgs{
			Content: f,
			UID:     -1,
			GID:     -1,
			Mode:    -1,
		}

		if send_file_perms {
			if c.mode == "" || c.uid == -1 || c.gid == -1 {
				finfo, err := f.Stat()
				if err != nil {
					return err
				}

				fMode, fUid, fGid := shared.GetOwnerMode(finfo)
				if err != nil {
					return err
				}

				if c.mode == "" {
					mode = fMode
				}

				if c.uid == -1 {
					uid = fUid
				}

				if c.gid == -1 {
					gid = fGid
				}
			}

			args.UID = int64(uid)
			args.GID = int64(gid)
			args.Mode = int(mode.Perm())
		}
		args.Type = "file"

		logger.Infof("Pushing %s to %s (%s)", f.Name(), fpath, args.Type)
		err = d.CreateContainerFile(container, fpath, args)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *fileCmd) pull(conf *config.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

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
	} else if strings.HasSuffix(target, string(os.PathSeparator)) || len(args)-1 > 1 || c.recursive {
		err := os.MkdirAll(target, 0755)
		if err != nil {
			return err
		}
		targetIsDir = true
	}

	for _, f := range args[:len(args)-1] {
		pathSpec := strings.SplitN(f, "/", 2)
		if len(pathSpec) != 2 {
			return fmt.Errorf(i18n.G("Invalid source %s"), f)
		}

		remote, container, err := conf.ParseRemote(pathSpec[0])
		if err != nil {
			return err
		}

		d, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		if c.recursive {
			err := c.recursivePullFile(d, container, pathSpec[1], target)
			if err != nil {
				return err
			}

			continue
		}

		buf, resp, err := d.GetContainerFile(container, pathSpec[1])
		if err != nil {
			return err
		}

		if resp.Type == "directory" {
			return fmt.Errorf(i18n.G("Can't pull a directory without --recursive"))
		}

		var targetPath string
		if targetIsDir {
			targetPath = path.Join(target, path.Base(pathSpec[1]))
		} else {
			targetPath = target
		}

		logger.Infof("Pulling %s from %s (%s)", targetPath, pathSpec[1], resp.Type)

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

		_, err = io.Copy(f, buf)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *fileCmd) delete(conf *config.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	for _, f := range args[:] {
		pathSpec := strings.SplitN(f, "/", 2)
		if len(pathSpec) != 2 {
			return fmt.Errorf(i18n.G("Invalid path %s"), f)
		}

		remote, container, err := conf.ParseRemote(pathSpec[0])
		if err != nil {
			return err
		}

		d, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		err = d.DeleteContainerFile(container, pathSpec[1])
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *fileCmd) edit(conf *config.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	if c.recursive {
		return fmt.Errorf(i18n.G("recursive edit doesn't make sense :("))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		return c.push(conf, false, append([]string{os.Stdin.Name()}, args[0]))
	}

	// Create temp file
	f, err := ioutil.TempFile("", "lxd_file_edit_")
	if err != nil {
		return fmt.Errorf("Unable to create a temporary file: %v", err)
	}
	fname := f.Name()
	f.Close()
	os.Remove(fname)
	defer os.Remove(fname)

	// Extract current value
	err = c.pull(conf, append([]string{args[0]}, fname))
	if err != nil {
		return err
	}

	_, err = shared.TextEditor(fname, []byte{})
	if err != nil {
		return err
	}

	err = c.push(conf, false, append([]string{fname}, args[0]))
	if err != nil {
		return err
	}

	return nil
}

func (c *fileCmd) run(conf *config.Config, args []string) error {
	if len(args) < 1 {
		return errUsage
	}

	switch args[0] {
	case "push":
		return c.push(conf, true, args[1:])
	case "pull":
		return c.pull(conf, args[1:])
	case "delete":
		return c.delete(conf, args[1:])
	case "edit":
		return c.edit(conf, args[1:])
	default:
		return errArgs
	}
}
