package daemon

import (
	"sync"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared"
)

// have we setup shared mounts?
var sharedMounted bool
var sharedMountsLock sync.Mutex

func SetupSharedMounts() error {
	// Check if we already went through this
	if sharedMounted {
		return nil
	}

	// Get a lock to prevent races
	sharedMountsLock.Lock()
	defer sharedMountsLock.Unlock()

	// Check if already setup
	path := shared.VarPath("shmounts")
	if shared.IsMountPoint(path) {
		sharedMounted = true
		return nil
	}

	// Mount a new tmpfs
	if err := unix.Mount("tmpfs", path, "tmpfs", 0, "size=100k,mode=0711"); err != nil {
		return err
	}

	// Mark as MS_SHARED and MS_REC
	var flags uintptr = unix.MS_SHARED | unix.MS_REC
	if err := unix.Mount(path, path, "none", flags, ""); err != nil {
		return err
	}

	sharedMounted = true
	return nil
}
