package shared

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/flosch/pongo2"
	"github.com/pkg/sftp"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
)

// SnapshotDelimiter is the character used to delimit instance and snapshot names.
const SnapshotDelimiter = "/"

// HTTPSDefaultPort is the default port for the LXD HTTPS listener.
const HTTPSDefaultPort = 8443

// HTTPDefaultPort is the default port for the LXD HTTP listener.
const HTTPDefaultPort = 8080

// HTTPSMetricsDefaultPort is the default port for LXD metrics.
const HTTPSMetricsDefaultPort = 9100

// HTTPSStorageBucketsDefaultPort is the default port for the storage buckets listener.
const HTTPSStorageBucketsDefaultPort = 9000

// URLEncode encodes a path and query parameters to a URL.
func URLEncode(path string, query map[string]string) (string, error) {
	u, err := url.Parse(path)
	if err != nil {
		return "", err
	}

	params := url.Values{}
	for key, value := range query {
		params.Add(key, value)
	}

	u.RawQuery = params.Encode()
	return u.String(), nil
}

// AddSlash adds a slash to the end of paths if they don't already have one.
// This can be useful for rsyncing things, since rsync has behavior present on
// the presence or absence of a trailing slash.
func AddSlash(path string) string {
	if path[len(path)-1] != '/' {
		return path + "/"
	}

	return path
}

// PathExists checks if the given path exists in the filesystem.
func PathExists(name string) bool {
	_, err := os.Lstat(name)
	if err != nil && os.IsNotExist(err) {
		return false
	}

	return true
}

// PathIsEmpty checks if the given path is empty.
func PathIsEmpty(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}

	defer func() { _ = f.Close() }()

	// read in ONLY one file
	_, err = f.ReadDir(1)

	// and if the file is EOF... well, the dir is empty.
	if err == io.EOF {
		return true, nil
	}

	return false, err
}

// IsDir returns true if the given path is a directory.
func IsDir(name string) bool {
	stat, err := os.Stat(name)
	if err != nil {
		return false
	}

	return stat.IsDir()
}

// IsUnixSocket returns true if the given path is either a Unix socket
// or a symbolic link pointing at a Unix socket.
func IsUnixSocket(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}

	return (stat.Mode() & os.ModeSocket) == os.ModeSocket
}

// HostPathFollow takes a valid path (from HostPath) and resolves it
// all the way to its target or to the last which can be resolved.
func HostPathFollow(path string) string {
	// Ignore empty paths
	if len(path) == 0 {
		return path
	}

	// Don't prefix stdin/stdout
	if path == "-" {
		return path
	}

	// Check if we're running in a snap package.
	if !InSnap() {
		return path
	}

	// Handle relative paths
	if path[0] != os.PathSeparator {
		// Use the cwd of the parent as snap-confine alters our own cwd on launch
		ppid := os.Getppid()
		if ppid < 1 {
			return path
		}

		pwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", ppid))
		if err != nil {
			return path
		}

		path = filepath.Clean(strings.Join([]string{pwd, path}, string(os.PathSeparator)))
	}

	// Rely on "readlink -m" to do the right thing.
	path = HostPath(path)
	for {
		target, err := RunCommand("readlink", "-m", path)
		if err != nil {
			return path
		}

		target = strings.TrimSpace(target)

		if path == HostPath(target) {
			return path
		}

		path = HostPath(target)
	}
}

// HostPath returns the host path for the provided path
// On a normal system, this does nothing
// When inside of a snap environment, returns the real path.
func HostPath(path string) string {
	// Ignore empty paths
	if len(path) == 0 {
		return path
	}

	// Don't prefix stdin/stdout
	if path == "-" {
		return path
	}

	// Check if we're running in a snap package
	if !InSnap() {
		return path
	}

	// Handle relative paths
	if path[0] != os.PathSeparator {
		// Use the cwd of the parent as snap-confine alters our own cwd on launch
		ppid := os.Getppid()
		if ppid < 1 {
			return path
		}

		pwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", ppid))
		if err != nil {
			return path
		}

		path = filepath.Clean(strings.Join([]string{pwd, path}, string(os.PathSeparator)))
	}

	// Check if the path is already snap-aware
	for _, prefix := range []string{"/dev", "/snap", "/var/snap", "/var/lib/snapd"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return path
		}
	}

	return "/var/lib/snapd/hostfs" + path
}

// VarPath returns the provided path elements joined by a slash and
// appended to the end of $LXD_DIR, which defaults to /var/lib/lxd.
func VarPath(path ...string) string {
	varDir := os.Getenv("LXD_DIR")
	if varDir == "" {
		varDir = "/var/lib/lxd"
	}

	items := []string{varDir}
	items = append(items, path...)
	return filepath.Join(items...)
}

// CachePath returns the directory that LXD should its cache under. If LXD_DIR is
// set, this path is $LXD_DIR/cache, otherwise it is /var/cache/lxd.
func CachePath(path ...string) string {
	varDir := os.Getenv("LXD_DIR")
	cacheDir := "/var/cache/lxd"
	if varDir != "" {
		cacheDir = filepath.Join(varDir, "cache")
	}

	items := []string{cacheDir}
	items = append(items, path...)
	return filepath.Join(items...)
}

