package shared

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

const SnapshotDelimiter = "/"
const DefaultPort = "8443"

// AddSlash adds a slash to the end of paths if they don't already have one.
// This can be useful for rsyncing things, since rsync has behavior present on
// the presence or absence of a trailing slash.
func AddSlash(path string) string {
	if path[len(path)-1] != '/' {
		return path + "/"
	}

	return path
}

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
	defer f.Close()

	// read in ONLY one file
	_, err = f.Readdir(1)

	// and if the file is EOF... well, the dir is empty.
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

// IsDir returns true if the given path is a directory.
func IsDir(name string) bool {
	stat, err := os.Lstat(name)
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
	logDir := "/var/cache/lxd"
	if varDir != "" {
		logDir = filepath.Join(varDir, "cache")
	}
	items := []string{logDir}
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

func ParseLXDFileHeaders(headers http.Header) (uid int, gid int, mode int, type_ string) {
	uid, err := strconv.Atoi(headers.Get("X-LXD-uid"))
	if err != nil {
		uid = -1
	}

	gid, err = strconv.Atoi(headers.Get("X-LXD-gid"))
	if err != nil {
		gid = -1
	}

	mode, err = strconv.Atoi(headers.Get("X-LXD-mode"))
	if err != nil {
		mode = -1
	} else {
		rawMode, err := strconv.ParseInt(headers.Get("X-LXD-mode"), 0, 0)
		if err == nil {
			mode = int(os.FileMode(rawMode) & os.ModePerm)
		}
	}

	type_ = headers.Get("X-LXD-type")
	/* backwards compat: before "type" was introduced, we could only
	 * manipulate files
	 */
	if type_ == "" {
		type_ = "file"
	}

	return uid, gid, mode, type_
}

func ReadToJSON(r io.Reader, req interface{}) error {
	buf, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	return json.Unmarshal(buf, req)
}

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

// Returns a random base64 encoded string from crypto/rand.
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

func SplitExt(fpath string) (string, string) {
	b := path.Base(fpath)
	ext := path.Ext(fpath)
	return b[:len(b)-len(ext)], ext
}

func AtoiEmptyDefault(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}

	return strconv.Atoi(s)
}

func ReadStdin() ([]byte, error) {
	buf := bufio.NewReader(os.Stdin)
	line, _, err := buf.ReadLine()
	if err != nil {
		return nil, err
	}
	return line, nil
}

func WriteAll(w io.Writer, buf []byte) error {
	return WriteAllBuf(w, bytes.NewBuffer(buf))
}

