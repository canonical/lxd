package rsync

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/linux"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/logger"
)

// Debug controls additional debugging in rsync output.
var Debug bool

// RunWrapper is an optional function that's used to wrap rsync, useful for confinement like AppArmor.
// It returns a cleanup function that will close the wrapper's environment, and should be called after the command has completed.
var RunWrapper func(cmd *exec.Cmd, source string, destination string) (func(), error)

// rsync is a wrapper for the rsync command which will respect RunWrapper.
func rsync(args ...string) (string, error) {
	if len(args) < 2 {
		return "", errors.New("rsync call expects a minimum of two arguments (source and destination)")
	}

	// Setup the command.
	cmd := exec.Command("rsync", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// Call the wrapper if defined.
	if RunWrapper != nil {
		source := args[len(args)-2]
		destination := args[len(args)-1]

		cleanup, err := RunWrapper(cmd, source, destination)
		if err != nil {
			return "", err
		}

		defer cleanup()
	}

	// Run the command.
	err := cmd.Run()
	if err != nil {
		return stdout.String(), shared.NewRunError("rsync", args, err, &stdout, &stderr)
	}

	return stdout.String(), nil
}

// assertSafePath returns an error if the provided path is not absolute or if it
// contains a ".." segment. rsync treats arguments beginning with "-" as
// options, so source and destination paths must always be absolute (and
// therefore begin with "/"). Rejecting ".." segments additionally prevents a
// crafted path from traversing outside of its intended directory.
func assertSafePath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("Rsync path %q must be absolute", path)
	}

	for segment := range strings.SplitSeq(path, "/") {
		if segment == ".." {
			return fmt.Errorf("Rsync path %q must not contain %q segments", path, "..")
		}
	}

	return nil
}

// rsyncSafeName matches a name that is safe to embed in the rsync "-e" remote
// shell command. It requires a non-empty string containing no Unicode
// separator (\p{Z}) or other (\p{C}) characters. This rejects ASCII and
// Unicode whitespace, control characters, and format characters such as
// bidirectional overrides (e.g. U+202E), matching the Unicode-aware validation
// applied to volume names elsewhere rather than only ASCII.
var rsyncSafeName = regexp.MustCompile(`^[^\p{Z}\p{C}]+$`)

// assertSafeName returns an error if the provided name is not safe to embed in
// the rsync "-e" remote shell command. The name is passed verbatim as a
// positional argument to "lxd netcat", which rsync tokenizes on whitespace and
// executes without a shell, and which also uses the name to build a log path
// via filepath.Join (see lxd/main_netcat.go). The name must therefore:
//   - be non-empty and free of whitespace and control characters, so it cannot
//     inject additional arguments into the remote command;
//   - be a local path, so it cannot escape the intended log directory.
//
// Snapshot delimiters ("/") are still allowed (e.g. "vol/snap"). A leading "-"
// is safe because Send passes "--" to "lxd netcat" before the positional
// arguments, so cobra never parses the name as a flag.
func assertSafeName(name string) error {
	if !rsyncSafeName.MatchString(name) {
		return fmt.Errorf("Rsync name %q must be non-empty and must not contain whitespace or control characters", name)
	}

	// filepath.IsLocal rejects absolute and empty names as well as names that
	// traverse outside of their directory (via ".."), which would otherwise
	// let a crafted name escape the log directory used by "lxd netcat".
	if !filepath.IsLocal(name) {
		return fmt.Errorf("Rsync name %q must be a local path", name)
	}

	return nil
}

func runRsync(source string, dest string, bwlimit string, xattrs bool, rsyncArgs ...string) (string, error) {
	err := assertSafePath(source)
	if err != nil {
		return "", err
	}

	err = assertSafePath(dest)
	if err != nil {
		return "", err
	}

	err = os.MkdirAll(dest, 0755)
	if err != nil {
		return "", err
	}

	rsyncVerbosity := "-q"
	if Debug {
		rsyncVerbosity = "-vi"
	}

	args := []string{
		"-a",
		"-HA",
		"--sparse",
		"--devices",
		"--delete",
		"--numeric-ids",
		// Checks for file modifications on nanoseconds granularity.
		"--modify-window=-1",
	}

	if xattrs {
		args = append(args, "--xattrs", "--filter=-x security.selinux")
	}

	if bwlimit != "" {
		args = append(args, "--bwlimit", bwlimit)
	}

	if len(rsyncArgs) > 0 {
		args = append(args, rsyncArgs...)
	}

	// Add an end of options marker ("--") so that the source and destination
	// paths are never interpreted as rsync options, even if they begin with a
	// "-".
	args = append(args,
		rsyncVerbosity,
		"--",
		source,
		dest)

	msg, err := rsync(args...)
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Unwrap().(*exec.ExitError)
			if ok {
				if exitError.ExitCode() == 24 {
					return msg, nil
				}
			}
		}

		return msg, err
	}

	return msg, nil
}