// LogPath returns the directory that LXD should put logs under. If LXD_DIR is
// set, this path is $LXD_DIR/logs, otherwise it is /var/log/lxd.
func LogPath(path ...string) string {
	varDir := os.Getenv("LXD_DIR")
	logDir := "/var/log/lxd"
	if varDir != "" {
		logDir = filepath.Join(varDir, "logs")
	}

	items := []string{logDir}
	items = append(items, path...)
	return filepath.Join(items...)
}

// LXDFileHeaders is extracted from the `X-LXD-*` family of file permissions
// headers.
type LXDFileHeaders struct {
	UID  int64
	GID  int64
	Mode int

	GIDModifyExisting  bool
	UIDModifyExisting  bool
	ModeModifyExisting bool

	Type  string
	Write string
}

// ParseLXDFileHeaders parses and validates the `X-LXD-*` family of file
// permissions headers.
//   - `X-LXD-uid`, `X-LXD-gid`
//     Base 10 integer
//   - `X-LXD-mode`
//     Base 10 integer (no leading `0`) or base 8 integer (leading `0`) for the
//     unix permissions bits
//   - `X-LXD-type`
//     One of `file`, `symlink`, `directory`
//   - `X-LXD-write`
//     One of `overwrite`, `append`
//   - `X-LXD-modify-perm`
//     Comma separated list; 0 or more of `mode`, `uid`, `gid`
func ParseLXDFileHeaders(headers http.Header) (*LXDFileHeaders, error) {
	var uid, gid int64 = -1, -1
	var mode = -1
	var err error

	rawUID := headers.Get("X-LXD-uid")
	if rawUID != "" {
		uid, err = strconv.ParseInt(rawUID, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("Invalid UID: %w", err)
		}
	}

	rawGID := headers.Get("X-LXD-gid")
	if rawGID != "" {
		gid, err = strconv.ParseInt(rawGID, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("Invalid GID: %w", err)
		}
	}

	rawMode := headers.Get("X-LXD-mode")
	if rawMode != "" {
		mode64, err := strconv.ParseInt(rawMode, 0, 0)
		if err != nil || mode64 < 0 {
			return nil, fmt.Errorf("Invalid Mode: %w", err)
		}

		mode = int(mode64 & int64(os.ModePerm))
	}

	filetype := headers.Get("X-LXD-type")
	/* backwards compat: before "type" was introduced, we could only
	 * manipulate files
	 */
	if filetype == "" {
		filetype = "file"
	}

	if !ValueInSlice(filetype, []string{"file", "symlink", "directory"}) {
		return nil, fmt.Errorf("Invalid file type: %q", filetype)
	}

	write := headers.Get("X-LXD-write")
	/* backwards compat: before "write" was introduced, we could only
	 * overwrite files
	 */
	if write == "" {
		write = "overwrite"
	}

	if !ValueInSlice(write, []string{"overwrite", "append"}) {
		return nil, fmt.Errorf("Invalid file write mode: %q", write)
	}

	UIDModifyExisting := false
	GIDModifyExisting := false
	modeModifyExisting := false

	modifyPermHeader := headers.Get("X-LXD-modify-perm")

	if modifyPermHeader != "" {
		for _, perm := range strings.Split(modifyPermHeader, ",") {
			UIDModifyExisting = UIDModifyExisting || perm == "uid"
			GIDModifyExisting = GIDModifyExisting || perm == "gid"
			modeModifyExisting = modeModifyExisting || perm == "mode"

			if !ValueInSlice(perm, []string{"uid", "gid", "mode"}) {
				return nil, fmt.Errorf("Invalid modify-perm field: %q", perm)
			}
		}
	}

	return &LXDFileHeaders{
		UID:  uid,
		GID:  gid,
		Mode: mode,

		UIDModifyExisting:  UIDModifyExisting,
		GIDModifyExisting:  GIDModifyExisting,
		ModeModifyExisting: modeModifyExisting,

		Type:  filetype,
		Write: write,
	}, nil
}

// ReaderToChannel reads data from an io.Reader and sends it to a returned channel
// in chunks. The function also takes the buffer size, which defaults to 128 KiB
// if the provided value is smaller.
func ReaderToChannel(r io.Reader, bufferSize int) <-chan []byte {
	if bufferSize <= 128*1024 {
		bufferSize = 128 * 1024
	}

	ch := make(chan ([]byte))

	go func() {
		readSize := 128 * 1024
		offset := 0
		buf := make([]byte, bufferSize)

		for {
			read := buf[offset : offset+readSize]
			nr, err := r.Read(read)
			offset += nr
			if offset > 0 && (offset+readSize >= bufferSize || err != nil) {
				ch <- buf[0:offset]
				offset = 0
				buf = make([]byte, bufferSize)
			}

			if err != nil {
				close(ch)
				break
			}
		}
	}()

	return ch
}

// RandomCryptoString generates 32 bytes long cryptographically secure random string.
func RandomCryptoString() (string, error) {
	buf := make([]byte, 32)
	n, err := rand.Read(buf)
	if err != nil {
		return "", err
	}

	if n != len(buf) {
		return "", fmt.Errorf("not enough random bytes read")
	}

	return hex.EncodeToString(buf), nil
}

// AtoiEmptyDefault returns the default value if the string is empty, otherwise converts
// it to an integer.
func AtoiEmptyDefault(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}

	return strconv.Atoi(s)
}

// ReadStdin reads a line of input from stdin and returns it as a byte slice.
func ReadStdin() ([]byte, error) {
	buf := bufio.NewReader(os.Stdin)
	line, _, err := buf.ReadLine()
	if err != nil {
		return nil, err
	}

	return line, nil
}

