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
)

/*
 * One entry in id mapping set - a single range of either
 * uid or gid mappings.
 */
type IdmapEntry struct {
	Isuid    bool
	Isgid    bool
	Hostid   int // id as seen on the host - i.e. 100000
	Nsid     int // id as seen in the ns - i.e. 0
	Maprange int
}

func (e *IdmapEntry) ToLxcString() string {
	if e.Isuid {
		return fmt.Sprintf("u %d %d %d", e.Nsid, e.Hostid, e.Maprange)
	}
	return fmt.Sprintf("g %d %d %d", e.Nsid, e.Hostid, e.Maprange)
}

func is_between(x, low, high int) bool {
	return x >= low && x < high
}

func (e *IdmapEntry) Intersects(i IdmapEntry) bool {
	if (e.Isuid && i.Isuid) || (e.Isgid && i.Isgid) {
		switch {
		case is_between(e.Hostid, i.Hostid, i.Hostid+i.Maprange):
			return true
		case is_between(i.Hostid, e.Hostid, e.Hostid+e.Maprange):
			return true
		case is_between(e.Hostid+e.Maprange, i.Hostid, i.Hostid+i.Maprange):
			return true
		case is_between(i.Hostid+e.Maprange, e.Hostid, e.Hostid+e.Maprange):
			return true
		case is_between(e.Nsid, i.Nsid, i.Nsid+i.Maprange):
			return true
		case is_between(i.Nsid, e.Nsid, e.Nsid+e.Maprange):
			return true
		case is_between(e.Nsid+e.Maprange, i.Nsid, i.Nsid+i.Maprange):
			return true
		case is_between(i.Nsid+e.Maprange, e.Nsid, e.Nsid+e.Maprange):
			return true
		}
	}
	return false
}

func (e *IdmapEntry) parse(s string) error {
	split := strings.Split(s, ":")
	var err error
	if len(split) != 4 {
		return fmt.Errorf("Bad idmap: %q", s)
	}
	switch split[0] {
	case "u":
		e.Isuid = true
	case "g":
		e.Isgid = true
	case "b":
		e.Isuid = true
		e.Isgid = true
	default:
		return fmt.Errorf("Bad idmap type in %q", s)
	}
	e.Nsid, err = strconv.Atoi(split[1])
	if err != nil {
		return err
	}
	e.Hostid, err = strconv.Atoi(split[2])
	if err != nil {
		return err
	}
	e.Maprange, err = strconv.Atoi(split[3])
	if err != nil {
		return err
	}

	// wraparound
	if e.Hostid+e.Maprange < e.Hostid || e.Nsid+e.Maprange < e.Nsid {
		return fmt.Errorf("Bad mapping: id wraparound")
	}

	return nil
}

/*
 * Shift a uid from the host into the container
 * I.e. 0 -> 1000 -> 101000
 */
func (e *IdmapEntry) shift_into_ns(id int) (int, error) {
	if id < e.Nsid || id >= e.Nsid+e.Maprange {
		// this mapping doesn't apply
		return 0, fmt.Errorf("N/A")
	}

	return id - e.Nsid + e.Hostid, nil
}

/*
 * Shift a uid from the container back to the host
 * I.e. 101000 -> 1000
 */
func (e *IdmapEntry) shift_from_ns(id int) (int, error) {
	if id < e.Hostid || id >= e.Hostid+e.Maprange {
		// this mapping doesn't apply
		return 0, fmt.Errorf("N/A")
	}

	return id - e.Hostid + e.Nsid, nil
}

/* taken from http://blog.golang.org/slices (which is under BSD licence) */
func Extend(slice []IdmapEntry, element IdmapEntry) []IdmapEntry {
	n := len(slice)
	if n == cap(slice) {
		// Slice is full; must grow.
		// We double its size and add 1, so if the size is zero we still grow.
		newSlice := make([]IdmapEntry, len(slice), 2*len(slice)+1)
		copy(newSlice, slice)
		slice = newSlice
	}
	slice = slice[0 : n+1]
	slice[n] = element
	return slice
}

type IdmapSet struct {
	Idmap []IdmapEntry
}

func (m IdmapSet) Len() int {
	return len(m.Idmap)
}

func (m IdmapSet) Intersects(i IdmapEntry) bool {
	for _, e := range m.Idmap {
		if i.Intersects(e) {
			return true
		}
	}
	return false
}

func (m IdmapSet) ToLxcString() []string {
	var lines []string
	for _, e := range m.Idmap {
		lines = append(lines, e.ToLxcString()+"\n")
	}
	return lines
}

func (m IdmapSet) Append(s string) (IdmapSet, error) {
	e := IdmapEntry{}
	err := e.parse(s)
	if err != nil {
		return m, err
	}
	if m.Intersects(e) {
		return m, fmt.Errorf("Conflicting id mapping")
	}
	m.Idmap = Extend(m.Idmap, e)
	return m, nil
}

func (m IdmapSet) doShiftIntoNs(uid int, gid int, how string) (int, int) {
	u := -1
	g := -1
	for _, e := range m.Idmap {
		var err error
		var tmpu, tmpg int
		if e.Isuid && u == -1 {
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
		if e.Isgid && g == -1 {
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

func GetOwner(path string) (int, int, error) {
	uid, gid, _, _, _, _, err := GetFileStat(path)
	return uid, gid, err
}

func (set *IdmapSet) doUidshiftIntoContainer(dir string, testmode bool, how string) error {
	// Expand any symlink in dir and cleanup resulting path
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	dir = strings.TrimRight(dir, "/")

	convert := func(path string, fi os.FileInfo, err error) (e error) {
		uid, gid, err := GetOwner(path)
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
			err = ShiftOwner(dir, path, int(newuid), int(newgid))
			if err != nil {
				return err
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

func (set *IdmapSet) ShiftFile(p string) error {
	return set.ShiftRootfs(p)
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
 * Get current username
 */
func getUsername() (string, error) {
	me, err := user.Current()
	if err == nil {
		return me.Username, nil
	} else {
		/* user.Current() requires cgo */
		username := os.Getenv("USER")
		if username == "" {
			return "", err
		}
		return username, nil
	}
}

/*
 * Create a new default idmap
 */
func DefaultIdmapSet() (*IdmapSet, error) {
	myname, err := getUsername()
	if err != nil {
		return nil, err
	}

	umin := 1000000
	urange := 100000
	gmin := 1000000
	grange := 100000

	newuidmap, _ := exec.LookPath("newuidmap")
	newgidmap, _ := exec.LookPath("newgidmap")

	if newuidmap != "" && newgidmap != "" && PathExists("/etc/subuid") && PathExists("/etc/subgid") {
		umin, urange, err = getFromMap("/etc/subuid", myname)
		if err != nil {
			return nil, err
		}

		gmin, grange, err = getFromMap("/etc/subgid", myname)
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

	e := IdmapEntry{Isuid: true, Nsid: 0, Hostid: umin, Maprange: urange}
	m.Idmap = Extend(m.Idmap, e)
	e = IdmapEntry{Isgid: true, Nsid: 0, Hostid: gmin, Maprange: grange}
	m.Idmap = Extend(m.Idmap, e)

	return m, nil
}
