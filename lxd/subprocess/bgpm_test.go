//go:build !windows

package subprocess

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v2"
)

func TestSignalHandling(t *testing.T) {
	var a []string
	a = append(a, "testscript/signal.sh")
	var file *os.File
	p, err := NewProcess("sh", a, "testscript/signal_out.txt", "")

	if err != nil {
		t.Error("Failed process creation: ", err)
	}

	err = p.Start(context.Background())
	if err != nil {
		t.Error("Failed to start process ", err)
	}

	time.Sleep(2 * time.Second)
	err = p.Reload()
	if err != nil {
		t.Error("Unable to Reload process: ", err)
	}

	time.Sleep(2 * time.Second)
	err = p.Signal(10)
	if err != nil {
		t.Error("Unable to Signal process: ", err)
	}

	ecode, err := p.Wait(context.Background())
	if err == nil {
		t.Error("Did not exit with an error")
	} else if ecode != 1 {
		t.Error("Exit code is not 1: ", ecode)
	}

	file, err = os.OpenFile("testscript/signal_out.txt", os.O_RDWR, 0644)
	if err != nil {
		t.Error("Could not open file ", err)
	}

	defer func() { _ = file.Close() }()

	var text = make([]byte, 1024)
	for {
		_, err = file.Read(text)
		// Break if finally arrived at end of file
		if err == io.EOF {
			break
		}

		// Break if error occurred
		if err != nil && err != io.EOF {
			t.Error("Error in reading file ", err)
		}
	}

	if !strings.Contains(string(text), "Called with signal 1") {
		t.Errorf("Reload failed. File output mismatch. Got %s", string(text))
	}

	if !strings.Contains(string(text), "Called with signal 10") {
		t.Errorf("Signal failed. File output mismatch. Got %s", string(text))
	}

	err = os.Remove("testscript/signal_out.txt")
	if err != nil {
		t.Error("Could not delete file ", err)
	}
}

// tests newprocess, start, stop, save, import, restart, wait.
func TestStopRestart(t *testing.T) {
	var a []string
	a = append(a, "testscript/stoprestart.sh")

	p, err := NewProcess("sh", a, "", "")
	if err != nil {
		t.Error("Failed process creation: ", err)
	}

	err = p.Start(context.Background())
	if err != nil {
		t.Error("Failed to start process: ", err)
	}

	err = p.Stop()
	if err != nil {
		t.Error("Failed to stop process: ", err)
	}

	err = p.Save("testscript/test2.yaml")
	if err != nil {
		t.Error("Failed to save process: ", err)
	}

	p, err = ImportProcess("testscript/test2.yaml")
	if err != nil {
		t.Error("Failed to import process: ", err)
	}

	err = p.Start(context.Background())
	if err != nil {
		t.Error("Failed to start process: ", err)
	}

	err = p.Restart(context.Background())
	if err != nil {
		t.Error("Failed to restart process: ", err)
	}

	exitcode, err := p.Wait(context.Background())
	if err != nil {
		t.Error("Could not wait for process: ", err)
	} else if exitcode != 0 {
		t.Errorf("Exit code expected to be 0 but got %d", exitcode)
	}

	err = os.Remove("testscript/test2.yaml")
	if err != nil {
		t.Error("Could not delete file: ", err)
	}
}

func TestProcessStartWaitExit(t *testing.T) {
	var a []string
	var file *os.File
	var exp string
	var text []byte
	a = append(a, "testscript/exit1.sh")
	p, err := NewProcess("sh", a, "testscript/out.txt", "")
	if err != nil {
		t.Error("Failed process creation: ", err)
	}

	err = p.Start(context.Background())
	if err != nil {
		t.Error("Failed to start process: ", err)
	}

	ecode, err := p.Wait(context.Background())
	if err == nil {
		t.Error("Did not exit with an error")
	} else if ecode != 1 {
		t.Error("Exit code is not 1: ", ecode)
	}

	file, err = os.OpenFile("testscript/out.txt", os.O_RDWR, 0644)
	if err != nil {
		t.Error("Could not open file: ", err)
	}

	defer func() { _ = file.Close() }()

	exp = "hello again\nwaiting now\n"
	// Read file, line by line
	text = make([]byte, len(exp))
	for {
		_, err = file.Read(text)
		// Break if finally arrived at end of file
		if err == io.EOF {
			break
		}
		// Break if error occurred
		if err != nil && err != io.EOF {
			t.Error("Error reading file: ", err)
		}
	}

	if string(text) != exp {
		t.Errorf("File output mismatch Expected %s got %s", "hello again\nwaiting now\n", string(text))
	}

	// Cleanup
	err = os.Remove("testscript/out.txt")
	if err != nil {
		t.Error("Could not delete file: ", err)
	}
}

// startSleep starts a long-running process for identity tests and registers a
// cleanup that kills it on test exit.
func startSleep(t *testing.T) *Process {
	t.Helper()

	p, err := NewProcess("sleep", []string{"30"}, "", "")
	if err != nil {
		t.Fatal("Failed process creation: ", err)
	}

	err = p.Start(context.Background())
	if err != nil {
		t.Fatal("Failed starting process: ", err)
	}

	t.Cleanup(func() { _ = p.Stop() })

	return p
}

