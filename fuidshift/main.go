package main

import (
	"path/filepath"
	"fmt"
	"os"
	"syscall"
)

func help(me string, status int) {
	fmt.Printf("Usage: %s directory [-t] <range1> [<range2> ...]\n", me)
	fmt.Printf("  -t implies test mode.  No file ownerships will be changed.\n")
	fmt.Printf("  A range is [u|b|g]:<first_container_id:first_host_id:range>.\n")
	fmt.Printf("  where u means shift uids, g means shift gids, b means shift both.\n")
	fmt.Printf("  For example: %s directory b:0:100000:65536 u:10000:1000:1\n", me)
	os.Exit(status)
}

func main() {
	if err := run(); err != nil {
		fmt.Printf("Error: %q\n", err)
		help(os.Args[0], 1)
	}
}

func run() error {
	if len(os.Args) < 4 {
		if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
			help(os.Args[0], 0)
		} else {
			help(os.Args[0], 1)
		}
	}

	directory := os.Args[1]
	idmap := Idmap{}
	testmode := false

	for pos := 2; pos < len(os.Args); pos += 1 {

		switch os.Args[pos] {
		case "t", "-t", "--test", "test":
			testmode = true
		default:
			var err error
			idmap, err = idmap.Append(os.Args[pos])
			if err != nil {
				return err
			}
		}
	}

	if idmap.Len() == 0 {
		fmt.Printf("No idmaps given\n")
		help(os.Args[0], 1)
	}

	if !testmode && os.Geteuid() != 0 {
		fmt.Printf("This must be run as root\n")
		os.Exit(1)
	}

	return Uidshift(directory, idmap, testmode)
}

func getOwner(path string) (int, int, error) {
	var stat syscall.Stat_t
	err := syscall.Lstat(path, &stat)
	if err != nil {
		return 0, 0, err
	}
	uid := int(stat.Uid)
	gid := int(stat.Gid)
	return uid, gid, nil
}

func fileExists(p string) bool {
	_, err := os.Lstat(p)
	if err != nil && os.IsNotExist(err) {
		return false
	}
	return true
}

func Uidshift(dir string, idmap Idmap, testmode bool) error {
	convert := func (path string, fi os.FileInfo, err error) (e error) {
		uid, gid, err := getOwner(path)
		if err != nil {
			return err
		}
		newuid, newgid := idmap.Shift_into_ns(uid, gid)
		if testmode {
			fmt.Printf("I would shift %q to %d %d\n", path, newuid, newgid)
		} else {
			err = os.Chown(path, int(newuid), int(newgid))
			if err == nil {
				m := fi.Mode()
				if m&os.ModeSymlink == 0 {
					err = os.Chmod(path, m)
					if err != nil {
						fmt.Printf("Error resetting mode on %q, continuing\n", path)
					}
				}
			}
		}
		return nil
	}

	if ! fileExists(dir) {
		return fmt.Errorf("No such file or directory: %q", dir)
	}
	return filepath.Walk(dir, convert)
}
