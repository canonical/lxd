package shared

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/tls"
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
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const SnapshotDelimiter = "/"
const DefaultPort = "8443"

func GetFileStat(p string) (uid int, gid int, major int, minor int,
	inode uint64, nlink int, err error) {
	var stat syscall.Stat_t
	err = syscall.Lstat(p, &stat)
	if err != nil {
		return
	}
	uid = int(stat.Uid)
	gid = int(stat.Gid)
	inode = uint64(stat.Ino)
	nlink = int(stat.Nlink)
	major = -1
	minor = -1
	if stat.Mode&syscall.S_IFBLK != 0 || stat.Mode&syscall.S_IFCHR != 0 {
		major = int(stat.Rdev / 256)
		minor = int(stat.Rdev % 256)
	}

	return
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

func IsMountPoint(name string) bool {
	stat, err := os.Stat(name)
	if err != nil {
		return false
	}

	rootStat, err := os.Lstat(name + "/..")
	if err != nil {
		return false
	}
	// If the directory has the same device as parent, then it's not a mountpoint.
	return stat.Sys().(*syscall.Stat_t).Dev != rootStat.Sys().(*syscall.Stat_t).Dev
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

func ParseLXDFileHeaders(headers http.Header) (uid int, gid int, mode os.FileMode) {
	uid, err := strconv.Atoi(headers.Get("X-LXD-uid"))
	if err != nil {
		uid = 0
	}

	gid, err = strconv.Atoi(headers.Get("X-LXD-gid"))
	if err != nil {
		gid = 0
	}

	/* Allow people to send stuff with a leading 0 for octal or a regular
	 * int that represents the perms when redered in octal. */
	rawMode, err := strconv.ParseInt(headers.Get("X-LXD-mode"), 0, 0)
	if err != nil {
		rawMode = 0644
	}
	mode = os.FileMode(rawMode)

	return uid, gid, mode
}

func ReadToJSON(r io.Reader, req interface{}) error {
	buf, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	return json.Unmarshal(buf, req)
}

func ReaderToChannel(r io.Reader) <-chan []byte {
	ch := make(chan ([]byte))

	go func() {
		for {
			/* io.Copy uses a 32KB buffer, so we might as well too. */
			buf := make([]byte, 32*1024)
			nr, err := r.Read(buf)
			if nr > 0 {
				ch <- buf[0:nr]
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

func GetTLSConfig(certf string, keyf string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certf, keyf)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		ClientAuth:         tls.RequireAnyClientCert,
		Certificates:       []tls.Certificate{cert},
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256},
		PreferServerCipherSuites: true,
	}

	tlsConfig.BuildNameToCertificate()

	return tlsConfig, nil
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

func ReadLastNLines(f *os.File, lines int) (string, error) {
	if lines <= 0 {
		return "", fmt.Errorf("invalid line count")
	}

	stat, err := f.Stat()
	if err != nil {
		return "", err
	}

	data, err := syscall.Mmap(int(f.Fd()), 0, int(stat.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return "", err
	}
	defer syscall.Munmap(data)

	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == '\n' {
			lines--
		}

		if lines < 0 {
			return string(data[i+1 : len(data)]), nil
		}
	}

	return string(data), nil
}

func SetSize(fd int, width int, height int) (err error) {
	var dimensions [4]uint16
	dimensions[0] = uint16(height)
	dimensions[1] = uint16(width)

	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(&dimensions)), 0, 0, 0); err != 0 {
		return err
	}
	return nil
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

func IntInSlice(key int, list []int) bool {
	for _, entry := range list {
		if entry == key {
			return true
		}
	}
	return false
}

/*
 * returns 1 if path is mounted shared:
 * returns 0 if path is not listed
 * returns -1 if path is explicitly mounted as not-shared
 */
func isSharedMount(file *os.File, pathName string) int {
	_, err := file.Seek(0, 0)
	if err != nil {
		Debugf("Error rewinding mountinfo file: %s\n", err)
		return 0
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		rows := strings.Fields(line)
		if rows[3] != pathName || rows[4] != pathName {
			continue
		}
		if strings.HasPrefix(rows[6], "shared:") {
			return 1
		} else {
			return -1
		}
	}
	return 0
}

func IsSharedMount(pathName string) bool {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer file.Close()

	switch isSharedMount(file, pathName) {
	case 1:
		return true
	default:
		return false
	}
}

func IsOnSharedMount(pathName string) bool {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer file.Close()

	for {
		switch isSharedMount(file, pathName) {
		case 1:
			return true
		case -1:
			return false
		}
		if pathName == "/" || pathName == "." {
			return false
		}
		pathName = filepath.Dir(pathName)
	}
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
	return strings.TrimSpace(string(out)), err
}
