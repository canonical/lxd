package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
)

type fileCmd struct {
	uid  int
	gid  int
	mode string
}

const fileUsage = `
Manage files on a container.

lxc file push [--uid=UID] [--gid=GID] [--mode=MODE] <source> [<source>...] <target>
lxc file pull <source> [<source>...] <target>
`

func (c *fileCmd) usage() string {
	return fileUsage
}

func (c *fileCmd) flags() {
	gnuflag.IntVar(&c.uid, "uid", -1, "Set the file's uid on push")
	gnuflag.IntVar(&c.gid, "gid", -1, "Set the file's gid on push")
	gnuflag.StringVar(&c.mode, "mode", "0644", "Set the file's perms on push")
}

func (c *fileCmd) push(config *lxd.Config, args []string) error {
	target := args[len(args)-1]
	pathSpec := strings.SplitAfterN(target, "/", 2)

	if len(pathSpec) != 2 {
		return fmt.Errorf("Invalid target %s", target)
	}

	targetPath := pathSpec[1]

	d, container, err := lxd.NewClient(config, pathSpec[0])
	if err != nil {
		return err
	}

	mode := os.FileMode(0755)
	if c.mode != "" {
		m, err := strconv.ParseInt(c.mode, 0, 0)
		if err != nil {
			return err
		}
		mode = os.FileMode(m)
	}

	uid := 1000
	if c.uid >= 0 {
		uid = c.uid
	}

	gid := 1000
	if c.gid >= 0 {
		gid = c.gid
	}

	/* Make sure all of the files are accessible by us before trying to
	 * push any of them. */
	files := make([]*os.File, 0)
	for _, f := range args[:len(args)-1] {
		if !strings.HasPrefix(f, "--") {
			file, err := os.Open(f)
			if err != nil {
				return err
			}

			files = append(files, file)
		}
	}

	for _, f := range files {
		fpath := path.Join(targetPath, path.Base(f.Name()))
		err := d.PushFile(container, fpath, gid, uid, mode, f)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *fileCmd) pull(config *lxd.Config, args []string) error {
	target := args[len(args)-1]
	targetIsDir := false
	sb, err := os.Stat(target)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	/*
	 * If the path exists, just use it. If it doesn't exist, it might be a
	 * directory in one of two cases:
	 *   1. Someone explicitly put "/" at the end
	 *   2. Someone provided more than one source. In this case the target
	 *      should be a directory so we can save all the files into it.
	 */
	if err == nil {

		targetIsDir = sb.IsDir()
		if !targetIsDir && len(args)-1 > 1 {
			return fmt.Errorf("More than one file to download, but target is not a directory")
		}

	} else if strings.HasSuffix(target, string(os.PathSeparator)) || len(args)-1 > 1 {

		if err := os.MkdirAll(target, 0755); err != nil {
			return err
		}
		targetIsDir = true
	}

	for _, f := range args[:len(args)-1] {
		pathSpec := strings.SplitN(f, "/", 2)
		if len(pathSpec) != 2 {
			return fmt.Errorf("Invalid source %s", f)
		}

		d, container, err := lxd.NewClient(config, pathSpec[0])
		if err != nil {
			return err
		}

		_, _, _, buf, err := d.PullFile(container, pathSpec[1])
		if err != nil {
			return err
		}

		var targetPath string
		if targetIsDir {
			targetPath = path.Join(target, path.Base(pathSpec[1]))
		} else {
			targetPath = target
		}

		f, err := os.Create(targetPath)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(f, buf)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *fileCmd) run(config *lxd.Config, args []string) error {

	switch args[0] {
	case "push":
		return c.push(config, args[1:])
	case "pull":
		return c.pull(config, args[1:])
	default:
		return fmt.Errorf("invalid argument %s", args[0])
	}
}
