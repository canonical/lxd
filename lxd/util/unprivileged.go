package util

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared/logger"
)

// maxConcurrentUnprivilegedRuns caps how many functions may run unprivileged at the same time.
//
// Every run pins a dedicated OS thread until its function returns, and an upload keeps its function
// running for as long as the client keeps sending. Without a cap, enough slow uploads would grow the
// thread count past the Go runtime limit (10000 threads by default, see runtime/debug.SetMaxThreads), at
// which point the runtime aborts the whole daemon with "thread exhaustion".
//
// Goroutines only occupy a thread of their own while running, blocked in a system call or locked to a
// thread, so the rest of the daemon normally gets by with a small number of threads. Imports are bound by
// disk throughput rather than by concurrency, so this is set high enough to keep even fast storage busy
// while leaving almost all of the runtime thread limit to the rest of the daemon.
const maxConcurrentUnprivilegedRuns = 128

// unprivilegedRunSem bounds the number of concurrent unprivileged runs.
var unprivilegedRunSem = make(chan struct{}, maxConcurrentUnprivilegedRuns)

// isInitialThread reports whether the calling goroutine runs on the initial thread of the process.
// It is a variable so that tests can exercise the handling of the initial thread.
var isInitialThread = func() bool {
	return unix.Gettid() == unix.Getpid()
}

// RunUnprivileged runs fn on a dedicated OS thread whose effective uid and gid are switched to uid and
// gid, and waits for it to return.
//
// Only the effective uid and gid are changed and the supplementary groups are dropped. This removes all
// effective capabilities from the thread, so that permission checks, filesystem quotas and reserved disk
// space apply to it as they would to any process of that user. The real and saved uid and gid remain 0,
// which stops processes of the unprivileged user (e.g. QEMU) from signaling or tracing the thread, as a
// signal sent to any thread affects the whole daemon.
//
// The thread is destroyed once fn returns, so that its credentials cannot be inherited by unrelated
// goroutines. The initial thread of the process is never used, as its credentials are the ones reported
// for the process as a whole and the Go runtime cannot destroy it.
//
// Goroutines started by fn run on other threads with the credentials of the daemon. As fn lacks the
// permission to open most files owned by LXD, it should only operate on already open file descriptors.
// Changing the credentials of a thread also makes the kernel reset the dumpable attribute of the process
// according to the fs.suid_dumpable sysctl.
//
// At most maxConcurrentUnprivilegedRuns functions run at the same time, and ctx is used while waiting for
// a free slot, so that a client going away frees up its place in the queue. A panic in fn is recovered and
// returned as an error.
func RunUnprivileged(ctx context.Context, uid uint32, gid uint32, fn func() error) error {
	if uid == 0 || gid == 0 {
		return errors.New("Unprivileged uid and gid must be non-zero")
	}

	// Check the context first, as select picks at random when both a slot and ctx.Done() are ready.
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("Failed waiting for an unprivileged run slot: %w", err)
	}

	select {
	case unprivilegedRunSem <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("Failed waiting for an unprivileged run slot: %w", ctx.Err())
	}

	defer func() { <-unprivilegedRunSem }()

	result := make(chan error, 1)

	// Closed once the run has finished, to give back the initial thread if it was held on to.
	finished := make(chan struct{})
	defer close(finished)

	var run func()
	run = func() {
		runtime.LockOSThread()

		// Goroutines are not tied to threads, so this one may have been scheduled on the initial thread.
		// Keep holding on to that thread while the next goroutine is started, which guarantees it a
		// different thread, and give it back unmodified once the run has finished.
		if isInitialThread() {
			go run()
			<-finished
			runtime.UnlockOSThread()
			return
		}

		// Deliberately returning while still locked to the thread. This makes the Go runtime destroy the
		// thread instead of returning it to the pool with reduced credentials.
		runOnLockedThread(uid, gid, fn, result)
	}

	go run()

	return <-result
}

// runOnLockedThread drops the credentials of the calling OS thread, runs fn and sends the outcome to
// result. The outcome is sent even if fn panics or exits the goroutine through runtime.Goexit.
//
// It must only be called from a goroutine locked to its OS thread that exits without unlocking it.
func runOnLockedThread(uid uint32, gid uint32, fn func() error, result chan<- error) {
	var err error
	returned := false

	defer func() {
		r := recover()
		if r != nil {
			logger.Error("Unprivileged function panicked", logger.Ctx{"err": r, "stack": string(debug.Stack())})
			err = fmt.Errorf("Unprivileged function panicked: %v", r)
		} else if !returned {
			err = errors.New("Unprivileged function exited without returning")
		}

		result <- err
	}()

	err = dropThreadCredentials(uid, gid)
	if err == nil {
		err = fn()
	}

	returned = true
}

// dropThreadCredentials switches the effective uid and gid of the calling OS thread to uid and gid, and
// drops its supplementary groups. The real and saved uid and gid are left unchanged.
//
// The wrappers in the syscall and unix packages deliberately apply credential changes to every thread of
// the process, so the raw system calls are used here to only affect the calling thread.
func dropThreadCredentials(uid uint32, gid uint32) error {
	// Passing -1 leaves the corresponding id unchanged.
	const unchanged = ^uintptr(0)

	_, _, errno := unix.RawSyscall(unix.SYS_SETGROUPS, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("Failed dropping supplementary groups: %w", errno)
	}

	_, _, errno = unix.RawSyscall(unix.SYS_SETRESGID, unchanged, uintptr(gid), unchanged)
	if errno != 0 {
		return fmt.Errorf("Failed switching to gid %d: %w", gid, errno)
	}

	_, _, errno = unix.RawSyscall(unix.SYS_SETRESUID, unchanged, uintptr(uid), unchanged)
	if errno != 0 {
		return fmt.Errorf("Failed switching to uid %d: %w", uid, errno)
	}

	// The kernel clears the effective capabilities when the effective uid changes from 0, unless the
	// securebits of the thread say otherwise. Verify it, as quotas do not apply while they are held.
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	err := unix.Capget(&header, &data[0])
	if err != nil {
		return fmt.Errorf("Failed reading thread capabilities: %w", err)
	}

	if data[0].Effective != 0 || data[1].Effective != 0 {
		return errors.New("Thread kept effective capabilities after switching credentials")
	}

	return nil
}
