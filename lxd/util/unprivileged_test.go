package util

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// unprivilegedTestIDs returns the uid of the "nobody" user and the gid of the "nogroup" (or "nobody")
// group, skipping the test if the test environment cannot support running unprivileged.
func unprivilegedTestIDs(t *testing.T) (uid uint32, gid uint32) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("Test requires root to change file ownership and thread credentials")
	}

	u, err := user.Lookup("nobody")
	if err != nil {
		t.Skip("Test requires the nobody user to be present")
	}

	g, err := user.LookupGroup("nogroup")
	if err != nil {
		g, err = user.LookupGroup("nobody")
		if err != nil {
			t.Skip("Test requires the nogroup or nobody group to be present")
		}
	}

	parsedUID, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		t.Fatalf("Failed parsing uid: %v", err)
	}

	parsedGID, err := strconv.ParseUint(g.Gid, 10, 32)
	if err != nil {
		t.Fatalf("Failed parsing gid: %v", err)
	}

	return uint32(parsedUID), uint32(parsedGID)
}

// threadStatusIDs returns the real, effective, saved and filesystem ids from the given "Uid:" or "Gid:"
// line of /proc/self/task/<tid>/status. It returns os.ErrNotExist if the thread no longer exists.
func threadStatusIDs(tid int, field string) ([]string, error) {
	status, err := os.ReadFile(filepath.Join("/proc/self/task", strconv.Itoa(tid), "status"))
	if err != nil {
		return nil, err
	}

	for line := range strings.SplitSeq(string(status), "\n") {
		after, ok := strings.CutPrefix(line, field+":")
		if ok {
			return strings.Fields(after), nil
		}
	}

	return nil, fmt.Errorf("Field %q not found in status of thread %d", field, tid)
}

// waitThreadGone waits for the given thread of the current process to exit.
func waitThreadGone(t *testing.T, tid int) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := os.Stat(filepath.Join("/proc/self/task", strconv.Itoa(tid)))
		if errors.Is(err, os.ErrNotExist) {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("Thread %d still exists after the run", tid)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// waitAllThreadsRoot waits until every thread of the current process runs with all uids and gids set to 0.
func waitAllThreadsRoot(t *testing.T) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		offending, err := nonRootThreads()
		if err != nil {
			t.Fatalf("Failed inspecting threads: %v", err)
		}

		if len(offending) == 0 {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("Threads still running with reduced credentials: %v", offending)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// nonRootThreads returns the threads of the current process with any uid or gid other than 0.
func nonRootThreads() ([]string, error) {
	entries, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return nil, err
	}

	var offending []string
	for _, entry := range entries {
		tid, err := strconv.Atoi(entry.Name())
		if err != nil {
			return nil, err
		}

		for _, field := range []string{"Uid", "Gid"} {
			ids, err := threadStatusIDs(tid, field)
			if errors.Is(err, os.ErrNotExist) {
				// The thread exited in the meantime.
				break
			}

			if err != nil {
				return nil, err
			}

			if slices.ContainsFunc(ids, func(id string) bool { return id != "0" }) {
				offending = append(offending, fmt.Sprintf("%d %s:%v", tid, field, ids))
			}
		}
	}

	return offending, nil
}

// fillUnprivilegedRunSlots occupies every unprivileged run slot until the returned function is called.
func fillUnprivilegedRunSlots(t *testing.T, uid uint32, gid uint32) (release func()) {
	t.Helper()

	gate := make(chan struct{})
	var started sync.WaitGroup
	var finished sync.WaitGroup

	for range maxConcurrentUnprivilegedRuns {
		started.Add(1)
		finished.Go(func() {
			err := RunUnprivileged(context.Background(), uid, gid, func() error {
				started.Done()
				<-gate
				return nil
			})
			if err != nil {
				t.Errorf("Failed running unprivileged: %v", err)
			}
		})
	}

	started.Wait()

	return func() {
		close(gate)
		finished.Wait()
	}
}

func TestRunUnprivilegedRejectsRoot(t *testing.T) {
	tests := []struct {
		name string
		uid  uint32
		gid  uint32
	}{
		{name: "root uid", uid: 0, gid: 65534},
		{name: "root gid", uid: 65534, gid: 0},
		{name: "root uid and gid", uid: 0, gid: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			err := RunUnprivileged(t.Context(), tt.uid, tt.gid, func() error {
				called = true
				return nil
			})
			if err == nil {
				t.Fatal("Expected an error when asked to run as root")
			}

			if called {
				t.Fatal("Function was run despite the error")
			}
		})
	}
}

