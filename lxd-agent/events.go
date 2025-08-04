package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	agentAPI "github.com/canonical/lxd/lxd-agent/api"
	"github.com/canonical/lxd/lxd/device/filters"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/ws"
)

var eventsCmd = APIEndpoint{
	Path: "events",

	Get:  APIEndpointAction{Handler: eventsGet},
	Post: APIEndpointAction{Handler: eventsPost},
}

type eventsServe struct {
	d *Daemon
}

// Render starts event socket.
func (r *eventsServe) Render(w http.ResponseWriter, request *http.Request) error {
	return eventsSocket(r.d, request, w)
}

func (r *eventsServe) String() string {
	return "event handler"
}

func eventsSocket(d *Daemon, r *http.Request, w http.ResponseWriter) error {
	typeStr := r.FormValue("type")
	if typeStr == "" {
		// We add 'config' here to allow listeners on /dev/lxd/sock to receive config changes.
		typeStr = "logging,operation,lifecycle,config,device"
	}

	var listenerConnection events.EventListenerConnection

	// If the client has not requested a websocket connection then fallback to long polling event stream mode.
	if r.Header.Get("Upgrade") == "websocket" {
		// Upgrade the connection to websocket
		conn, err := ws.Upgrader.Upgrade(w, r, nil)
		if err != nil {
			return err
		}

		defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

		listenerConnection = events.NewWebsocketListenerConnection(conn)
	} else {
		h, ok := w.(http.Hijacker)
		if !ok {
			return errors.New("Missing implemented http.Hijacker interface")
		}

		conn, _, err := h.Hijack()
		if err != nil {
			return err
		}

		defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

		listenerConnection, err = events.NewStreamListenerConnection(conn)
		if err != nil {
			return err
		}
	}

	// As we don't know which project we are in, subscribe to events from all projects.
	listener, err := d.events.AddListener("", true, nil, listenerConnection, strings.Split(typeStr, ","), nil, nil, nil)
	if err != nil {
		return err
	}

	listener.Wait(r.Context())

	return nil
}

func eventsGet(d *Daemon, r *http.Request) response.Response {
	return &eventsServe{d: d}
}

func eventsPost(d *Daemon, r *http.Request) response.Response {
	var event api.Event

	err := json.NewDecoder(r.Body).Decode(&event)
	if err != nil {
		return response.InternalError(err)
	}

	err = d.events.Send("", event.Type, event.Metadata)
	if err != nil {
		return response.InternalError(err)
	}

	// Handle device related actions locally.
	go eventsProcess(event)

	return response.SyncResponse(true, nil)
}

func eventsProcess(event api.Event) {
	// We currently only need to react to device events.
	if event.Type != "device" {
		return
	}

	type deviceEvent struct {
		Action agentAPI.DeviceEventAction `json:"action"`
		Config map[string]string          `json:"config"`
		Name   string                     `json:"name"`
		Mount  instancetype.VMAgentMount  `json:"mount"`
	}

	e := deviceEvent{}
	err := json.Unmarshal(event.Metadata, &e)
	if err != nil {
		return
	}

	// Only handle device additions and removals.
	if e.Action != agentAPI.DeviceAdded && e.Action != agentAPI.DeviceRemoved {
		return
	}

	// We only handle disk hotplug/removal.
	if !filters.IsDisk(e.Config) {
		return
	}

	// And only for path based devices.
	targetPath := e.Config["path"]
	if targetPath == "" {
		return
	}

	mntSource := "lxd_" + e.Name
	if e.Mount.Source != "" {
		mntSource = e.Mount.Source
	}

	l := logger.AddContext(logger.Ctx{"type": "virtiofs", "source": mntSource, "path": targetPath})

	// Reject path containing "..".
	if strings.Contains(targetPath, "..") {
		l.Error("Invalid path containing '..'")
		return
	}

	// If the path is not absolute, the mount will be created relative to the current directory.
	// (since the mount command executed below originates from the `lxd-agent` binary that is in the `/run/lxd_agent` directory).
	// This is not ideal and not consistent with the way mounts are handled with containers. For consistency make the path absolute.
	targetPath, err = filepath.Abs(targetPath)
	if err != nil || !strings.HasPrefix(targetPath, "/") {
		l.Error("Failed to make path absolute")
		return
	}

	switch e.Action {
	case agentAPI.DeviceAdded:
		_ = os.MkdirAll(targetPath, 0755)

		// Parse mount options, if provided.
		var args []string
		if len(e.Mount.Options) > 0 {
			args = append(args, "-o", strings.Join(e.Mount.Options, ","))
		}

		args = append(args, "-t", "virtiofs", mntSource, targetPath)

		// Attempt to perform the mount.
		for range 5 {
			_, err = shared.RunCommandContext(context.Background(), "mount", args...)
			if err == nil {
				l.Info("Mounted hotplug")
				return
			}

			time.Sleep(500 * time.Millisecond)
		}

		l.Info("Failed to mount hotplug", logger.Ctx{"err": err})
	case agentAPI.DeviceRemoved:
		mountInfoFile, err := os.Open("/proc/self/mountinfo")
		if err != nil {
			l.Error("Error opening /proc/self/mountinfo", logger.Ctx{"err": err})
			return
		}

		defer mountInfoFile.Close()

		var mountPoint string
		scanner := bufio.NewScanner(mountInfoFile)
		trimmedPath := strings.TrimSuffix(targetPath, "/")

		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())

			// Check that the mount point that matches the target path exists.
			if len(fields) >= 10 && fields[4] == trimmedPath {
				mountPoint = fields[4]
				break
			}
		}

		err = scanner.Err()
		if err != nil {
			l.Error("Error reading /proc/self/mountinfo", logger.Ctx{"err": err})
			return
		}

		if mountPoint == "" {
			l.Error("Mount point not found")
			return
		}

		err = unix.Unmount(mountPoint, unix.MNT_DETACH)
		if err != nil {
			l.Error("Failed to unmount", logger.Ctx{"err": err, "mountPoint": mountPoint})
			return
		}
	}
}