// LocalCopy copies a directory using rsync (with the --devices option).
func LocalCopy(source string, dest string, bwlimit string, xattrs bool, rsyncArgs ...string) (string, error) {
	return runRsync(shared.AddSlash(source), dest, bwlimit, xattrs, rsyncArgs...)
}

// CopyFile copies a single file using rsync (with the --devices option).
func CopyFile(source string, dest string, bwlimit string, xattrs bool, rsyncArgs ...string) (string, error) {
	return runRsync(strings.TrimSuffix(source, "/"), dest, bwlimit, xattrs, rsyncArgs...)
}

// Send sets up the sending half of an rsync, to recursively send the
// directory pointed to by path over the websocket.
func Send(name string, path string, conn io.ReadWriteCloser, wrapper ioprogress.ReaderWrapper, features []string, bwlimit string, execPath string, rsyncArgs ...string) error {
	err := assertSafeName(name)
	if err != nil {
		return err
	}

	err = assertSafePath(path)
	if err != nil {
		return err
	}

	/*
	 * The way rsync works, it invokes a subprocess that does the actual
	 * talking (given to it by a -E argument). Since there isn't an easy
	 * way for us to capture this process' stdin/stdout, we just use netcat
	 * and write to/from a unix socket.
	 *
	 * In principle we don't need this socket. It seems to me that some
	 * clever invocation of rsync --server --sender and usage of that
	 * process' stdin/stdout could work around the need for this socket,
	 * but I couldn't get it to work. Another option would be to look at
	 * the spawned process' first child and read/write from its
	 * stdin/stdout, but that also seemed messy. In any case, this seems to
	 * work just fine.
	 */
	auds := "@lxd/" + uuid.New().String()
	// We simply copy a part of the uuid if it's longer than the allowed
	// maximum. That should be safe enough for our purposes.
	if len(auds) > linux.ABSTRACT_UNIX_SOCK_LEN-1 {
		auds = auds[:linux.ABSTRACT_UNIX_SOCK_LEN-1]
	}

	l, err := net.Listen("unix", auds)
	if err != nil {
		return err
	}

	defer func() { _ = l.Close() }()

	/*
	 * Here, the path /tmp/foo is ignored. Since we specify localhost,
	 * rsync thinks we are syncing to a remote host (in this case, the
	 * other end of the lxd websocket), and so the path specified on the
	 * --server instance of rsync takes precedence.
	 */
	// Place cobra's end-of-flags marker ("--") before the positional arguments
	// so that neither the name nor the arguments rsync appends (the remote host
	// and rsync's own --server options) are parsed as flags by "lxd netcat",
	// even when the name begins with a "-".
	rsyncCmd := fmt.Sprintf("%s netcat -- %s %s", execPath, auds, name)

	args := []string{
		"-ar",
		"--devices",
		"--numeric-ids",
		"--partial",
		"--sparse",
	}

	if bwlimit != "" {
		args = append(args, "--bwlimit", bwlimit)
	}

	if len(features) > 0 {
		args = append(args, rsyncFeatureArgs(features)...)
	}

	if len(rsyncArgs) > 0 {
		args = append(args, rsyncArgs...)
	}

	// The remote shell command (-e) must be passed as an option, so it has to
	// come before the end of options marker ("--") below.
	args = append(args, "-e", rsyncCmd)

	// Add an end of options marker ("--") so that the source path is never
	// interpreted as an rsync option, even if it begins with a "-".
	args = append(args, "--", path, "localhost:/tmp/foo")

	cmd := exec.Command("rsync", args...)

	// Call the wrapper if defined.
	if RunWrapper != nil {
		cleanup, err := RunWrapper(cmd, path, "")
		if err != nil {
			return err
		}

		defer cleanup()
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	var ncConn *net.Conn
	chConn := make(chan *net.Conn, 1)

	go func() {
		ncConn, err := l.Accept()
		if err != nil {
			chConn <- nil
			return
		}

		chConn <- &ncConn
	}()

	select {
	case ncConn = <-chConn:
		if ncConn == nil {
			output, _ := io.ReadAll(stderr)
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("Failed connecting to rsync socket (%s)", string(output))
		}

	case <-time.After(10 * time.Second):
		output, _ := io.ReadAll(stderr)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("rsync failed spawning after 10s (%s)", string(output))
	}

	// Setup progress tracker.
	netcatConn := *ncConn
	readNetcatPipe := io.ReadCloser(netcatConn)
	if wrapper != nil {
		readNetcatPipe = wrapper(readNetcatPipe)
	}

	// Forward from netcat to target.
	chCopyNetcat := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, readNetcatPipe)
		chCopyNetcat <- err
		_ = readNetcatPipe.Close()
		_ = netcatConn.Close()
		_ = conn.Close() // sends barrier message.
	}()

	// Forward from target to netcat.
	writeNetcatPipe := io.WriteCloser(netcatConn)
	chCopyTarget := make(chan error, 1)
	go func() {
		_, err := io.Copy(writeNetcatPipe, conn)
		chCopyTarget <- err
		_ = writeNetcatPipe.Close()
	}()

	// Wait for rsync to complete.
	output, err := io.ReadAll(stderr)
	if err != nil {
		_ = cmd.Process.Kill()
		logger.Errorf("Rsync stderr read failed: %s: %v", path, err)
	}

	err = cmd.Wait()
	errs := []error{}
	chCopyNetcatErr := <-chCopyNetcat
	chCopyTargetErr := <-chCopyTarget

	if err != nil {
		errs = append(errs, err)

		// Try to get more info about the error.
		if chCopyNetcatErr != nil {
			errs = append(errs, chCopyNetcatErr)
		}

		if chCopyTargetErr != nil {
			errs = append(errs, chCopyTargetErr)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("Rsync send failed: %s, %s: %v (%s)", name, path, errs, string(output))
	}

	return nil
}