// TestRunUnprivilegedCredentials checks the credentials fn runs with: only the effective ids are switched,
// supplementary groups are dropped and no effective capability is left.
func TestRunUnprivilegedCredentials(t *testing.T) {
	uid, gid := unprivilegedTestIDs(t)

	// A file only root may access.
	rootOnly := filepath.Join(t.TempDir(), "root-only")
	err := os.WriteFile(rootOnly, []byte("secret"), 0600)
	if err != nil {
		t.Fatalf("Failed creating file: %v", err)
	}

	errSentinel := errors.New("sentinel")

	err = RunUnprivileged(t.Context(), uid, gid, func() error {
		ruid, euid, suid := unix.Getresuid()
		if ruid != 0 || euid != int(uid) || suid != 0 {
			return fmt.Errorf("Unexpected uids %d/%d/%d", ruid, euid, suid)
		}

		rgid, egid, sgid := unix.Getresgid()
		if rgid != 0 || egid != int(gid) || sgid != 0 {
			return fmt.Errorf("Unexpected gids %d/%d/%d", rgid, egid, sgid)
		}

		// Permission checks and quotas use the filesystem uid, which follows the effective uid.
		uids, err := threadStatusIDs(unix.Gettid(), "Uid")
		if err != nil {
			return err
		}

		if len(uids) != 4 || uids[3] != strconv.FormatUint(uint64(uid), 10) {
			return fmt.Errorf("Unexpected uids in thread status %v", uids)
		}

		groups, err := unix.Getgroups()
		if err != nil {
			return err
		}

		if len(groups) != 0 {
			return fmt.Errorf("Unexpected supplementary groups %v", groups)
		}

		header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
		var data [2]unix.CapUserData
		err = unix.Capget(&header, &data[0])
		if err != nil {
			return err
		}

		if data[0].Effective != 0 || data[1].Effective != 0 {
			return fmt.Errorf("Unexpected effective capabilities %x %x", data[0].Effective, data[1].Effective)
		}

		// Without CAP_DAC_OVERRIDE the permission checks apply.
		_, err = os.ReadFile(rootOnly)
		if !errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("Expected permission error reading a root-only file, got: %v", err)
		}

		return errSentinel
	})
	if !errors.Is(err, errSentinel) {
		t.Fatalf("Expected the error returned by the function, got: %v", err)
	}
}

// TestRunUnprivilegedDiscardsThread checks that the thread used for a run is destroyed afterwards,
// whichever way the function finishes.
func TestRunUnprivilegedDiscardsThread(t *testing.T) {
	uid, gid := unprivilegedTestIDs(t)

	tests := []struct {
		name    string
		fn      func() error
		wantErr string
	}{
		{name: "success", fn: func() error { return nil }},
		{name: "error", fn: func() error { return errors.New("failed") }, wantErr: "failed"},
		{name: "panic", fn: func() error { panic("boom") }, wantErr: "Unprivileged function panicked: boom"},
		{name: "goexit", fn: func() error { runtime.Goexit(); return nil }, wantErr: "Unprivileged function exited without returning"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tid atomic.Int64
			err := RunUnprivileged(t.Context(), uid, gid, func() error {
				tid.Store(int64(unix.Gettid()))
				return tt.fn()
			})

			if tt.wantErr == "" && err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr) {
				t.Fatalf("Expected error %q, got: %v", tt.wantErr, err)
			}

			waitThreadGone(t, int(tid.Load()))
		})
	}
}