// WriteAll writes all data from the byte slice to the given writer.
func WriteAll(w io.Writer, data []byte) error {
	buf := bytes.NewBuffer(data)

	toWrite := int64(buf.Len())
	for {
		n, err := io.Copy(w, buf)
		if err != nil {
			return err
		}

		toWrite -= n
		if toWrite <= 0 {
			return nil
		}
	}
}

// QuotaWriter returns an error once a given write quota gets exceeded.
type QuotaWriter struct {
	writer io.Writer
	quota  int64
	n      int64
}

// NewQuotaWriter returns a new QuotaWriter wrapping the given writer.
//
// If the given quota is negative, then no quota is applied.
func NewQuotaWriter(writer io.Writer, quota int64) *QuotaWriter {
	return &QuotaWriter{
		writer: writer,
		quota:  quota,
	}
}

// Write implements the Writer interface.
func (w *QuotaWriter) Write(p []byte) (n int, err error) {
	if w.quota >= 0 {
		w.n += int64(len(p))
		if w.n > w.quota {
			return 0, fmt.Errorf("reached %d bytes, exceeding quota of %d", w.n, w.quota)
		}
	}
	return w.writer.Write(p)
}

// FileMove tries to move a file by using os.Rename,
// if that fails it tries to copy the file and remove the source.
func FileMove(oldPath string, newPath string) error {
	err := os.Rename(oldPath, newPath)
	if err == nil {
		return nil
	}

	err = FileCopy(oldPath, newPath)
	if err != nil {
		return err
	}

	_ = os.Remove(oldPath)

	return nil
}

// FileCopy copies a file, overwriting the target if it exists.
func FileCopy(source string, dest string) error {
	fi, err := os.Lstat(source)
	if err != nil {
		return err
	}

	_, uid, gid := GetOwnerMode(fi)

	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}

		if PathExists(dest) {
			err = os.Remove(dest)
			if err != nil {
				return err
			}
		}

		err = os.Symlink(target, dest)
		if err != nil {
			return err
		}

		if runtime.GOOS != "windows" {
			return os.Lchown(dest, uid, gid)
		}

		return nil
	}

	s, err := os.Open(source)
	if err != nil {
		return err
	}

	defer func() { _ = s.Close() }()

	d, err := os.Create(dest)
	if err != nil {
		if !os.IsExist(err) {
			return err
		}

		d, err = os.OpenFile(dest, os.O_WRONLY, fi.Mode())
		if err != nil {
			return err
		}
	}

	_, err = io.Copy(d, s)
	if err != nil {
		return err
	}

	/* chown not supported on windows */
	if runtime.GOOS != "windows" {
		err = d.Chown(uid, gid)
		if err != nil {
			return err
		}
	}

	return d.Close()
}

// DirCopy copies a directory recursively, overwriting the target if it exists.
func DirCopy(source string, dest string) error {
	// Get info about source.
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("failed to get source directory info: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("source is not a directory")
	}

	// Remove dest if it already exists.
	if PathExists(dest) {
		err := os.RemoveAll(dest)
		if err != nil {
			return fmt.Errorf("failed to remove destination directory %s: %w", dest, err)
		}
	}

	// Create dest.
	err = os.MkdirAll(dest, info.Mode())
	if err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", dest, err)
	}

	// Copy all files.
	entries, err := os.ReadDir(source)
	if err != nil {
		return fmt.Errorf("failed to read source directory %s: %w", source, err)
	}

	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		destPath := filepath.Join(dest, entry.Name())

		if entry.IsDir() {
			err := DirCopy(sourcePath, destPath)
			if err != nil {
				return fmt.Errorf("failed to copy sub-directory from %s to %s: %w", sourcePath, destPath, err)
			}
		} else {
			err := FileCopy(sourcePath, destPath)
			if err != nil {
				return fmt.Errorf("failed to copy file from %s to %s: %w", sourcePath, destPath, err)
			}
		}
	}

	return nil
}

// BytesReadCloser wraps a bytes.Buffer to implement io.ReadCloser.
type BytesReadCloser struct {
	Buf *bytes.Buffer
}

// Read reads data from the buffer into b.
func (r BytesReadCloser) Read(b []byte) (n int, err error) {
	return r.Buf.Read(b)
}

// Close is a no-op as the data is in memory.
func (r BytesReadCloser) Close() error {
	return nil
}

// IsSnapshot returns true if a given name contains the snapshot delimiter.
func IsSnapshot(name string) bool {
	return strings.Contains(name, SnapshotDelimiter)
}

// MkdirAllOwner creates a directory named path, along with any necessary parents, and with specified
// permissions. It sets the ownership of the created directories to the provided uid and gid.
func MkdirAllOwner(path string, perm os.FileMode, uid int, gid int) error {
	// This function is a slightly modified version of MkdirAll from the Go standard library.
	// https://golang.org/src/os/path.go?s=488:535#L9

	// Fast path: if we can tell whether path is a directory or file, stop with success or error.
	dir, err := os.Stat(path)
	if err == nil {
		if dir.IsDir() {
			return nil
		}

		return fmt.Errorf("path exists but isn't a directory")
	}

	// Slow path: make sure parent exists and then call Mkdir for path.
	i := len(path)
	for i > 0 && os.IsPathSeparator(path[i-1]) { // Skip trailing path separator.
		i--
	}

	j := i
	for j > 0 && !os.IsPathSeparator(path[j-1]) { // Scan backward over element.
		j--
	}

	if j > 1 {
		// Create parent
		err = MkdirAllOwner(path[0:j-1], perm, uid, gid)
		if err != nil {
			return err
		}
	}

	// Parent now exists; invoke Mkdir and use its result.
	err = os.Mkdir(path, perm)

	errChown := os.Chown(path, uid, gid)
	if errChown != nil {
		return errChown
	}

	if err != nil {
		// Handle arguments like "foo/." by
		// double-checking that directory doesn't exist.
		dir, err1 := os.Lstat(path)
		if err1 == nil && dir.IsDir() {
			return nil
		}

		return err
	}

	return nil
}

