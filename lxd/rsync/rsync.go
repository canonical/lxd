package rsync

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pborman/uuid"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

// Debug controls additional debugging in rsync output.
var Debug bool

// LocalCopy copies a directory using rsync (with the --devices option).
func LocalCopy(source string, dest string, bwlimit string, xattrs bool, rsyncArgs ...string) (string, error) {
	err := os.MkdirAll(dest, 0755)
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
		"--checksum",
		"--numeric-ids",
	}

	if xattrs {
		args = append(args, "--xattrs")
		if AtLeast("3.1.3") {
			args = append(args, "--filter=-x security.selinux")
		}
	}

	if bwlimit != "" {
		args = append(args, "--bwlimit", bwlimit)
	}

	if len(rsyncArgs) > 0 {
		args = append(args, rsyncArgs...)
	}

	args = append(args,
		rsyncVerbosity,
		shared.AddSlash(source),
		dest)

	msg, err := shared.RunCommand("rsync", args...)
	if err != nil {
		runError, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runError.Err.(*exec.ExitError)
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

func sendSetup(name string, path string, bwlimit string, execPath string, features []string, rsyncArgs ...string) (*exec.Cmd, net.Conn, io.ReadCloser, error) {
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
	auds := fmt.Sprintf("@lxd/%s", uuid.NewRandom().String())
	// We simply copy a part of the uuid if it's longer than the allowed
	// maximum. That should be safe enough for our purposes.
	if len(auds) > shared.ABSTRACT_UNIX_SOCK_LEN-1 {
		auds = auds[:shared.ABSTRACT_UNIX_SOCK_LEN-1]
	}
	l, err := net.Listen("unix", auds)
	if err != nil {
		return nil, nil, nil, err
	}
	defer l.Close()

	/*
	 * Here, the path /tmp/foo is ignored. Since we specify localhost,
	 * rsync thinks we are syncing to a remote host (in this case, the
	 * other end of the lxd websocket), and so the path specified on the
	 * --server instance of rsync takes precedence.
	 *
	 * Additionally, we use sh -c instead of just calling nc directly
	 * because rsync passes a whole bunch of arguments to the wrapper
	 * command (i.e. the command to run on --server). However, we're
	 * hardcoding that at the other end, so we can just ignore it.
	 */
	rsyncCmd := fmt.Sprintf("sh -c \"%s netcat %s %s\"", execPath, auds, name)

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

	if features != nil && len(features) > 0 {
		args = append(args, rsyncFeatureArgs(features)...)
	}

	if len(rsyncArgs) > 0 {
		args = append(args, rsyncArgs...)
	}

	args = append(args, []string{
		path,
		"localhost:/tmp/foo",
		"-e",
		rsyncCmd}...)

	cmd := exec.Command("rsync", args...)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}

	var conn *net.Conn
	chConn := make(chan *net.Conn, 1)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			chConn <- nil
			return
		}

		chConn <- &conn
	}()

	select {
	case conn = <-chConn:
		if conn == nil {
			output, _ := ioutil.ReadAll(stderr)
			cmd.Process.Kill()
			cmd.Wait()
			return nil, nil, nil, fmt.Errorf("Failed to connect to rsync socket (%s)", string(output))
		}

	case <-time.After(10 * time.Second):
		output, _ := ioutil.ReadAll(stderr)
		cmd.Process.Kill()
		cmd.Wait()
		return nil, nil, nil, fmt.Errorf("rsync failed to spawn after 10s (%s)", string(output))
	}

	return cmd, *conn, stderr, nil
}

// Send sets up the sending half of an rsync, to recursively send the
// directory pointed to by path over the websocket.
func Send(name string, path string, conn io.ReadWriteCloser, tracker *ioprogress.ProgressTracker, features []string, bwlimit string, execPath string, rsyncArgs ...string) error {
	cmd, netcatConn, stderr, err := sendSetup(name, path, bwlimit, execPath, features, rsyncArgs...)
	if err != nil {
		return err
	}

	// Setup progress tracker.
	readNetcatPipe := io.ReadCloser(netcatConn)
	if tracker != nil {
		readNetcatPipe = &ioprogress.ProgressReader{
			ReadCloser: netcatConn,
			Tracker:    tracker,
		}
	}

	// Forward from netcat to target.
	chCopyNetcat := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, readNetcatPipe)
		chCopyNetcat <- err
		readNetcatPipe.Close()
		netcatConn.Close()
		conn.Close() // sends barrier message.
	}()

	// Forward from target to netcat.
	writeNetcatPipe := io.WriteCloser(netcatConn)
	chCopyTarget := make(chan error, 1)
	go func() {
		_, err := io.Copy(writeNetcatPipe, conn)
		chCopyTarget <- err
		writeNetcatPipe.Close()
	}()

	// Wait for rsync to complete.
	output, err := ioutil.ReadAll(stderr)
	if err != nil {
		cmd.Process.Kill()
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
func Recv(path string, conn io.ReadWriteCloser, tracker *ioprogress.ProgressTracker, features []string) error {
	args := []string{
		"--server",
		"-vlogDtpre.iLsfx",
		"--numeric-ids",
		"--devices",
		"--partial",
		"--sparse",
	}

	if features != nil && len(features) > 0 {
		args = append(args, rsyncFeatureArgs(features)...)
	}

	args = append(args, []string{".", path}...)

	cmd := exec.Command("rsync", args...)

	// Forward from rsync to source.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	chCopyRsync := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, stdout)
		stdout.Close()
		conn.Close() // sends barrier message.
		chCopyRsync <- err
	}()

	// Forward from source to rsync.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	readSourcePipe := io.ReadCloser(conn)
	if tracker != nil {
		readSourcePipe = &ioprogress.ProgressReader{
			ReadCloser: conn,
			Tracker:    tracker,
		}
	}

	chCopySource := make(chan error, 1)
	go func() {
		_, err := io.Copy(stdin, readSourcePipe)
		stdin.Close()
		chCopySource <- err
	}()

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cmd.Process.Kill()
		logger.Errorf("Rsync stderr read failed: %s: %v", path, err)
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	output, err := ioutil.ReadAll(stderr)
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
	if shared.StringInSlice("xattrs", features) {
		args = append(args, "--xattrs")
		if AtLeast("3.1.3") {
			args = append(args, "--filter=-x security.selinux")
		}
	}

	if shared.StringInSlice("delete", features) {
		args = append(args, "--delete")
	}

	if shared.StringInSlice("compress", features) {
		args = append(args, "--compress")
		args = append(args, "--compress-level=2")
	}

	return args
}

// AtLeast compares the local version to a minimum version.
func AtLeast(min string) bool {
	// Parse the current version.
	out, err := shared.RunCommand("rsync", "--version")
	if err != nil {
		return false
	}

	fields := strings.Split(strings.Split(out, "\n")[0], "  ")
	if len(fields) < 3 {
		return false
	}

	versionStr := strings.TrimPrefix(fields[1], "version ")

	ver, err := version.Parse(versionStr)
	if err != nil {
		return false
	}

	// Load minium version.
	minVer, err := version.NewDottedVersion(min)
	if err != nil {
		return false
	}

	return ver.Compare(minVer) >= 0
}