// TestRunUnprivilegedInitialThread checks that a run never drops the credentials of the initial thread,
// and that the initial thread is given back unmodified.
func TestRunUnprivilegedInitialThread(t *testing.T) {
	uid, gid := unprivilegedTestIDs(t)

	// Pretend that the first goroutine started away from the initial thread lands on it, while still
	// guarding the real initial thread.
	var heldTID atomic.Int64
	original := isInitialThread
	t.Cleanup(func() { isInitialThread = original })
	isInitialThread = func() bool {
		if original() {
			return true
		}

		return heldTID.CompareAndSwap(0, int64(unix.Gettid()))
	}

	var runTID int
	err := RunUnprivileged(t.Context(), uid, gid, func() error {
		runTID = unix.Gettid()
		return nil
	})
	if err != nil {
		t.Fatalf("Failed running unprivileged: %v", err)
	}

	held := int(heldTID.Load())
	if held == 0 {
		t.Fatal("The initial thread check was not performed")
	}

	if runTID == held {
		t.Fatal("The function ran on the thread that was held back")
	}

	waitThreadGone(t, runTID)

	// The held thread is given back to the runtime rather than destroyed, so it has to be unmodified.
	for _, field := range []string{"Uid", "Gid"} {
		ids, err := threadStatusIDs(held, field)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			t.Fatalf("Failed inspecting thread %d: %v", held, err)
		}

		if slices.ContainsFunc(ids, func(id string) bool { return id != "0" }) {
			t.Fatalf("Held thread %d has %s %v", held, field, ids)
		}
	}
}

// TestRunUnprivilegedNeverUsesInitialThread checks over many runs that the function never runs on the
// initial thread, whose credentials are reported for the whole process.
func TestRunUnprivilegedNeverUsesInitialThread(t *testing.T) {
	uid, gid := unprivilegedTestIDs(t)

	var hits atomic.Int64
	original := isInitialThread
	t.Cleanup(func() { isInitialThread = original })
	isInitialThread = func() bool {
		onInitial := original()
		if onInitial {
			hits.Add(1)
		}

		return onInitial
	}

	pid := os.Getpid()

	var wg sync.WaitGroup
	for range 200 {
		wg.Go(func() {
			err := RunUnprivileged(t.Context(), uid, gid, func() error {
				if unix.Gettid() == pid {
					return errors.New("Function ran on the initial thread")
				}

				return nil
			})
			if err != nil {
				t.Error(err)
			}
		})
	}

	wg.Wait()

	t.Logf("Goroutine landed on the initial thread %d times", hits.Load())

	ids, err := threadStatusIDs(pid, "Uid")
	if err != nil {
		t.Fatalf("Failed inspecting the initial thread: %v", err)
	}

	if slices.ContainsFunc(ids, func(id string) bool { return id != "0" }) {
		t.Fatalf("Initial thread has uids %v", ids)
	}
}

// TestRunUnprivilegedSignals checks that processes of the unprivileged user cannot signal the thread
// running the function, as that would affect the whole process.
func TestRunUnprivilegedSignals(t *testing.T) {
	uid, gid := unprivilegedTestIDs(t)

	kill, err := exec.LookPath("kill")
	if err != nil {
		t.Skip("Test requires the kill command")
	}

	tids := make(chan int)
	gate := make(chan struct{})
	result := make(chan error, 1)

	go func() {
		result <- RunUnprivileged(t.Context(), uid, gid, func() error {
			tids <- unix.Gettid()
			<-gate
			return nil
		})
	}()

	tid := <-tids

	// Signal 0 only performs the permission check. The command is started from a privileged goroutine,
	// as a child forked from the unprivileged thread would inherit its credentials.
	cmd := exec.Command(kill, "-0", strconv.Itoa(tid))
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid}}
	out, err := cmd.CombinedOutput()

	close(gate)

	runErr := <-result
	if runErr != nil {
		t.Fatalf("Failed running unprivileged: %v", runErr)
	}

	if err == nil {
		t.Fatal("Process of the unprivileged user was allowed to signal the thread")
	}

	if !strings.Contains(string(out), "not permitted") {
		t.Fatalf("Expected a permission error from kill, got: %v (%s)", err, out)
	}
}