// HasKey returns true if map has key.
func HasKey[K comparable, V any](key K, m map[K]V) bool {
	_, found := m[key]

	return found
}

// ValueInSlice returns true if key is in list.
func ValueInSlice[T comparable](key T, list []T) bool {
	for _, entry := range list {
		if entry == key {
			return true
		}
	}

	return false
}

// StringPrefixInSlice returns true if any element in the list has the given prefix.
func StringPrefixInSlice(key string, list []string) bool {
	for _, entry := range list {
		if strings.HasPrefix(entry, key) {
			return true
		}
	}

	return false
}

// RemoveElementsFromSlice returns a slice equivalent to removing the given elements from the given list.
// Elements not present in the list are ignored.
func RemoveElementsFromSlice[T comparable](list []T, elements ...T) []T {
	for i := len(elements) - 1; i >= 0; i-- {
		element := elements[i]
		match := false
		for j := len(list) - 1; j >= 0; j-- {
			if element == list[j] {
				match = true
				list = append(list[:j], list[j+1:]...)
				break
			}
		}

		if match {
			elements = append(elements[:i], elements[i+1:]...)
		}
	}

	return list
}

// StringHasPrefix returns true if value has one of the supplied prefixes.
func StringHasPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// IsTrue returns true if value is "1", "true", "yes" or "on" (case insensitive).
func IsTrue(value string) bool {
	if value == "1" {
		return true
	}

	return ValueInSlice(strings.ToLower(value), []string{"true", "yes", "on"})
}

// IsTrueOrEmpty returns true if value is empty or if IsTrue() returns true.
func IsTrueOrEmpty(value string) bool {
	return value == "" || IsTrue(value)
}

// IsFalse returns true if value is "0", "false", "no" or "off" (case insensitive).
func IsFalse(value string) bool {
	if value == "0" {
		return true
	}

	return ValueInSlice(strings.ToLower(value), []string{"false", "no", "off"})
}

// IsFalseOrEmpty returns true if value is empty or if IsFalse() returns true.
func IsFalseOrEmpty(value string) bool {
	return value == "" || IsFalse(value)
}

// IsUserConfig returns true if the key starts with the prefix "user.".
func IsUserConfig(key string) bool {
	return strings.HasPrefix(key, "user.")
}

// StringMapHasStringKey returns true if any of the supplied keys are present in the map.
func StringMapHasStringKey(m map[string]string, keys ...string) bool {
	for _, k := range keys {
		_, ok := m[k]
		if ok {
			return true
		}
	}

	return false
}

// IsBlockdev determines if a given file mode represents a block device. It returns true
// if the mode has the os.ModeDevice bit set and the os.ModeCharDevice bit not set.
func IsBlockdev(fm os.FileMode) bool {
	return ((fm&os.ModeDevice != 0) && (fm&os.ModeCharDevice == 0))
}

// IsBlockdevPath checks if the given path corresponds to a block device.
func IsBlockdevPath(pathName string) bool {
	sb, err := os.Stat(pathName)
	if err != nil {
		return false
	}

	fm := sb.Mode()
	return ((fm&os.ModeDevice != 0) && (fm&os.ModeCharDevice == 0))
}

// DeepCopy copies src to dest by using encoding/gob so its not that fast.
func DeepCopy(src, dest any) error {
	buff := new(bytes.Buffer)
	enc := gob.NewEncoder(buff)
	dec := gob.NewDecoder(buff)
	err := enc.Encode(src)
	if err != nil {
		return err
	}

	err = dec.Decode(dest)
	if err != nil {
		return err
	}

	return nil
}

// RunningInUserNS checks if the current process is running inside a user namespace.
func RunningInUserNS() bool {
	file, err := os.Open("/proc/self/uid_map")
	if err != nil {
		return false
	}

	defer func() { _ = file.Close() }()

	buf := bufio.NewReader(file)
	l, _, err := buf.ReadLine()
	if err != nil {
		return false
	}

	line := string(l)
	var a, b, c int64
	_, _ = fmt.Sscanf(line, "%d %d %d", &a, &b, &c)
	if a == 0 && b == 0 && c == 4294967295 {
		return false
	}

	return true
}

