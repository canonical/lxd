package shared

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path"
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
	hostid   int // id as seen on the host - i.e. 100000
	nsid     int // id as seen in the ns - i.e. 0
	maprange int
}

func (e *idmapEntry) ToLxcString() string {
	if e.isuid {
		return fmt.Sprintf("u %d %d %d\n", e.nsid, e.hostid, e.maprange)
	}
	return fmt.Sprintf("g %d %d %d\n", e.nsid, e.hostid, e.maprange)
}

func is_between(x, low, high int) bool {
	return x >= low && x < high
}

func (e *idmapEntry) Intersects(i idmapEntry) bool {
	if (e.isuid && i.isuid) || (e.isgid && i.isgid) {
		switch {
		case is_between(e.hostid, i.hostid, i.hostid+i.maprange):
			return true
		case is_between(i.hostid, e.hostid, e.hostid+e.maprange):
			return true
		case is_between(e.hostid+e.maprange, i.hostid, i.hostid+i.maprange):
			return true
		case is_between(i.hostid+e.maprange, e.hostid, e.hostid+e.maprange):
			return true
		case is_between(e.nsid, i.nsid, i.nsid+i.maprange):
			return true
		case is_between(i.nsid, e.nsid, e.nsid+e.maprange):
			return true
		case is_between(e.nsid+e.maprange, i.nsid, i.nsid+i.maprange):
			return true
		case is_between(i.nsid+e.maprange, e.nsid, e.nsid+e.maprange):
			return true
		}
	}
	return false
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
	e.nsid, err = strconv.Atoi(split[1])
	if err != nil {
		return err
	}
	e.hostid, err = strconv.Atoi(split[2])
	if err != nil {
		return err
	}
	e.maprange, err = strconv.Atoi(split[3])
	if err != nil {
		return err
	}

	// wraparound
	if e.hostid+e.maprange < e.hostid || e.nsid+e.maprange < e.nsid {
		return fmt.Errorf("Bad mapping: id wraparound")
	}

	return nil
}

/*
 * Shift a uid from the host into the container
 * I.e. 0 -> 1000 -> 101000
 */
func (e *idmapEntry) shift_into_ns(id int) (int, error) {
	if id < e.nsid || id >= e.nsid+e.maprange {
		// this mapping doesn't apply
		return 0, fmt.Errorf("N/A")
	}

	return id - e.nsid + e.hostid, nil
}

/*
 * Shift a uid from the container back to the host
 * I.e. 101000 -> 1000
 */
func (e *idmapEntry) shift_from_ns(id int) (int, error) {
	if id < e.hostid || id >= e.hostid+e.maprange {
		// this mapping doesn't apply
		return 0, fmt.Errorf("N/A")
	}

	return id - e.hostid + e.nsid, nil
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

func (m IdmapSet) Intersects(i idmapEntry) bool {
	for _, e := range m.idmap {
		if i.Intersects(e) {
			return true
		}
	}
	return false
}

func (m IdmapSet) ToLxcString() []string {
	var lines []string
	for _, e := range m.idmap {
		lines = append(lines, e.ToLxcString())
	}
	return lines
}

func (m IdmapSet) Append(s string) (IdmapSet, error) {
	e := idmapEntry{}
	err := e.parse(s)
	if err != nil {
		return m, err
	}
	if m.Intersects(e) {
		return m, fmt.Errorf("Conflicting id mapping")
	}
	m.idmap = extend(m.idmap, e)
	return m, nil
}

func (m IdmapSet) doShiftIntoNs(uid int, gid int, how string) (int, int) {
	u := -1
	g := -1
	for _, e := range m.idmap {
		var err error
		var tmpu, tmpg int
		if e.isuid && u == -1 {
			switch how {
			case "in":
				tmpu, err = e.shift_into_ns(uid)
			case "out":
				tmpu, err = e.shift_from_ns(uid)
			}
			if err == nil {
				u = tmpu
			}
		}
		if e.isgid && g == -1 {
			switch how {
			case "in":
				tmpg, err = e.shift_into_ns(gid)
			case "out":
				tmpg, err = e.shift_from_ns(gid)
			}
			if err == nil {
				g = tmpg
			}
		}
	}

	return u, g
}

func (m IdmapSet) ShiftIntoNs(uid int, gid int) (int, int) {
	return m.doShiftIntoNs(uid, gid, "in")
}

func (m IdmapSet) ShiftFromNs(uid int, gid int) (int, int) {
	return m.doShiftIntoNs(uid, gid, "out")
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

func (set *IdmapSet) doUidshiftIntoContainer(dir string, testmode bool, how string) error {
	convert := func(path string, fi os.FileInfo, err error) (e error) {
		uid, gid, err := getOwner(path)
		if err != nil {
			return err
		}
		var newuid, newgid int
		switch how {
		case "in":
			newuid, newgid = set.ShiftIntoNs(uid, gid)
		case "out":
			newuid, newgid = set.ShiftFromNs(uid, gid)
		}
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

func (set *IdmapSet) UidshiftIntoContainer(dir string, testmode bool) error {
	return set.doUidshiftIntoContainer(dir, testmode, "in")
}

func (set *IdmapSet) UidshiftFromContainer(dir string, testmode bool) error {
	return set.doUidshiftIntoContainer(dir, testmode, "out")
}

func (set *IdmapSet) ShiftRootfs(p string) error {
	return set.doUidshiftIntoContainer(p, false, "in")
}

func (set *IdmapSet) UnshiftRootfs(p string) error {
	return set.doUidshiftIntoContainer(p, false, "out")
}

const (
	minIDRange = 65536
)

/*
 * get a uid or gid mapping from /etc/subxid
 */
func getFromMap(fname string, username string) (int, int, error) {
	f, err := os.Open(fname)
	var min int
	var idrange int
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
			min = int(bigmin)
			idrange = int(bigIdrange)
			return min, idrange, nil
		}
	}

	return 0, 0, fmt.Errorf("User %q has no %ss.", username, path.Base(fname))
}

/*
 * Create a new default idmap
 */
func DefaultIdmapSet() (*IdmapSet, error) {
	me, err := user.Current()
	if err != nil {
		return nil, err
	}

	umin := 1000000
	urange := 100000
	gmin := 1000000
	grange := 100000

	newuidmap, _ := exec.LookPath("newuidmap")
	newgidmap, _ := exec.LookPath("newgidmap")

	if newuidmap != "" && newgidmap != "" {
		umin, urange, err = getFromMap("/etc/subuid", me.Username)
		if err != nil {
			return nil, err
		}

		gmin, grange, err = getFromMap("/etc/subgid", me.Username)
		if err != nil {
			return nil, err
		}
	}

	if urange < minIDRange {
		return nil, fmt.Errorf("uidrange less than %d", minIDRange)
	}

	if grange < minIDRange {
		return nil, fmt.Errorf("gidrange less than %d", minIDRange)
	}

	m := new(IdmapSet)

	e := idmapEntry{isuid: true, nsid: 0, hostid: umin, maprange: urange}
	m.idmap = extend(m.idmap, e)
	e = idmapEntry{isgid: true, nsid: 0, hostid: gmin, maprange: grange}
	m.idmap = extend(m.idmap, e)

	return m, nil
}
