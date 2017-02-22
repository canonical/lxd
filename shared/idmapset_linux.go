package shared

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
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

func (e *IdmapEntry) ToLxcString() []string {
	if e.Isuid && e.Isgid {
		return []string{
			fmt.Sprintf("u %d %d %d", e.Nsid, e.Hostid, e.Maprange),
			fmt.Sprintf("g %d %d %d", e.Nsid, e.Hostid, e.Maprange),
		}
	}

	if e.Isuid {
		return []string{fmt.Sprintf("u %d %d %d", e.Nsid, e.Hostid, e.Maprange)}
	}

	return []string{fmt.Sprintf("g %d %d %d", e.Nsid, e.Hostid, e.Maprange)}
}

func is_between(x, low, high int) bool {
	return x >= low && x < high
}

func (e *IdmapEntry) HostidsIntersect(i IdmapEntry) bool {
	if (e.Isuid && i.Isuid) || (e.Isgid && i.Isgid) {
		switch {
		case is_between(e.Hostid, i.Hostid, i.Hostid+i.Maprange):
			return true
		case is_between(i.Hostid, e.Hostid, e.Hostid+e.Maprange):
			return true
		case is_between(e.Hostid+e.Maprange, i.Hostid, i.Hostid+i.Maprange):
			return true
		case is_between(i.Hostid+i.Maprange, e.Hostid, e.Hostid+e.Maprange):
			return true
		}
	}

	return false
}

