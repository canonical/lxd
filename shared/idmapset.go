package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

/*
 * One entry in id mapping set - a single range of either
 * uid or gid mappings.
 */
type idmapEntry struct {
	isuid    bool
	isgid    bool
	srcid    int
	destid   int
	maprange int
}

func (e *idmapEntry) parse(s string) error {
	split := strings.Split(s, ":")
	var err error
	if len(split) != 4 {
		return fmt.Errorf("Bad idmap: %q", s)
	}
	switch split[0] {
	case "u":
		e.isuid = true
	case "g":
		e.isgid = true
	case "b":
		e.isuid = true
		e.isgid = true
	default:
		return fmt.Errorf("Bad idmap type in %q", s)
	}
	e.srcid, err = strconv.Atoi(split[1])
	if err != nil {
		return err
	}
	e.destid, err = strconv.Atoi(split[2])
	if err != nil {
		return err
	}
	e.maprange, err = strconv.Atoi(split[3])
	if err != nil {
		return err
	}

	// wraparound
	if e.srcid+e.maprange < e.srcid || e.destid+e.maprange < e.destid {
		return fmt.Errorf("Bad mapping: id wraparound")
	}

	return nil
}

/*
 * Convert an id from host id to mapped container id
 */
func (e *idmapEntry) shift_into_ns(id int) (int, error) {
	if id < e.srcid || id >= e.srcid+e.maprange {
		// this mapping doesn't apply
		return 0, fmt.Errorf("N/A")
	}

	return id - e.srcid + e.destid, nil
}

/* taken from http://blog.golang.org/slices (which is under BSD licence) */
func extend(slice []idmapEntry, element idmapEntry) []idmapEntry {
	n := len(slice)
	if n == cap(slice) {
		// Slice is full; must grow.
		// We double its size and add 1, so if the size is zero we still grow.
		newSlice := make([]idmapEntry, len(slice), 2*len(slice)+1)
		copy(newSlice, slice)
		slice = newSlice
	}
	slice = slice[0 : n+1]
	slice[n] = element
	return slice
}

type IdmapSet struct {
	idmap []idmapEntry
}

func (m IdmapSet) Len() int {
	return len(m.idmap)
}

func (m IdmapSet) Append(s string) (IdmapSet, error) {
	e := idmapEntry{}
	err := e.parse(s)
	if err != nil {
		return m, err
	}
	m.idmap = extend(m.idmap, e)
	return m, nil
}

func (m IdmapSet) ShiftIntoNs(uid int, gid int) (int, int) {
	u := -1
	g := -1
	for _, e := range m.idmap {
		if e.isuid && u == -1 {
			tmpu, err := e.shift_into_ns(uid)
			if err == nil {
				u = tmpu
			}
		}
		if e.isgid && g == -1 {
			tmpg, err := e.shift_into_ns(gid)
			if err == nil {
				g = tmpg
			}
		}
	}

	return u, g
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

func Uidshift(dir string, idmap IdmapSet, testmode bool) error {
	convert := func(path string, fi os.FileInfo, err error) (e error) {
		uid, gid, err := getOwner(path)
		if err != nil {
			return err
		}
		newuid, newgid := idmap.ShiftIntoNs(uid, gid)
		if testmode {
			fmt.Printf("I would shift %q to %d %d\n", path, newuid, newgid)
		} else {
			err = os.Lchown(path, int(newuid), int(newgid))
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

	if !PathExists(dir) {
		return fmt.Errorf("No such file or directory: %q", dir)
	}
	return filepath.Walk(dir, convert)
}
