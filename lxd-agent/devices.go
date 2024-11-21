package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
)

var devicesCmd = APIEndpoint{
	Path: "devices",

	Delete: APIEndpointAction{Handler: deviceDelete},
}

// deviceDelete handles the removal of a device from the VM agent.
// e.g, if the device is a disk mount, this will cleanly unmount it and remove it if necessary.
func deviceDelete(d *Daemon, r *http.Request) response.Response {
	var device api.AgentDeviceRemove

	err := json.NewDecoder(r.Body).Decode(&device)
	if err != nil {
		return response.InternalError(err)
	}

	// We only support disk devices for now
	if device.Type != "disk" {
		return response.BadRequest(fmt.Errorf("Device type %q not supported for removal within VM agent", device.Type))
	}

	targetPath := device.Config["path"]

	if !filepath.IsAbs(targetPath) {
		return response.SmartError(fmt.Errorf("The device path must be absolute: %q", device.Config["path"]))
	}

	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return response.SmartError(fmt.Errorf("Error opening /proc/self/mountinfo: %v", err))
	}

	defer file.Close()

	var mountPoints []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 10 && strings.HasPrefix(fields[4], strings.TrimSuffix(targetPath, "/")) {
			mountPoints = append(mountPoints, fields[4])
		}
	}

	err = scanner.Err()
	if err != nil {
		return response.SmartError(fmt.Errorf("Error reading /proc/self/mountinfo: %v", err))
	}

	if len(mountPoints) == 0 {
		return response.SmartError(fmt.Errorf("No mount points found for %s", targetPath))
	}

	// Reverse the slice to unmount in reverse order.
	// This is needed to unmount potential over-mounts first.
	for i, j := 0, len(mountPoints)-1; i < j; i, j = i+1, j-1 {
		mountPoints[i], mountPoints[j] = mountPoints[j], mountPoints[i]
	}

	for _, mountPoint := range mountPoints {
		err = unix.Unmount(mountPoint, unix.MNT_DETACH)
		if err != nil {
			return response.SmartError(fmt.Errorf("Error unmounting %s: %v", mountPoint, err))
		}
	}

	// Now that the unmount has occurred,
	// check if we need to remove the target path.
	if device.Volatile != nil {
		path, ok := device.Volatile["last_state.created"]
		if ok {
			if targetPath == path {
				err = os.Remove(filepath.Clean(targetPath))
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed to remove last state file: %v", err))
				}
			}
		}
	}

	return response.EmptySyncResponse
}