// TextEditor opens a text editor with a temporary YAML file for editing configs.
func TextEditor(inPath string, inContent []byte) ([]byte, error) {
	var f *os.File
	var err error
	var path string

	// Detect the text editor to use
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
		if editor == "" {
			for _, p := range []string{"editor", "vi", "emacs", "nano"} {
				_, err := exec.LookPath(p)
				if err == nil {
					editor = p
					break
				}
			}
			if editor == "" {
				return []byte{}, fmt.Errorf("No text editor found, please set the EDITOR environment variable")
			}
		}
	}

	if inPath == "" {
		// If provided input, create a new file
		f, err = os.CreateTemp("", "lxd_editor_*.yaml")
		if err != nil {
			return []byte{}, err
		}

		revert := revert.New()
		defer revert.Fail()
		revert.Add(func() {
			_ = f.Close()
			_ = os.Remove(f.Name())
		})

		_, err = f.Write(inContent)
		if err != nil {
			return []byte{}, err
		}

		err = f.Close()
		if err != nil {
			return []byte{}, err
		}

		revert.Success()
		revert.Add(func() { _ = os.Remove(path) })
	} else {
		path = inPath
	}

	cmdParts := strings.Fields(editor)
	cmd := exec.Command(cmdParts[0], append(cmdParts[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return []byte{}, err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return []byte{}, err
	}

	return content, nil
}

// ParseMetadata converts the provided metadata into a map[string]any. An error is
// returned if the input is not a valid map or if the keys are not strings.
func ParseMetadata(metadata any) (map[string]any, error) {
	newMetadata := make(map[string]any)
	s := reflect.ValueOf(metadata)
	if !s.IsValid() {
		return nil, nil
	}

	if s.Kind() == reflect.Map {
		for _, k := range s.MapKeys() {
			if k.Kind() != reflect.String {
				return nil, fmt.Errorf("Invalid metadata provided (key isn't a string)")
			}

			newMetadata[k.String()] = s.MapIndex(k).Interface()
		}
	} else if s.Kind() == reflect.Ptr && !s.Elem().IsValid() {
		return nil, nil
	} else {
		return nil, fmt.Errorf("Invalid metadata provided (type isn't a map)")
	}

	return newMetadata, nil
}

// RemoveDuplicatesFromString removes all duplicates of the string 'sep'
// from the specified string 's'. Leading and trailing occurrences of sep
// are NOT removed (duplicate leading/trailing are). Performs poorly if
// there are multiple consecutive redundant separators.
func RemoveDuplicatesFromString(s string, sep string) string {
	if sep == "" {
		// Return the original string as it cannot have duplicates.
		return s
	}

	dup := sep + sep
	for strings.Contains(s, dup) {
		s = strings.Replace(s, dup, sep, -1)
	}

	return s
}

// RunError is the error from the RunCommand family of functions.
type RunError struct {
	cmd    string
	args   []string
	err    error
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func (e RunError) Error() string {
	if e.stderr.Len() == 0 {
		return fmt.Sprintf("Failed to run: %s %s: %v", e.cmd, strings.Join(e.args, " "), e.err)
	}

	return fmt.Sprintf("Failed to run: %s %s: %v (%s)", e.cmd, strings.Join(e.args, " "), e.err, strings.TrimSpace(e.stderr.String()))
}

func (e RunError) Unwrap() error {
	return e.err
}

// StdOut returns the stdout buffer.
func (e RunError) StdOut() *bytes.Buffer {
	return e.stdout
}

// StdErr returns the stdout buffer.
func (e RunError) StdErr() *bytes.Buffer {
	return e.stderr
}

// NewRunError returns new RunError.
func NewRunError(cmd string, args []string, err error, stdout *bytes.Buffer, stderr *bytes.Buffer) error {
	return RunError{
		cmd:    cmd,
		args:   args,
		err:    err,
		stdout: stdout,
		stderr: stderr,
	}
}

// RunCommandSplit runs a command with a supplied environment and optional arguments and returns the
// resulting stdout and stderr output as separate variables. If the supplied environment is nil then
// the default environment is used. If the command fails to start or returns a non-zero exit code
// then an error is returned containing the output of stderr too.
func RunCommandSplit(ctx context.Context, env []string, filesInherit []*os.File, name string, arg ...string) (stdOutput string, stdError string, err error) {
	cmd := exec.CommandContext(ctx, name, arg...)

	if env != nil {
		cmd.Env = env
	}

	if filesInherit != nil {
		cmd.ExtraFiles = filesInherit
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return stdout.String(), stderr.String(), NewRunError(name, arg, err, &stdout, &stderr)
	}

	return stdout.String(), stderr.String(), nil
}

// RunCommandContext runs a command with optional arguments and returns stdout. If the command fails to
// start or returns a non-zero exit code then an error is returned containing the output of stderr.
func RunCommandContext(ctx context.Context, name string, arg ...string) (string, error) {
	stdout, _, err := RunCommandSplit(ctx, nil, nil, name, arg...)
	return stdout, err
}

// RunCommand runs a command with optional arguments and returns stdout. If the command fails to
// start or returns a non-zero exit code then an error is returned containing the output of stderr.
// Deprecated: Use RunCommandContext.
func RunCommand(name string, arg ...string) (string, error) {
	stdout, _, err := RunCommandSplit(context.TODO(), nil, nil, name, arg...)
	return stdout, err
}

// RunCommandInheritFds runs a command with optional arguments and passes a set
// of file descriptors to the newly created process, returning stdout. If the
// command fails to start or returns a non-zero exit code then an error is
// returned containing the output of stderr.
func RunCommandInheritFds(ctx context.Context, filesInherit []*os.File, name string, arg ...string) (string, error) {
	stdout, _, err := RunCommandSplit(ctx, nil, filesInherit, name, arg...)
	return stdout, err
}

// RunCommandCLocale runs a command with a LC_ALL=C.UTF-8 and LANGUAGE=en environment set with optional arguments and
// returns stdout. If the command fails to start or returns a non-zero exit code then an error is
// returned containing the output of stderr.
func RunCommandCLocale(name string, arg ...string) (string, error) {
	stdout, _, err := RunCommandSplit(context.TODO(), append(os.Environ(), "LC_ALL=C.UTF-8", "LANGUAGE=en"), nil, name, arg...)
	return stdout, err
}

// RunCommandWithFds runs a command with supplied file descriptors.
func RunCommandWithFds(ctx context.Context, stdin io.Reader, stdout io.Writer, name string, arg ...string) error {
	cmd := exec.CommandContext(ctx, name, arg...)

	if stdin != nil {
		cmd.Stdin = stdin
	}

	if stdout != nil {
		cmd.Stdout = stdout
	}

	var buffer bytes.Buffer
	cmd.Stderr = &buffer

	err := cmd.Run()
	if err != nil {
		return NewRunError(name, arg, err, nil, &buffer)
	}

	return nil
}

// TryRunCommand runs the specified command up to 20 times with a 500ms delay between each call
// until it runs without an error. If after 20 times it is still failing then returns the error.
func TryRunCommand(name string, arg ...string) (string, error) {
	var err error
	var output string

	for i := 0; i < 20; i++ {
		output, err = RunCommand(name, arg...)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	return output, err
}

// TimeIsSet checks if the provided time is set to a valid timestamp. It returns false if the
// timestamp is zero or negative, and true otherwise.
func TimeIsSet(ts time.Time) bool {
	if ts.Unix() <= 0 {
		return false
	}

	if ts.UTC().Unix() <= 0 {
		return false
	}

	return true
}

// EscapePathFstab escapes a path fstab-style.
// This ensures that getmntent_r() and friends can correctly parse stuff like
// /some/wacky path with spaces /some/wacky target with spaces.
func EscapePathFstab(path string) string {
	r := strings.NewReplacer(
		" ", "\\040",
		"\t", "\\011",
		"\n", "\\012",
		"\\", "\\\\")
	return r.Replace(path)
}

// SetProgressMetadata updates the provided metadata map with progress information, including
// the percentage complete, data processed, and speed. It formats and stores these values for
// both API callers and CLI display purposes.
func SetProgressMetadata(metadata map[string]any, stage, displayPrefix string, percent, processed, speed int64) {
	progress := make(map[string]string)
	// stage, percent, speed sent for API callers.
	progress["stage"] = stage
	if processed > 0 {
		progress["processed"] = strconv.FormatInt(processed, 10)
	}

	if percent > 0 {
		progress["percent"] = strconv.FormatInt(percent, 10)
	}

	progress["speed"] = strconv.FormatInt(speed, 10)
	metadata["progress"] = progress

	// <stage>_progress with formatted text sent for lxc cli.
	if percent > 0 {
		if speed > 0 {
			metadata[stage+"_progress"] = fmt.Sprintf("%s: %d%% (%s/s)", displayPrefix, percent, units.GetByteSizeString(speed, 2))
		} else {
			metadata[stage+"_progress"] = fmt.Sprintf("%s: %d%%", displayPrefix, percent)
		}
	} else if processed > 0 {
		metadata[stage+"_progress"] = displayPrefix + ": " + units.GetByteSizeString(processed, 2) + " (" + units.GetByteSizeString(speed, 2) + "/s)"
	} else {
		metadata[stage+"_progress"] = displayPrefix + ": " + units.GetByteSizeString(speed, 2) + "/s"
	}
}

// DownloadFileHash downloads a file from the specified URL and writes it to the target,
// optionally verifying the file's hash using the provided hash function. The function
// either returns the number of bytes written or an error if the download fails or the
// hash does not match.
func DownloadFileHash(ctx context.Context, httpClient *http.Client, useragent string, progress func(progress ioprogress.ProgressData), canceler *cancel.HTTPRequestCanceller, filename string, url string, hash string, hashFunc hash.Hash, target io.WriteSeeker) (int64, error) {
	// Always seek to the beginning
	_, _ = target.Seek(0, io.SeekStart)

	var req *http.Request
	var err error

	// Prepare the download request
	if ctx != nil {
		req, err = http.NewRequestWithContext(ctx, "GET", url, nil)
	} else {
		req, err = http.NewRequest("GET", url, nil)
	}

	if err != nil {
		return -1, err
	}

	if useragent != "" {
		req.Header.Set("User-Agent", useragent)
	}

	// Perform the request
	r, doneCh, err := cancel.CancelableDownload(canceler, httpClient.Do, req)
	if err != nil {
		return -1, err
	}

	defer func() { _ = r.Body.Close() }()
	defer close(doneCh)

	if r.StatusCode != http.StatusOK {
		return -1, fmt.Errorf("Unable to fetch %s: %s", url, r.Status)
	}

	// Handle the data
	body := r.Body
	if progress != nil {
		body = &ioprogress.ProgressReader{
			ReadCloser: r.Body,
			Tracker: &ioprogress.ProgressTracker{
				Length: r.ContentLength,
				Handler: func(percent int64, speed int64) {
					if filename != "" {
						progress(ioprogress.ProgressData{Text: fmt.Sprintf("%s: %d%% (%s/s)", filename, percent, units.GetByteSizeString(speed, 2))})
					} else {
						progress(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2))})
					}
				},
			},
		}
	}

	var size int64

	if hashFunc != nil {
		size, err = io.Copy(io.MultiWriter(target, hashFunc), body)
		if err != nil {
			return -1, err
		}

		result := fmt.Sprintf("%x", hashFunc.Sum(nil))
		if result != hash {
			return -1, fmt.Errorf("Hash mismatch for %s: %s != %s", url, result, hash)
		}
	} else {
		size, err = io.Copy(target, body)
		if err != nil {
			return -1, err
		}
	}

	return size, nil
}

