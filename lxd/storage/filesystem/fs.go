package filesystem

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared/logger"
)

// Filesystem magic numbers.
const (
	FilesystemSuperMagicZfs = 0x2fc12fc1
)

// StatVFS retrieves Virtual File System (VFS) info about a path.
func StatVFS(path string) (*unix.Statfs_t, error) {
	var st unix.Statfs_t

	err := unix.Statfs(path, &st)
	if err != nil {
		return nil, err
	}

	return &st, nil
}

// Detect returns the filesystem on which the passed-in path sits.
func Detect(path string) (string, error) {
	fs, err := StatVFS(path)
	if err != nil {
		return "", err
	}

	return FSTypeToName(int32(fs.Type))
}

// FSTypeToName returns the name of the given fs type.
// The fsType is from the Type field of unix.Statfs_t. We use int32 so that this function behaves the same on both
// 32bit and 64bit platforms by requiring any 64bit FS types to be overflowed before being passed in. They will
// then be compared with equally overflowed FS type constant values.
func FSTypeToName(fsType int32) (string, error) {
	// This function is needed to allow FS type constants that overflow an int32 to be overflowed without a
	// compile error on 32bit platforms. This allows us to use any 64bit constants from the unix package on
	// both 64bit and 32bit platforms without having to define the constant in its rolled over form on 32bit.
	to32 := func(fsType int64) int32 {
		return int32(fsType)
	}

	switch fsType {
	case to32(unix.BTRFS_SUPER_MAGIC): // BTRFS' constant required overflowing to an int32.
		return "btrfs", nil
	case unix.TMPFS_MAGIC:
		return "tmpfs", nil
	case unix.EXT4_SUPER_MAGIC:
		return "ext4", nil
	case unix.XFS_SUPER_MAGIC:
		return "xfs", nil
	case unix.NFS_SUPER_MAGIC:
		return "nfs", nil
	case FilesystemSuperMagicZfs:
		return "zfs", nil
	}

	logger.Debugf("Unknown backing filesystem type: 0x%x", fsType)
	return fmt.Sprintf("0x%x", fsType), nil
}

func hasMountEntry(name string) int {
	// In case someone uses symlinks we need to look for the actual
	// mountpoint.
	actualPath, err := filepath.EvalSymlinks(name)
	if err != nil {
		return -1
	}

	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return -1
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		tokens := strings.Fields(line)
		if len(tokens) < 5 {
			return -1
		}

		cleanPath := filepath.Clean(tokens[4])
		if cleanPath == actualPath {
			return 1
		}
	}

	return 0
}

// IsMountPoint returns true if path is a mount point.
func IsMountPoint(path string) bool {
	// If we find a mount entry, it is obviously a mount point.
	ret := hasMountEntry(path)
	if ret == 1 {
		return true
	}

	// Get the stat details.
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}

	rootStat, err := os.Lstat(path + "/..")
	if err != nil {
		return false
	}

	// If the directory has the same device as parent, then it's not a mountpoint.
	if stat.Sys().(*syscall.Stat_t).Dev == rootStat.Sys().(*syscall.Stat_t).Dev {
		return false
	}

	// Btrfs annoyingly uses a different Dev id for different subvolumes on the same mount.
	// So for btrfs, we require a matching mount entry in mountinfo.
	fs, err := Detect(path)
	if err == nil && fs == "btrfs" {
		return false
	}

	return true
}

// SyncFS will force a filesystem sync for the filesystem backing the provided path.
func SyncFS(path string) error {
	// Get us a file descriptor.
	fsFile, err := os.Open(path)
	if err != nil {
		return err
	}

	defer func() { _ = fsFile.Close() }()

	// Call SyncFS.
	return unix.Syncfs(int(fsFile.Fd()))
}

// PathNameEncode encodes a path string to be used as part of a file name.
// The encoding scheme replaces "-" with "--" and then "/" with "-".
func PathNameEncode(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "-", "--"), "/", "-")
}