func (e *IdmapEntry) Intersects(i IdmapEntry) bool {
	if (e.Isuid && i.Isuid) || (e.Isgid && i.Isgid) {
		switch {
		case is_between(e.Hostid, i.Hostid, i.Hostid+i.Maprange-1):
			return true
		case is_between(i.Hostid, e.Hostid, e.Hostid+e.Maprange-1):
			return true
		case is_between(e.Hostid+e.Maprange-1, i.Hostid, i.Hostid+i.Maprange-1):
			return true
		case is_between(i.Hostid+i.Maprange-1, e.Hostid, e.Hostid+e.Maprange-1):
			return true
		case is_between(e.Nsid, i.Nsid, i.Nsid+i.Maprange-1):
			return true
		case is_between(i.Nsid, e.Nsid, e.Nsid+e.Maprange-1):
			return true
		case is_between(e.Nsid+e.Maprange-1, i.Nsid, i.Nsid+i.Maprange-1):
			return true
		case is_between(i.Nsid+i.Maprange-1, e.Nsid, e.Nsid+e.Maprange-1):
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

type ByHostid []*IdmapEntry

func (s ByHostid) Len() int {
	return len(s)
}

func (s ByHostid) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s ByHostid) Less(i, j int) bool {
	return s[i].Hostid < s[j].Hostid
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

func (m IdmapSet) HostidsIntersect(i IdmapEntry) bool {
	for _, e := range m.Idmap {
		if i.HostidsIntersect(e) {
			return true
		}
	}
	return false
}

/* AddSafe adds an entry to the idmap set, breaking apart any ranges that the
 * new idmap intersects with in the process.
 */
func (m *IdmapSet) AddSafe(i IdmapEntry) error {
	result := []IdmapEntry{}
	added := false
	for _, e := range m.Idmap {
		if !e.Intersects(i) {
			result = append(result, e)
			continue
		}

		if e.HostidsIntersect(i) {
			return fmt.Errorf("can't map the same host ID twice")
		}

		added = true

		lower := IdmapEntry{
			Isuid:    e.Isuid,
			Isgid:    e.Isgid,
			Hostid:   e.Hostid,
			Nsid:     e.Nsid,
			Maprange: i.Nsid - e.Nsid,
		}

		upper := IdmapEntry{
			Isuid:    e.Isuid,
			Isgid:    e.Isgid,
			Hostid:   e.Hostid + lower.Maprange + i.Maprange,
			Nsid:     i.Nsid + i.Maprange,
			Maprange: e.Maprange - i.Maprange - lower.Maprange,
		}

		if lower.Maprange > 0 {
			result = append(result, lower)
		}
		result = append(result, i)
		if upper.Maprange > 0 {
			result = append(result, upper)
		}
	}

	if !added {
		result = append(result, i)
	}

	m.Idmap = result
	return nil
}

func (m IdmapSet) ToLxcString() []string {
	var lines []string
	for _, e := range m.Idmap {
		for _, l := range e.ToLxcString() {
			if !StringInSlice(l+"\n", lines) {
				lines = append(lines, l+"\n")
			}
		}
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
	// Expand any symlink before the final path component
	tmp := filepath.Dir(dir)
	tmp, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		return err
	}
	dir = filepath.Join(tmp, filepath.Base(dir))
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

/*
 * get a uid or gid mapping from /etc/subxid
 */
func getFromShadow(fname string, username string) ([][]int, error) {
	entries := [][]int{}

	f, err := os.Open(fname)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Skip comments
		s := strings.Split(scanner.Text(), "#")
		if len(s[0]) == 0 {
			continue
		}

		// Validate format
		s = strings.Split(s[0], ":")
		if len(s) < 3 {
			return nil, fmt.Errorf("Unexpected values in %q: %q", fname, s)
		}

		if strings.EqualFold(s[0], username) {
			// Get range start
			entryStart, err := strconv.ParseUint(s[1], 10, 32)
			if err != nil {
				continue
			}

			// Get range size
			entrySize, err := strconv.ParseUint(s[2], 10, 32)
			if err != nil {
				continue
			}

			entries = append(entries, []int{int(entryStart), int(entrySize)})
		}
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("User %q has no %ss.", username, path.Base(fname))
	}

	return entries, nil
}

/*
 * get a uid or gid mapping from /proc/self/{g,u}id_map
 */
func getFromProc(fname string) ([][]int, error) {
	entries := [][]int{}

	f, err := os.Open(fname)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Skip comments
		s := strings.Split(scanner.Text(), "#")
		if len(s[0]) == 0 {
			continue
		}

		// Validate format
		s = strings.Fields(s[0])
		if len(s) < 3 {
			return nil, fmt.Errorf("Unexpected values in %q: %q", fname, s)
		}

		// Get range start
		entryStart, err := strconv.ParseUint(s[0], 10, 32)
		if err != nil {
			continue
		}

		// Get range size
		entryHost, err := strconv.ParseUint(s[1], 10, 32)
		if err != nil {
			continue
		}

		// Get range size
		entrySize, err := strconv.ParseUint(s[2], 10, 32)
		if err != nil {
			continue
		}

		entries = append(entries, []int{int(entryStart), int(entryHost), int(entrySize)})
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("Namespace doesn't have any map set")
	}

	return entries, nil
}

/*
 * Create a new default idmap
 */
func DefaultIdmapSet() (*IdmapSet, error) {
	idmapset := new(IdmapSet)

	// Check if shadow's uidmap tools are installed
	newuidmap, _ := exec.LookPath("newuidmap")
	newgidmap, _ := exec.LookPath("newgidmap")
	if newuidmap != "" && newgidmap != "" && PathExists("/etc/subuid") && PathExists("/etc/subgid") {
		// Parse the shadow uidmap
		entries, err := getFromShadow("/etc/subuid", "root")
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			// Check that it's big enough to be useful
			if int(entry[1]) < 65536 {
				continue
			}

			e := IdmapEntry{Isuid: true, Nsid: 0, Hostid: entry[0], Maprange: entry[1]}
			idmapset.Idmap = Extend(idmapset.Idmap, e)

			// NOTE: Remove once LXD can deal with multiple shadow maps
			break
		}

		// Parse the shadow gidmap
		entries, err = getFromShadow("/etc/subgid", "root")
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			// Check that it's big enough to be useful
			if int(entry[1]) < 65536 {
				continue
			}

			e := IdmapEntry{Isgid: true, Nsid: 0, Hostid: entry[0], Maprange: entry[1]}
			idmapset.Idmap = Extend(idmapset.Idmap, e)

			// NOTE: Remove once LXD can deal with multiple shadow maps
			break
		}
	} else {
		// Fallback map
		e := IdmapEntry{Isuid: true, Isgid: true, Nsid: 0, Hostid: 1000000, Maprange: 1000000000}
		idmapset.Idmap = Extend(idmapset.Idmap, e)
	}

	return idmapset, nil
}

/*
 * Create an idmap of the current allocation
 */
func CurrentIdmapSet() (*IdmapSet, error) {
	idmapset := new(IdmapSet)

	if PathExists("/proc/self/uid_map") {
		// Parse the uidmap
		entries, err := getFromProc("/proc/self/uid_map")
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			e := IdmapEntry{Isuid: true, Nsid: entry[0], Hostid: entry[1], Maprange: entry[2]}
			idmapset.Idmap = Extend(idmapset.Idmap, e)
		}
	} else {
		// Fallback map
		e := IdmapEntry{Isuid: true, Nsid: 0, Hostid: 0, Maprange: 0}
		idmapset.Idmap = Extend(idmapset.Idmap, e)
	}

	if PathExists("/proc/self/gid_map") {
		// Parse the gidmap
		entries, err := getFromProc("/proc/self/gid_map")
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			e := IdmapEntry{Isgid: true, Nsid: entry[0], Hostid: entry[1], Maprange: entry[2]}
			idmapset.Idmap = Extend(idmapset.Idmap, e)
		}
	} else {
		// Fallback map
		e := IdmapEntry{Isgid: true, Nsid: 0, Hostid: 0, Maprange: 0}
		idmapset.Idmap = Extend(idmapset.Idmap, e)
	}

	return idmapset, nil
}