// ParseNumberFromFile reads a file content and tries to extract a number as int64 from it.
func ParseNumberFromFile(file string) (int64, error) {
	f, err := os.Open(file)
	if err != nil {
		return int64(0), err
	}

	defer func() { _ = f.Close() }()

	buf := make([]byte, 4096)
	n, err := f.Read(buf)
	if err != nil {
		return int64(0), err
	}

	str := strings.TrimSpace(string(buf[0:n]))
	nr, err := strconv.Atoi(str)
	if err != nil {
		return int64(0), err
	}

	return int64(nr), nil
}

// ReadSeeker is a composite type that embeds both io.Reader and io.Seeker.
type ReadSeeker struct {
	io.Reader
	io.Seeker
}

// NewReadSeeker creates a new ReadSeeker from the provided io.Reader and io.Seeker.
func NewReadSeeker(reader io.Reader, seeker io.Seeker) *ReadSeeker {
	return &ReadSeeker{Reader: reader, Seeker: seeker}
}

// Read reads from the embedded io.Reader into the provided slice of bytes.
func (r *ReadSeeker) Read(p []byte) (n int, err error) {
	return r.Reader.Read(p)
}

// Seek sets the offset for the next Read or Write operation, based on the reference point
// specified by whence.
func (r *ReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return r.Seeker.Seek(offset, whence)
}