// PathNameDecode decodes a string containing an encoded path back to its original form.
// The decoding scheme converts "-" back to "/" and "--" back to "-".
func PathNameDecode(text string) string {
	// This converts "--" to the null character "\0" first, to allow remaining "-" chars to be
	// converted back to "/" before making a final pass to convert "\0" back to original "-".
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(text, "--", "\000"), "-", "/"), "\000", "-")
}

// mountOption represents an individual mount option.
type mountOption struct {
	capture bool
	flag    uintptr
}

// mountFlagTypes represents a list of possible mount flags.
var mountFlagTypes = map[string]mountOption{
	"async":         {false, unix.MS_SYNCHRONOUS},
	"atime":         {false, unix.MS_NOATIME},
	"bind":          {true, unix.MS_BIND},
	"defaults":      {true, 0},
	"dev":           {false, unix.MS_NODEV},
	"diratime":      {false, unix.MS_NODIRATIME},
	"dirsync":       {true, unix.MS_DIRSYNC},
	"exec":          {false, unix.MS_NOEXEC},
	"lazytime":      {true, unix.MS_LAZYTIME},
	"mand":          {true, unix.MS_MANDLOCK},
	"noatime":       {true, unix.MS_NOATIME},
	"nodev":         {true, unix.MS_NODEV},
	"nodiratime":    {true, unix.MS_NODIRATIME},
	"noexec":        {true, unix.MS_NOEXEC},
	"nomand":        {false, unix.MS_MANDLOCK},
	"norelatime":    {false, unix.MS_RELATIME},
	"nostrictatime": {false, unix.MS_STRICTATIME},
	"nosuid":        {true, unix.MS_NOSUID},
	"rbind":         {true, unix.MS_BIND | unix.MS_REC},
	"relatime":      {true, unix.MS_RELATIME},
	"remount":       {true, unix.MS_REMOUNT},
	"ro":            {true, unix.MS_RDONLY},
	"rw":            {false, unix.MS_RDONLY},
	"strictatime":   {true, unix.MS_STRICTATIME},
	"suid":          {false, unix.MS_NOSUID},
	"sync":          {true, unix.MS_SYNCHRONOUS},
}

// ResolveMountOptions resolves the provided mount options.
func ResolveMountOptions(options []string) (uintptr, string) {
	mountFlags := uintptr(0)
	var mountOptions []string

	for i := range options {
		do, ok := mountFlagTypes[options[i]]
		if !ok {
			mountOptions = append(mountOptions, options[i])
			continue
		}

		if do.capture {
			mountFlags |= do.flag
		} else {
			mountFlags &= ^do.flag
		}
	}

	return mountFlags, strings.Join(mountOptions, ",")
}

// GetMountinfo tracks down the mount entry for the path and returns all MountInfo fields.
func GetMountinfo(path string) ([]string, error) {
	stat := &unix.Statx_t{}
	err := unix.Statx(0, path, 0, 0, stat)
	if err != nil {
		return nil, err
	}

	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	statMountID := strconv.FormatUint(stat.Mnt_id, 10)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		tokens := strings.Fields(line)
		if len(tokens) < 5 {
			continue
		}

		if tokens[0] == statMountID {
			return tokens, nil
		}
	}

	return nil, errors.New("No mountinfo entry found")
}

// StatDeviceID stats path and returns the device ID (major:minor) of the
// filesystem it lives on, in the same form as the third field of a mountinfo
// entry. Other helpers that derive information from a path by stat'ing it should
// follow the same StatXxx naming.
func StatDeviceID(path string) (string, error) {
	var st unix.Stat_t

	err := unix.Stat(path, &st)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%d:%d", unix.Major(uint64(st.Dev)), unix.Minor(uint64(st.Dev))), nil
}

// ErrNoMountInfoEntry is returned (possibly wrapped) by extractFromMountInfo and
// GetDeviceIDFromMountInfo when no line matched. Callers that need to tell "not
// mounted" apart from a real failure to read or parse the mountinfo file (e.g. to
// avoid treating the latter as "not a mount any more") should check for it with
// errors.Is rather than treating every returned error the same way.
var ErrNoMountInfoEntry = errors.New("No matching mountinfo entry")

