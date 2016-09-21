package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
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
		`Manage files on a container.

lxc file pull [-r|--recursive] <source> [<source>...] <target>
lxc file push [-r|--recursive] [-p|create-dirs] [--uid=UID] [--gid=GID] [--mode=MODE] <source> [<source>...] <target>
lxc file edit <file>

<source> in the case of pull, <target> in the case of push and <file> in the case of edit are <container name>/<path>

Examples:

To push /etc/hosts into the container foo:
  lxc file push /etc/hosts foo/etc/hosts

To pull /etc/hosts from the container:
  lxc file pull foo/etc/hosts .
`)
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

func (c *fileCmd) push(config *lxd.Config, send_file_perms bool, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	target := args[len(args)-1]
	pathSpec := strings.SplitN(target, "/", 2)

	if len(pathSpec) != 2 {
		return fmt.Errorf(i18n.G("Invalid target %s"), target)
	}

	targetPath := pathSpec[1]
	remote, container := config.ParseRemoteAndContainer(pathSpec[0])

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	var sourcefilenames []string
	for _, fname := range args[:len(args)-1] {
		if !strings.HasPrefix(fname, "--") {
			sourcefilenames = append(sourcefilenames, fname)
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

		for _, fname := range sourcefilenames {
			if c.mkdirs {
				if err := d.MkdirP(container, fname, mode); err != nil {
					return err
				}
			}

			if err := d.RecursivePushFile(container, fname, pathSpec[1]); err != nil {
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

	_, targetfilename := filepath.Split(targetPath)

	if (targetfilename != "") && (len(sourcefilenames) > 1) {
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
		if targetfilename == "" {
			fpath = path.Join(fpath, path.Base(f.Name()))
		}

		if c.mkdirs {
			if err := d.MkdirP(container, filepath.Dir(fpath), mode); err != nil {
				return err
			}
		}

		if send_file_perms {
			if c.mode == "" || c.uid == -1 || c.gid == -1 {
				fMode, fUid, fGid, err := c.getOwner(f)
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

			err = d.PushFile(container, fpath, gid, uid, fmt.Sprintf("%04o", mode.Perm()), f)
		} else {
			err = d.PushFile(container, fpath, -1, -1, "", f)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func (c *fileCmd) pull(config *lxd.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	target := args[len(args)-1]
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
		if err := os.MkdirAll(target, 0755); err != nil {
			return err
		}
		targetIsDir = true
	}

	for _, f := range args[:len(args)-1] {
		pathSpec := strings.SplitN(f, "/", 2)
		if len(pathSpec) != 2 {
			return fmt.Errorf(i18n.G("Invalid source %s"), f)
		}

		remote, container := config.ParseRemoteAndContainer(pathSpec[0])
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		if c.recursive {
			if err := d.RecursivePullFile(container, pathSpec[1], target); err != nil {
				return err
			}

			continue
		}

		_, _, mode, type_, buf, _, err := d.PullFile(container, pathSpec[1])
		if err != nil {
			return err
		}

		if type_ == "directory" {
			return fmt.Errorf(i18n.G("can't pull a directory without --recursive"))
		}

		var targetPath string
		if targetIsDir {
			targetPath = path.Join(target, path.Base(pathSpec[1]))
		} else {
			targetPath = target
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

			err = f.Chmod(os.FileMode(mode))
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

func (c *fileCmd) edit(config *lxd.Config, args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	if c.recursive {
		return fmt.Errorf(i18n.G("recursive edit doesn't make sense :("))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		return c.push(config, false, append([]string{os.Stdin.Name()}, args[0]))
	}

	// Create temp file
	f, err := ioutil.TempFile("", "lxd_file_edit_")
	fname := f.Name()
	f.Close()
	os.Remove(fname)
	defer os.Remove(fname)

	// Extract current value
	err = c.pull(config, append([]string{args[0]}, fname))
	if err != nil {
		return err
	}

	_, err = shared.TextEditor(fname, []byte{})
	if err != nil {
		return err
	}

	err = c.push(config, false, append([]string{fname}, args[0]))
	if err != nil {
		return err
	}

	return nil
}

func (c *fileCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	switch args[0] {
	case "push":
		return c.push(config, true, args[1:])
	case "pull":
		return c.pull(config, args[1:])
	case "edit":
		return c.edit(config, args[1:])
	default:
		return errArgs
	}
}
