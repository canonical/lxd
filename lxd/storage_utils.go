package main

import (
	"os"
	"syscall"
	"time"
)

// Useful functions for unreliable backends
func tryMount(src string, dst string, fs string, flags uintptr, options string) error {
	var err error

	for i := 0; i < 20; i++ {
		err = syscall.Mount(src, dst, fs, flags, options)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return err
	}

	return nil
}

func tryUnmount(path string, flags int) error {
	var err error

	for i := 0; i < 20; i++ {
		err = syscall.Unmount(path, flags)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return err
	}

	return nil
}

// Default permissions for folders in ${LXD_DIR}
const containersDirMode os.FileMode = 0755
const customDirMode os.FileMode = 0755
const imagesDirMode os.FileMode = 0700
const snapshotsDirMode os.FileMode = 0700

// Driver permissions for driver specific folders in ${LXD_DIR}
// zfs
const deletedDirMode os.FileMode = 0700