// savePidFile saves p to a fresh pid file, applies mutate to the saved YAML,
// and returns the file path.
func savePidFile(t *testing.T, p *Process, mutate func(*Process)) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "proc.yaml")
	err := p.Save(path)
	if err != nil {
		t.Fatal("Failed saving process: ", err)
	}

	if mutate == nil {
		return path
	}

	dat, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("Failed reading pid file: ", err)
	}

	saved := &Process{}
	err = yaml.Unmarshal(dat, saved)
	if err != nil {
		t.Fatal("Failed parsing pid file: ", err)
	}

	mutate(saved)

	dat, err = yaml.Marshal(saved)
	if err != nil {
		t.Fatal("Failed serializing pid file: ", err)
	}

	err = os.WriteFile(path, dat, 0600)
	if err != nil {
		t.Fatal("Failed writing pid file: ", err)
	}

	return path
}

// assertAlive fails the test if the monitored process exits within the grace
// period. Detects a process that was wrongly signaled.
func assertAlive(t *testing.T, p *Process, grace time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	exitcode, err := p.Wait(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Process was killed but should have been left alone (exit code %d, err %v)", exitcode, err)
	}
}

// TestImportRoundTripLive checks that a saved live process can
// be imported, probed and stopped, and the stop lands on the correct process.
func TestImportRoundTripLive(t *testing.T) {
	p := startSleep(t)
	path := savePidFile(t, p, nil)

	imp, err := ImportProcess(path)
	if err != nil {
		t.Fatal("Failed importing process: ", err)
	}

	err = imp.Signal(0)
	if err != nil {
		t.Error("Imported process should be seen as running: ", err)
	}

	err = imp.Stop()
	if err != nil {
		t.Error("Failed stopping imported process: ", err)
	}

	// The kill must land on the process we saved: its monitor sees it exit.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = p.Wait(ctx)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Error("Original process did not exit after Stop of imported process: ", err)
	}

	// A second Stop reports the process as gone.
	err = imp.Stop()
	if !errors.Is(err, ErrNotRunning) {
		t.Error("Second Stop should return ErrNotRunning, got: ", err)
	}
}

// TestImportStalePidReuse checks that a pid file whose starttime does not match
// the process currently holding the pid is recognized as not running and the imposter is
// not signaled.
func TestImportStalePidReuse(t *testing.T) {
	victim := startSleep(t)
	path := savePidFile(t, victim, func(saved *Process) {
		if saved.StartTime == 0 {
			t.Fatal("Saved pid file has no starttime; identity cannot be verified")
		}

		saved.StartTime += 12345
	})

	imp, err := ImportProcess(path)
	if err != nil {
		t.Fatal("Failed importing process: ", err)
	}

	err = imp.Stop()
	if !errors.Is(err, ErrNotRunning) {
		t.Error("Stop of a recycled pid should return ErrNotRunning, got: ", err)
	}

	assertAlive(t, victim, 150*time.Millisecond)
}

// TestImportStaleBootID checks that a pid file from a previous boot is never
// acted on.
func TestImportStaleBootID(t *testing.T) {
	victim := startSleep(t)
	path := savePidFile(t, victim, func(saved *Process) {
		if saved.BootID == "" {
			t.Fatal("Saved pid file has no boot ID; identity cannot be verified")
		}

		saved.BootID = "00000000-dead-beef-0000-000000000000"
	})

	imp, err := ImportProcess(path)
	if err != nil {
		t.Fatal("Failed importing process: ", err)
	}

	err = imp.Stop()
	if !errors.Is(err, ErrNotRunning) {
		t.Error("Stop after a reboot should return ErrNotRunning, got: ", err)
	}

	assertAlive(t, victim, 150*time.Millisecond)
}

// TestImportDeadProcess checks that importing a pid file for an exited
// process yields ErrNotRunning rather than an import error. If the subprocess.ImportProcess() api
// changes to return eg an ErrStale in the future, call sites would need to change;
// this test encodes current caller expectations.
func TestImportDeadProcess(t *testing.T) {
	p, err := NewProcess("sleep", []string{"0"}, "", "")
	if err != nil {
		t.Fatal("Failed process creation: ", err)
	}

	err = p.Start(context.Background())
	if err != nil {
		t.Fatal("Failed starting process: ", err)
	}

	_, err = p.Wait(context.Background())
	if err != nil {
		t.Fatal("Failed waiting for process: ", err)
	}

	path := savePidFile(t, p, nil)

	imp, err := ImportProcess(path)
	if err != nil {
		t.Fatal("Failed importing process: ", err)
	}

	err = imp.Stop()
	if !errors.Is(err, ErrNotRunning) {
		t.Error("Stop of an exited process should return ErrNotRunning, got: ", err)
	}
}

// TestImportOldFormat checks that pid files written before the changes are handled
// as they are currently. This is relevant for an lxd refresh on top of existing daemons.
func TestImportOldFormat(t *testing.T) {
	p := startSleep(t)
	path := savePidFile(t, p, func(saved *Process) {
		saved.StartTime = 0
		saved.BootID = ""
	})

	imp, err := ImportProcess(path)
	if err != nil {
		t.Fatal("Failed importing process: ", err)
	}

	err = imp.Signal(0)
	if err != nil {
		t.Error("Old-format import of a live process should be seen as running: ", err)
	}

	err = imp.Stop()
	if err != nil {
		t.Error("Failed stopping process imported from old-format file: ", err)
	}
}