func WriteAllBuf(w io.Writer, buf *bytes.Buffer) error {
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

// FileMove tries to move a file by using os.Rename,
// if that fails it tries to copy the file and remove the source.
func FileMove(oldPath string, newPath string) error {
	if err := os.Rename(oldPath, newPath); err == nil {
		return nil
	}

	if err := FileCopy(oldPath, newPath); err != nil {
		return err
	}

	os.Remove(oldPath)

	return nil
}

// FileCopy copies a file, overwriting the target if it exists.
func FileCopy(source string, dest string) error {
	s, err := os.Open(source)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dest)
	if err != nil {
		if os.IsExist(err) {
			d, err = os.OpenFile(dest, os.O_WRONLY, 0700)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	defer d.Close()

	_, err = io.Copy(d, s)
	return err
}

type BytesReadCloser struct {
	Buf *bytes.Buffer
}

func (r BytesReadCloser) Read(b []byte) (n int, err error) {
	return r.Buf.Read(b)
}

func (r BytesReadCloser) Close() error {
	/* no-op since we're in memory */
	return nil
}

func IsSnapshot(name string) bool {
	return strings.Contains(name, SnapshotDelimiter)
}

func ExtractSnapshotName(name string) string {
	return strings.SplitN(name, SnapshotDelimiter, 2)[1]
}

func ReadDir(p string) ([]string, error) {
	ents, err := ioutil.ReadDir(p)
	if err != nil {
		return []string{}, err
	}

	var ret []string
	for _, ent := range ents {
		ret = append(ret, ent.Name())
	}
	return ret, nil
}

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

	err_chown := os.Chown(path, uid, gid)
	if err_chown != nil {
		return err_chown
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

func StringInSlice(key string, list []string) bool {
	for _, entry := range list {
		if entry == key {
			return true
		}
	}
	return false
}

func IntInSlice(key int, list []int) bool {
	for _, entry := range list {
		if entry == key {
			return true
		}
	}
	return false
}

func Int64InSlice(key int64, list []int64) bool {
	for _, entry := range list {
		if entry == key {
			return true
		}
	}
	return false
}

func IsTrue(value string) bool {
	if StringInSlice(strings.ToLower(value), []string{"true", "1", "yes", "on"}) {
		return true
	}

	return false
}

func IsOnSharedMount(pathName string) (bool, error) {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	defer file.Close()

	absPath, err := filepath.Abs(pathName)
	if err != nil {
		return false, err
	}

	expPath, err := os.Readlink(absPath)
	if err != nil {
		expPath = absPath
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		rows := strings.Fields(line)

		if rows[4] != expPath {
			continue
		}

		if strings.HasPrefix(rows[6], "shared:") {
			return true, nil
		} else {
			return false, nil
		}
	}

	return false, nil
}

func IsBlockdev(fm os.FileMode) bool {
	return ((fm&os.ModeDevice != 0) && (fm&os.ModeCharDevice == 0))
}

func IsBlockdevPath(pathName string) bool {
	sb, err := os.Stat(pathName)
	if err != nil {
		return false
	}

	fm := sb.Mode()
	return ((fm&os.ModeDevice != 0) && (fm&os.ModeCharDevice == 0))
}

func BlockFsDetect(dev string) (string, error) {
	out, err := exec.Command("blkid", "-s", "TYPE", "-o", "value", dev).Output()
	if err != nil {
		return "", fmt.Errorf("Failed to run blkid on: %s", dev)
	}

	return strings.TrimSpace(string(out)), nil
}

// DeepCopy copies src to dest by using encoding/gob so its not that fast.
func DeepCopy(src, dest interface{}) error {
	buff := new(bytes.Buffer)
	enc := gob.NewEncoder(buff)
	dec := gob.NewDecoder(buff)
	if err := enc.Encode(src); err != nil {
		return err
	}

	if err := dec.Decode(dest); err != nil {
		return err
	}

	return nil
}

func RunningInUserNS() bool {
	file, err := os.Open("/proc/self/uid_map")
	if err != nil {
		return false
	}
	defer file.Close()

	buf := bufio.NewReader(file)
	l, _, err := buf.ReadLine()
	if err != nil {
		return false
	}

	line := string(l)
	var a, b, c int64
	fmt.Sscanf(line, "%d %d %d", &a, &b, &c)
	if a == 0 && b == 0 && c == 4294967295 {
		return false
	}
	return true
}

func ValidHostname(name string) bool {
	// Validate length
	if len(name) < 1 || len(name) > 63 {
		return false
	}

	// Validate first character
	if strings.HasPrefix(name, "-") {
		return false
	}

	if _, err := strconv.Atoi(string(name[0])); err == nil {
		return false
	}

	// Validate last character
	if strings.HasSuffix(name, "-") {
		return false
	}

	// Validate the character set
	match, _ := regexp.MatchString("^[-a-zA-Z0-9]*$", name)
	if !match {
		return false
	}

	return true
}

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
				return []byte{}, fmt.Errorf("No text editor found, please set the EDITOR environment variable.")
			}
		}
	}

	if inPath == "" {
		// If provided input, create a new file
		f, err = ioutil.TempFile("", "lxd_editor_")
		if err != nil {
			return []byte{}, err
		}

		if err = f.Chmod(0600); err != nil {
			f.Close()
			os.Remove(f.Name())
			return []byte{}, err
		}

		f.Write(inContent)
		f.Close()

		path = f.Name()
		defer os.Remove(path)
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

	content, err := ioutil.ReadFile(path)
	if err != nil {
		return []byte{}, err
	}

	return content, nil
}

func ParseMetadata(metadata interface{}) (map[string]interface{}, error) {
	newMetadata := make(map[string]interface{})
	s := reflect.ValueOf(metadata)
	if !s.IsValid() {
		return nil, nil
	}

	if s.Kind() == reflect.Map {
		for _, k := range s.MapKeys() {
			if k.Kind() != reflect.String {
				return nil, fmt.Errorf("Invalid metadata provided (key isn't a string).")
			}
			newMetadata[k.String()] = s.MapIndex(k).Interface()
		}
	} else if s.Kind() == reflect.Ptr && !s.Elem().IsValid() {
		return nil, nil
	} else {
		return nil, fmt.Errorf("Invalid metadata provided (type isn't a map).")
	}

	return newMetadata, nil
}

