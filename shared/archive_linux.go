package shared

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
)

func Unpack(file string, path string, blockBackend bool, runningInUserns bool, tracker *ioprogress.ProgressTracker) error {
	extractArgs, extension, _, err := DetectCompression(file)
	if err != nil {
		return err
	}

	command := ""
	args := []string{}
	var reader io.Reader
	if strings.HasPrefix(extension, ".tar") {
		command = "tar"
		if runningInUserns {
			args = append(args, "--wildcards")
			args = append(args, "--exclude=dev/*")
			args = append(args, "--exclude=./dev/*")
			args = append(args, "--exclude=rootfs/dev/*")
			args = append(args, "--exclude=rootfs/./dev/*")
		}
		args = append(args, "-C", path, "--numeric-owner", "--xattrs-include=*")
		args = append(args, extractArgs...)
		args = append(args, "-")

		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()

		reader = f

		// Attach the ProgressTracker if supplied.
		if tracker != nil {
			fsinfo, err := f.Stat()
			if err != nil {
				return err
			}

			tracker.Length = fsinfo.Size()
			reader = &ioprogress.ProgressReader{
				ReadCloser: f,
				Tracker:    tracker,
			}
		}
	} else if strings.HasPrefix(extension, ".squashfs") {
		// unsquashfs does not support reading from stdin,
		// so ProgressTracker is not possible.
		command = "unsquashfs"
		args = append(args, "-f", "-d", path, "-n")

		// Limit unsquashfs chunk size to 10% of memory and up to 256MB (default)
		// When running on a low memory system, also disable multi-processing
		mem, err := DeviceTotalMemory()
		mem = mem / 1024 / 1024 / 10
		if err == nil && mem < 256 {
			args = append(args, "-da", fmt.Sprintf("%d", mem), "-fr", fmt.Sprintf("%d", mem), "-p", "1")
		}

		args = append(args, file)
	} else {
		return fmt.Errorf("Unsupported image format: %s", extension)
	}

	err = RunCommandWithFds(reader, nil, command, args...)
	if err != nil {
		// Check if we ran out of space
		fs := unix.Statfs_t{}

		err1 := unix.Statfs(path, &fs)
		if err1 != nil {
			return err1
		}

		// Check if we're running out of space
		if int64(fs.Bfree) < 10 {
			if blockBackend {
				return fmt.Errorf("Unable to unpack image, run out of disk space (consider increasing your pool's volume.size)")
			} else {
				return fmt.Errorf("Unable to unpack image, run out of disk space")
			}
		}

		logger.Debugf("Unpacking failed")
		logger.Debugf(err.Error())
		return fmt.Errorf("Unpack failed, %s.", err)
	}

	return nil
}