// TestRunUnprivilegedContext checks that the context is honored while waiting for a free slot.
func TestRunUnprivilegedContext(t *testing.T) {
	uid, gid := unprivilegedTestIDs(t)

	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		called := false
		err := RunUnprivileged(ctx, uid, gid, func() error {
			called = true
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Expected context.Canceled, got: %v", err)
		}

		if called {
			t.Fatal("Function was run despite the canceled context")
		}
	})

	t.Run("deadline while waiting for a slot", func(t *testing.T) {
		release := fillUnprivilegedRunSlots(t, uid, gid)

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		called := false
		err := RunUnprivileged(ctx, uid, gid, func() error {
			called = true
			return nil
		})

		release()

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Expected context.DeadlineExceeded, got: %v", err)
		}

		if called {
			t.Fatal("Function was run despite the expired context")
		}

		// The slots are given back once the runs have finished.
		err = RunUnprivileged(t.Context(), uid, gid, func() error { return nil })
		if err != nil {
			t.Fatalf("Failed running after the slots were released: %v", err)
		}
	})
}

// TestRunUnprivilegedLimitsConcurrency checks that no more functions run at the same time than the limit
// allows. Every run pins an OS thread, so without the limit a burst of slow uploads would grow the thread
// count until the Go runtime aborts the process.
func TestRunUnprivilegedLimitsConcurrency(t *testing.T) {
	uid, gid := unprivilegedTestIDs(t)

	var inFlight atomic.Int64
	var peak atomic.Int64
	gate := make(chan struct{})

	var wg sync.WaitGroup
	for range maxConcurrentUnprivilegedRuns * 2 {
		wg.Go(func() {
			err := RunUnprivileged(t.Context(), uid, gid, func() error {
				current := inFlight.Add(1)
				defer inFlight.Add(-1)

				for {
					recorded := peak.Load()
					if current <= recorded || peak.CompareAndSwap(recorded, current) {
						break
					}
				}

				<-gate
				return nil
			})
			if err != nil {
				t.Errorf("Failed running unprivileged: %v", err)
			}
		})
	}

	// Wait for the limit to be reached, then give the remaining runs a chance to wrongly start.
	deadline := time.Now().Add(10 * time.Second)
	for inFlight.Load() < maxConcurrentUnprivilegedRuns && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()

	if peak.Load() != maxConcurrentUnprivilegedRuns {
		t.Fatalf("Expected %d concurrent runs at peak, saw %d", maxConcurrentUnprivilegedRuns, peak.Load())
	}
}

// TestRunUnprivilegedKeepsOtherGoroutinesPrivileged runs many functions that block, which makes the
// runtime hand their processors off and start new threads, while other goroutines keep checking the
// credentials of whichever thread they are scheduled on. No thread with reduced credentials may ever be
// used for anything but the function it was created for.
func TestRunUnprivilegedKeepsOtherGoroutinesPrivileged(t *testing.T) {
	uid, gid := unprivilegedTestIDs(t)

	initialGroups, err := unix.Getgroups()
	if err != nil {
		t.Fatalf("Failed reading groups: %v", err)
	}

	var stop atomic.Bool
	var violations atomic.Int64
	var checkers sync.WaitGroup
	for range 32 {
		checkers.Go(func() {
			for !stop.Load() {
				ruid, euid, suid := unix.Getresuid()
				rgid, egid, sgid := unix.Getresgid()
				groups, err := unix.Getgroups()
				if err != nil || ruid != 0 || euid != 0 || suid != 0 || rgid != 0 || egid != 0 || sgid != 0 || !slices.Equal(groups, initialGroups) {
					violations.Add(1)
				}

				// Block in a system call so that the runtime needs additional threads.
				_ = unix.Nanosleep(&unix.Timespec{Nsec: int64(100 * time.Microsecond)}, nil)
			}
		})
	}

	var runs sync.WaitGroup
	for range maxConcurrentUnprivilegedRuns * 2 {
		runs.Go(func() {
			err := RunUnprivileged(t.Context(), uid, gid, func() error {
				for range 5 {
					// Parking a locked goroutine hands its processor off to another thread.
					time.Sleep(time.Millisecond)
					_ = unix.Nanosleep(&unix.Timespec{Nsec: int64(100 * time.Microsecond)}, nil)
				}

				return nil
			})
			if err != nil {
				t.Errorf("Failed running unprivileged: %v", err)
			}
		})
	}

	runs.Wait()
	stop.Store(true)
	checkers.Wait()

	if violations.Load() != 0 {
		t.Fatalf("Goroutines ran on a thread with reduced credentials %d times", violations.Load())
	}

	waitAllThreadsRoot(t)
}