// Recv sets up the receiving half of the websocket to rsync (the other
// half set up by rsync.Send), putting the contents in the directory specified
// by path.
func Recv(path string, conn io.ReadWriteCloser, readWrapper ioprogress.ReaderWrapper, features []string) error {
	err := assertSafePath(path)
	if err != nil {
		return err
	}

	args := []string{
		"--server",
		"-vlogDtpre.iLsfx",
		"--numeric-ids",
		"--devices",
		"--partial",
		"--sparse",
		// This flag is only required on the receiving end.
		// Checks for file modifications on nanoseconds granularity.
		"--modify-window=-1",
	}

	if len(features) > 0 {
		args = append(args, rsyncFeatureArgs(features)...)
	}

	// Add an end of options marker ("--") so that the destination path is never
	// interpreted as an rsync option, even if it begins with a "-".
	args = append(args, "--", ".", path)

	cmd := exec.Command("rsync", args...)

	// Call the wrapper if defined.
	if RunWrapper != nil {
		cleanup, err := RunWrapper(cmd, "", path)
		if err != nil {
			return err
		}

		defer cleanup()
	}

	// Forward from rsync to source.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	chCopyRsync := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, stdout)
		_ = stdout.Close()
		_ = conn.Close() // sends barrier message.
		chCopyRsync <- err
	}()

	// Forward from source to rsync.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	readSourcePipe := io.ReadCloser(conn)
	if readWrapper != nil {
		readSourcePipe = readWrapper(readSourcePipe)
	}

	chCopySource := make(chan error, 1)
	go func() {
		_, err := io.Copy(stdin, readSourcePipe)
		_ = stdin.Close()
		chCopySource <- err
	}()

	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = cmd.Process.Kill()
		logger.Errorf("Rsync stderr read failed: %s: %v", path, err)
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	output, err := io.ReadAll(stderr)
	if err != nil {
		logger.Errorf("Rsync stderr read failed: %s: %v", path, err)
	}

	err = cmd.Wait()
	errs := []error{}
	chCopyRsyncErr := <-chCopyRsync
	chCopySourceErr := <-chCopySource

	if err != nil {
		errs = append(errs, err)

		// Try to get more info about the error.
		if chCopyRsyncErr != nil {
			errs = append(errs, chCopyRsyncErr)
		}

		if chCopySourceErr != nil {
			errs = append(errs, chCopySourceErr)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("Rsync receive failed: %s: %v (%s)", path, errs, string(output))
	}

	return nil
}

func rsyncFeatureArgs(features []string) []string {
	args := []string{}
	if slices.Contains(features, "xattrs") {
		args = append(args, "--xattrs", "--filter=-x security.selinux")
	}

	if slices.Contains(features, "delete") {
		args = append(args, "--delete")
	}

	if slices.Contains(features, "compress") {
		args = append(args, "--compress", "--compress-level=2")
	}

	return args
}