// extractFromMountInfo scans a mountinfo file and, for every line with at least 5
// whitespace-separated fields for which match returns true, records extract's
// result. It returns the recording from the last matching line, which is what a
// process in that mount namespace would actually see (later entries shadow
// earlier ones mounted at the same point). It returns ErrNoMountInfoEntry if no
// line matched.
func extractFromMountInfo(mountinfoPath string, match func(fields []string) bool, extract func(fields []string) []string) ([]string, error) {
	f, err := os.Open(mountinfoPath)
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	var found []string

	scanner := bufio.NewScanner(f)

	// bufio.Scanner's default 64 KiB max line length can be too small for a
	// real mountinfo line: overlayfs mounts concatenate their lowerdir list
	// into the superblock options field, which grows with the number of image
	// layers and can exceed that easily. Without this, one such line anywhere
	// in the file - even for an unrelated mount - would make Scan() stop and
	// report ErrTooLong before ever reaching the line being looked for.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		// Fields are: mountID parentID major:minor root mountPoint ...
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}

		if match(fields) {
			found = extract(fields)
		}
	}

	err = scanner.Err()
	if err != nil {
		return nil, err
	}

	if found == nil {
		return nil, ErrNoMountInfoEntry
	}

	return found, nil
}

// mountInfoEscaper replaces the characters the kernel octal-escapes in
// mountinfo path fields (fs/proc_namespace.c's mangle(), the same convention
// /etc/mtab uses): space, tab, newline and backslash become \040, \011, \012
// and \134 respectively. Backslash must be replaced first, or the backslashes
// introduced by the other replacements would themselves get escaped.
var mountInfoEscaper = strings.NewReplacer(
	`\`, `\134`,
	" ", `\040`,
	"\t", `\011`,
	"\n", `\012`,
)

// escapeMountInfoField encodes path the way the kernel encodes mountinfo path
// fields, so it can be compared against an already-escaped field like fields[4].
func escapeMountInfoField(path string) string {
	return mountInfoEscaper.Replace(path)
}

// GetDeviceIDFromMountInfo returns the device ID (major:minor) of the filesystem
// mounted at mountPoint, as seen through the given mountinfo file.
//
// Unlike GetMountinfo, this matches by the mount point's path rather than by
// stat'ing it and matching on mount ID, so it works against a mountinfo file
// belonging to a different mount namespace than the caller's own (e.g.
// /proc/<pid>/mountinfo for another process), where the path can't be stat'd
// directly.
func GetDeviceIDFromMountInfo(mountinfoPath string, mountPoint string) (string, error) {
	// A mountinfo line looks like:
	//
	//   66 25 0:58 / /dev/lxd rw,relatime shared:32 - tmpfs tmpfs rw,size=1024k,mode=711
	//
	// Whitespace-split into fields, that's index 0: mount ID ("66"), 1: parent
	// mount ID ("25"), 2: device ID ("0:58"), 3: root ("/"), 4: mount point
	// ("/dev/lxd"), and more after that we don't care about here. So fields[2]
	// below is the device ID and fields[4] is the mount point.
	//
	// The kernel escapes space/tab/newline/backslash in path fields (e.g. a
	// mount point named "a b" appears as "a\040b"), so mountPoint needs the same
	// encoding before comparing it against fields[4].
	escapedMountPoint := escapeMountInfoField(mountPoint)
	fields, err := extractFromMountInfo(mountinfoPath,
		func(fields []string) bool { return fields[4] == escapedMountPoint },
		func(fields []string) []string { return fields[2:3] })
	if errors.Is(err, ErrNoMountInfoEntry) {
		return "", fmt.Errorf("No %q mount entry in %q: %w", mountPoint, mountinfoPath, ErrNoMountInfoEntry)
	}

	if err != nil {
		return "", err
	}

	return fields[0], nil
}