// RenderTemplate renders a pongo2 template.
func RenderTemplate(template string, ctx pongo2.Context) (string, error) {
	// Load template from string
	tpl, err := pongo2.FromString("{% autoescape off %}" + template + "{% endautoescape %}")
	if err != nil {
		return "", err
	}

	// Get rendered template
	ret, err := tpl.Execute(ctx)
	if err != nil {
		return ret, err
	}

	// Looks like we're nesting templates so run pongo again
	if strings.Contains(ret, "{{") || strings.Contains(ret, "{%") {
		return RenderTemplate(ret, ctx)
	}

	return ret, err
}

// GetExpiry returns the expiry date based on the reference date and a length of time.
// The length of time format is "<integer>(S|M|H|d|w|m|y)", and can contain multiple such fields, e.g.
// "1d 3H" (1 day and 3 hours).
func GetExpiry(refDate time.Time, s string) (time.Time, error) {
	expr := strings.TrimSpace(s)

	if expr == "" {
		return time.Time{}, nil
	}

	re, err := regexp.Compile(`^(\d+)(S|M|H|d|w|m|y)$`)
	if err != nil {
		return time.Time{}, err
	}

	expiry := map[string]int{
		"S": 0,
		"M": 0,
		"H": 0,
		"d": 0,
		"w": 0,
		"m": 0,
		"y": 0,
	}

	values := strings.Split(expr, " ")

	if len(values) == 0 {
		return time.Time{}, nil
	}

	for _, value := range values {
		fields := re.FindStringSubmatch(value)
		if fields == nil {
			return time.Time{}, fmt.Errorf("Invalid expiry expression")
		}

		if expiry[fields[2]] > 0 {
			// We don't allow fields to be set multiple times
			return time.Time{}, fmt.Errorf("Invalid expiry expression")
		}

		val, err := strconv.Atoi(fields[1])
		if err != nil {
			return time.Time{}, err
		}

		expiry[fields[2]] = val
	}

	t := refDate.AddDate(expiry["y"], expiry["m"], expiry["d"]+expiry["w"]*7).Add(
		time.Hour*time.Duration(expiry["H"]) + time.Minute*time.Duration(expiry["M"]) + time.Second*time.Duration(expiry["S"]))

	return t, nil
}

// InSnap returns true if we're running inside the LXD snap.
func InSnap() bool {
	// Detect the snap.
	_, snapPath := os.LookupEnv("SNAP")
	snapName := os.Getenv("SNAP_NAME")
	if snapPath && snapName == "lxd" {
		return true
	}

	return false
}

// JoinUrls returns the join of the input urls/paths sanitized.
func JoinUrls(baseURL string, p string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	u.Path = path.Join(u.Path, p)
	return u.String(), nil
}

// SplitNTrimSpace returns result of strings.SplitN() and then strings.TrimSpace() on each element.
// Accepts nilIfEmpty argument which if true, will return nil slice if s is empty (after trimming space).
func SplitNTrimSpace(s string, sep string, n int, nilIfEmpty bool) []string {
	if nilIfEmpty && strings.TrimSpace(s) == "" {
		return nil
	}

	parts := strings.SplitN(s, sep, n)

	for i, v := range parts {
		parts[i] = strings.TrimSpace(v)
	}

	return parts
}

// JoinTokenDecode decodes a base64 and JSON encoded join token.
func JoinTokenDecode(input string) (*api.ClusterMemberJoinToken, error) {
	joinTokenJSON, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return nil, err
	}

	var j api.ClusterMemberJoinToken
	err = json.Unmarshal(joinTokenJSON, &j)
	if err != nil {
		return nil, err
	}

	if j.ServerName == "" {
		return nil, fmt.Errorf("No server name in join token")
	}

	if len(j.Addresses) < 1 {
		return nil, fmt.Errorf("No cluster member addresses in join token")
	}

	if j.Secret == "" {
		return nil, fmt.Errorf("No secret in join token")
	}

	if j.Fingerprint == "" {
		return nil, fmt.Errorf("No certificate fingerprint in join token")
	}

	return &j, nil
}

