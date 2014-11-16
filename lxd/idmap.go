package main

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"path"
	"strconv"
	"strings"
)

/*
 * We'll flesh this out to be lists of ranges
 * We will want a list of available ranges (all ranges
 * which lxd may use) and taken range (parts of the
 * available ranges which are already in use by containers)
 *
 * We also may want some way of deciding which containers may
 * or perhaps must not share ranges
 *
 * For now, we simply have a single range, shared by all
 * containers
 */
type Idmap struct {
	Uidmin, Uidrange uint
	Gidmin, Gidrange uint
}

func checkmap(fname string, username string) (uint, uint, error) {
	f, err := os.Open(fname)
	var min uint
	var idrange uint
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	min = 0
	idrange = 0
	for scanner.Scan() {
		/*
		 * /etc/sub{gu}id allow comments in the files, so ignore
		 * everything after a '#'
		 */
		s := strings.Split(scanner.Text(), "#")
		if len(s[0]) == 0 {
			continue
		}

		s = strings.Split(s[0], ":")
		if len(s) < 3 {
			return 0, 0, fmt.Errorf("unexpected values in %q: %q", fname, s)
		}
		if strings.EqualFold(s[0], username) {
			bigmin, err := strconv.ParseUint(s[1], 10, 32)
			if err != nil {
				continue
			}
			bigIdrange, err := strconv.ParseUint(s[2], 10, 32)
			if err != nil {
				continue
			}
			min = uint(bigmin)
			idrange = uint(bigIdrange)
			return min, idrange, nil
		}
	}

	return 0, 0, fmt.Errorf("User %q has no %ss.", username, path.Base(fname))
}

func NewIdmap() (*Idmap, error) {
	me, err := user.Current()
	if err != nil {
		return nil, err
	}

	m := new(Idmap)
	umin, urange, err := checkmap("/etc/subuid", me.Username)
	if err != nil {
		return nil, err
	}
	gmin, grange, err := checkmap("/etc/subgid", me.Username)
	if err != nil {
		return nil, err
	}
	m.Uidmin = umin
	m.Uidrange = urange
	m.Gidmin = gmin
	m.Gidrange = grange
	return m, nil
}