// Parse a size string in bytes (e.g. 200kB or 5GB) into the number of bytes it
// represents. Supports suffixes up to EB. "" == 0.
func ParseByteSizeString(input string) (int64, error) {
	if input == "" {
		return 0, nil
	}

	if len(input) < 3 {
		return -1, fmt.Errorf("Invalid value: %s", input)
	}

	isInBytes := strings.HasSuffix(strings.ToUpper(input), "BYTES")

	// Extract the suffix
	suffix := input[len(input)-2:]
	if isInBytes {
		suffix = input[len(input)-len("BYTES"):]
	}

	// Extract the value
	value := input[0 : len(input)-2]
	if isInBytes {
		value = input[0 : len(input)-len("BYTES")]
	}

	// COMMENT(brauner): Remove any whitespace that might have been left
	// between the value and the unit.
	value = strings.TrimRight(value, " ")

	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("Invalid integer: %s", input)
	}

	if valueInt < 0 {
		return -1, fmt.Errorf("Invalid value: %d", valueInt)
	}

	if isInBytes {
		return valueInt, nil
	}

	// Figure out the multiplicator
	multiplicator := int64(0)
	switch strings.ToUpper(suffix) {
	case "KB":
		multiplicator = 1024
	case "MB":
		multiplicator = 1024 * 1024
	case "GB":
		multiplicator = 1024 * 1024 * 1024
	case "TB":
		multiplicator = 1024 * 1024 * 1024 * 1024
	case "PB":
		multiplicator = 1024 * 1024 * 1024 * 1024 * 1024
	case "EB":
		multiplicator = 1024 * 1024 * 1024 * 1024 * 1024 * 1024
	default:
		return -1, fmt.Errorf("Unsupported suffix: %s", suffix)
	}

	return valueInt * multiplicator, nil
}

// Parse a size string in bits (e.g. 200kbit or 5Gbit) into the number of bits
// it represents. Supports suffixes up to Ebit. "" == 0.
func ParseBitSizeString(input string) (int64, error) {
	if input == "" {
		return 0, nil
	}

	if len(input) < 5 {
		return -1, fmt.Errorf("Invalid value: %s", input)
	}

	// Extract the suffix
	suffix := input[len(input)-4:]

	// Extract the value
	value := input[0 : len(input)-4]
	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("Invalid integer: %s", input)
	}

	if valueInt < 0 {
		return -1, fmt.Errorf("Invalid value: %d", valueInt)
	}

	// Figure out the multiplicator
	multiplicator := int64(0)
	switch suffix {
	case "kbit":
		multiplicator = 1000
	case "Mbit":
		multiplicator = 1000 * 1000
	case "Gbit":
		multiplicator = 1000 * 1000 * 1000
	case "Tbit":
		multiplicator = 1000 * 1000 * 1000 * 1000
	case "Pbit":
		multiplicator = 1000 * 1000 * 1000 * 1000 * 1000
	case "Ebit":
		multiplicator = 1000 * 1000 * 1000 * 1000 * 1000 * 1000
	default:
		return -1, fmt.Errorf("Unsupported suffix: %s", suffix)
	}

	return valueInt * multiplicator, nil
}

func GetByteSizeString(input int64, precision uint) string {
	if input < 1024 {
		return fmt.Sprintf("%d bytes", input)
	}

	value := float64(input)

	for _, unit := range []string{"kB", "MB", "GB", "TB", "PB", "EB"} {
		value = value / 1024
		if value < 1024 {
			return fmt.Sprintf("%.*f%s", precision, value, unit)
		}
	}

	return fmt.Sprintf("%.*fEB", precision, value)
}

// RemoveDuplicatesFromString removes all duplicates of the string 'sep'
// from the specified string 's'.  Leading and trailing occurrences of sep
// are NOT removed (duplicate leading/trailing are).  Performs poorly if
// there are multiple consecutive redundant separators.
func RemoveDuplicatesFromString(s string, sep string) string {
	dup := sep + sep
	for s = strings.Replace(s, dup, sep, -1); strings.Contains(s, dup); s = strings.Replace(s, dup, sep, -1) {

	}
	return s
}

func RunCommand(name string, arg ...string) error {
	output, err := exec.Command(name, arg...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Failed to run: %s %s: %s", name, strings.Join(arg, " "), strings.TrimSpace(string(output)))
	}

	return nil
}