// TargetDetect returns either target node or group based on the provided prefix:
// An invocation with `target=h1` returns "h1", "" and `target=@g1` returns "", "g1".
func TargetDetect(target string) (targetNode string, targetGroup string) {
	if strings.HasPrefix(target, "@") {
		targetGroup = strings.TrimPrefix(target, "@")
	} else {
		targetNode = target
	}

	return targetNode, targetGroup
}

// ApplyDeviceOverrides handles the logic for applying device overrides.
// Receives the profile and local devices and the device overrides.
// Returns the resulting list of devices.
func ApplyDeviceOverrides(localDevices map[string]map[string]string, profileDevices map[string]map[string]string, deviceOverrides map[string]map[string]string) (map[string]map[string]string, error) {
	// Allow setting device overrides.
	for deviceName := range deviceOverrides {
		_, isLocalDevice := localDevices[deviceName]
		if isLocalDevice {
			// Apply overrides to local device.
			for k, v := range deviceOverrides[deviceName] {
				localDevices[deviceName][k] = v
			}
		} else {
			// Check device exists in expanded profile devices.
			profileDeviceConfig, found := profileDevices[deviceName]
			if !found {
				return nil, fmt.Errorf("Cannot override config for device %q: Device not found in profile devices", deviceName)
			}

			for k, v := range deviceOverrides[deviceName] {
				profileDeviceConfig[k] = v
			}

			localDevices[deviceName] = profileDeviceConfig
		}
	}

	return localDevices, nil
}

// IsMicroOVNUsed returns whether the current LXD deployment is using a built-in openvswitch
// or is connected to MicroOVN, which in this case, would make `/run/openvswitch` a symlink to
// `/var/snap/lxd/common/microovn/chassis/switch`.
func IsMicroOVNUsed() bool {
	targetPath, err := os.Readlink("/run/openvswitch")
	if err == nil && strings.HasSuffix(targetPath, "/microovn/chassis/switch") {
		return true
	}

	return false
}

// EncodeRemoteAbsPathWithNonExistingDir splits a path into existing and non-existing parts and encodes it.
func EncodeRemoteAbsPathWithNonExistingDir(c *sftp.Client, absPath string) (string, error) {
	if !filepath.IsAbs(absPath) {
		return "", fmt.Errorf("Path is not absolute")
	}

	// First, try to resolve the path (handles /./, /../, symlinks, etc.)
	// A user might provide a path with /./ or /../ in it, so we need to resolve it.
	// Note that this does not return an error if the path doesn't exist. We'll handle that later with Lstat.
	resolvedExistingPath, err := c.RealPath(absPath)
	if err != nil {
		return "", fmt.Errorf("Failed to resolve path while encoding path with non existing directory %q: %w", absPath, err)
	}

	// Now, check the path existence
	_, err = c.Lstat(resolvedExistingPath)
	if err == nil {
		// The path exists. We can just return it as is.
		return resolvedExistingPath, nil
	}

	// If there is an error, it should be because the path doesn't exist.
	if !os.IsNotExist(err) {
		return "", err
	}

	// The path doesn't exist. We need to split it into existing and non-existing parts,
	// in order to be able to create the non-existing parts later.
	currentPath := string(os.PathSeparator)
	parts := strings.Split(resolvedExistingPath, string(os.PathSeparator))[1:]

	// Find the split point between existing and non-existing parts.
	// We start iterating from the root of the provided path, and for each part, we check if it exists.
	splitIndex := -1
	for i, part := range parts {
		if part == "" {
			continue
		}

		if part == "." || part == ".." {
			currentPath = filepath.Join(currentPath, part)
			continue
		}

		testPath := filepath.Join(currentPath, part)
		p, err := c.Lstat(testPath)
		if err != nil {
			if os.IsNotExist(err) {
				splitIndex = i
				break
			}

			return "", err
		}

		if p.Mode()&os.ModeSymlink != 0 {
			// This is a symlink, so we need to resolve it first.
			resolvedPath, err := c.ReadLink(testPath)
			if err != nil {
				return "", err
			}

			currentPath = resolvedPath
		} else {
			currentPath = testPath
		}
	}

	// Add a '/./' marker to separate the existing and non-existing parts
	sep := string(os.PathSeparator) + "." + string(os.PathSeparator)
	finalPath := string(os.PathSeparator) + filepath.Join(parts[:splitIndex]...) + sep + filepath.Join(parts[splitIndex:]...)

	// Check if the final path is 'clean' (e.g, `/a/b/./../c/d` is valid but `/../../..` is invalid since the resolved path goes out of the root).
	cleanedPath := path.Clean(finalPath)
	if strings.HasPrefix(cleanedPath, "../") || strings.HasPrefix(cleanedPath, "/..") || !strings.HasPrefix(cleanedPath, "/") {
		return "", fmt.Errorf("Invalid cleaned final path, %q", cleanedPath)
	}

	return finalPath, nil
}

// DecodeRemoteAbsPathWithNonExistingDir splits an encoded path back into its parts.
func DecodeRemoteAbsPathWithNonExistingDir(encodedPath string) (existing, nonExisting string) {
	parts := strings.Split(encodedPath, "/./")
	if len(parts) == 1 {
		return parts[0], ""
	}

	return parts[0], parts[1]
}
