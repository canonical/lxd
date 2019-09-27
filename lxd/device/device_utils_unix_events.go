package device

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

// UnixEvent represents the properties of a Unix device inotify event.
type UnixEvent struct {
	Action string // The type of event, either add or remove.
	Path   string // The absolute source path on the host.
}

// UnixSubscription used to subcribe to specific events.
type UnixSubscription struct {
	Path    string                              // The absolute source path on the host.
	Handler func(UnixEvent) (*RunConfig, error) // The function to run when an event occurs.
}

// unixHandlers stores the event handler callbacks for Unix events.
var unixHandlers = map[string]UnixSubscription{}

// unixMutex controls access to the unixHandlers map.
var unixMutex sync.Mutex

// unixRegisterHandler registers a handler function to be called whenever a Unix device event occurs.
func unixRegisterHandler(s *state.State, instance Instance, deviceName, path string, handler func(UnixEvent) (*RunConfig, error)) error {
	if path == "" || handler == nil {
		return fmt.Errorf("Invalid subscription")
	}

	unixMutex.Lock()
	defer unixMutex.Unlock()

	// Null delimited string of project name, instance name and device name.
	key := fmt.Sprintf("%s\000%s\000%s", instance.Project(), instance.Name(), deviceName)
	unixHandlers[key] = UnixSubscription{
		Path:    path,
		Handler: handler,
	}

	// Add inotify watcher to its nearest existing ancestor.
	cleanDevDirPath := filepath.Dir(filepath.Clean(path))
	err := inotifyAddClosestLivingAncestor(s, cleanDevDirPath)
	if err != nil {
		return fmt.Errorf("Failed to add \"%s\" to inotify targets: %s", cleanDevDirPath, err)
	}

	logger.Debugf("Added \"%s\" to inotify targets", cleanDevDirPath)
	return nil
}

// unixUnregisterHandler removes a registered Unix handler function for a device.
func unixUnregisterHandler(s *state.State, instance Instance, deviceName string) error {
	unixMutex.Lock()
	defer unixMutex.Unlock()

	// Null delimited string of project name, instance name and device name.
	key := fmt.Sprintf("%s\000%s\000%s", instance.Project(), instance.Name(), deviceName)

	sub, exists := unixHandlers[key]
	if !exists {
		return nil
	}

	// Remove active subscription for this device.
	delete(unixHandlers, key)

	// Create a map of all unique living ancestor paths for all active subscriptions and count
	// how many subscriptions are using each living ancestor path.
	subsLivingAncestors := make(map[string]uint)
	for _, sub := range unixHandlers {

		exists, path := inotifyFindClosestLivingAncestor(filepath.Dir(sub.Path))
		if !exists {
			continue
		}

		subsLivingAncestors[path]++ // Count how many subscriptions are sharing a watcher.
	}

	// Identify which living ancestor path the subscription we just deleted was using.
	exists, ourSubPath := inotifyFindClosestLivingAncestor(filepath.Dir(sub.Path))

	// If we were the only subscription using the living ancestor path, then remove the watcher.
	if exists && subsLivingAncestors[ourSubPath] == 0 {
		err := inotifyDelWatcher(s, ourSubPath)
		if err != nil {
			return fmt.Errorf("Failed to remove \"%s\" from inotify targets: %s", ourSubPath, err)
		}
		logger.Debugf("Removed \"%s\" from inotify targets", ourSubPath)
	}

	return nil
}

// unixRunHandlers executes any handlers registered for Unix events.
func unixRunHandlers(state *state.State, event *UnixEvent) {
	unixMutex.Lock()
	defer unixMutex.Unlock()

	for key, sub := range unixHandlers {
		keyParts := strings.SplitN(key, "\000", 3)
		projectName := keyParts[0]
		instanceName := keyParts[1]
		deviceName := keyParts[2]

		// Delete subscription if no handler function defined.
		if sub.Handler == nil {
			delete(unixHandlers, key)
			continue
		}

		// Don't execute handler if subscription path and event paths don't match.
		if sub.Path != event.Path {
			continue
		}

		// Run handler function.
		runConf, err := sub.Handler(*event)
		if err != nil {
			logger.Error("Unix event hook failed", log.Ctx{"err": err, "project": projectName, "instance": instanceName, "device": deviceName, "path": sub.Path})
			continue
		}

		// If runConf supplied, load instance and call its Unix event handler function so
		// any instance specific device actions can occur.
		if runConf != nil {
			instance, err := InstanceLoadByProjectAndName(state, projectName, instanceName)
			if err != nil {
				logger.Error("Unix event loading instance failed", log.Ctx{"err": err, "project": projectName, "instance": instanceName, "device": deviceName})
				continue
			}

			err = instance.DeviceEventHandler(runConf)
			if err != nil {
				logger.Error("Unix event instance handler failed", log.Ctx{"err": err, "project": projectName, "instance": instanceName, "device": deviceName})
				continue
			}
		}
	}
}

// unixGetSubcribedPaths returns all the subcribed paths as a map keyed on path.
func unixGetSubcribedPaths() map[string]struct{} {
	unixMutex.Lock()
	defer unixMutex.Unlock()

	paths := make(map[string]struct{})

	for _, sub := range unixHandlers {
		paths[sub.Path] = struct{}{}
	}

	return paths
}

// unixNewEvent returns a newly created Unix device event struct.
// If an empty action is supplied then the action of the event is derived from whether the path
// exists (add) or not (removed). This allows the peculiarities of the inotify API to be somewhat
// masked by the consuming event handler functions.
func unixNewEvent(action string, path string) UnixEvent {
	if action == "" {
		if shared.PathExists(path) {
			action = "add"
		} else {
			action = "remove"
		}
	}

	return UnixEvent{
		Action: action,
		Path:   path,
	}
}
