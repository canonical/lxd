package device

import (
	"fmt"
	"strings"
	"sync"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/shared/logger"
)

// readyStateHandlers stores the handler callbacks registered for instance ready state changes.
// Handlers are passed the up-to-date instance and are expected to read its current ready state
// from it (e.g. via inst.LocalConfig()), so that they always reconcile against the latest value
// even if multiple notifications are coalesced into a single call.
var readyStateHandlers = map[string]func(inst instance.Instance) error{}

// readyStateMutex controls access to readyStateHandlers.
var readyStateMutex sync.Mutex

// readyStateHandlerKey returns the map key used to store a device's ready state handler.
func readyStateHandlerKey(projectName string, instanceName string, deviceName string) string {
	return fmt.Sprintf("%s\000%s\000%s", projectName, instanceName, deviceName)
}

// readyStateRegisterHandler registers a handler function to be called whenever the ready state of
// inst changes (see the devLXD "PUT /1.0/state" API and the instance_ready_state API extension).
func readyStateRegisterHandler(inst instance.Instance, deviceName string, handler func(inst instance.Instance) error) {
	readyStateMutex.Lock()
	defer readyStateMutex.Unlock()

	readyStateHandlers[readyStateHandlerKey(inst.Project().Name, inst.Name(), deviceName)] = handler
}

// readyStateUnregisterHandler removes a registered ready state handler function for a device.
func readyStateUnregisterHandler(inst instance.Instance, deviceName string) {
	readyStateMutex.Lock()
	defer readyStateMutex.Unlock()

	delete(readyStateHandlers, readyStateHandlerKey(inst.Project().Name, inst.Name(), deviceName))
}

// ReadyStateChanged runs any handlers registered by inst's devices for ready state change events.
func ReadyStateChanged(inst instance.Instance) {
	readyStateMutex.Lock()
	defer readyStateMutex.Unlock()

	prefix := fmt.Sprintf("%s\000%s\000", inst.Project().Name, inst.Name())
	for key, handler := range readyStateHandlers {
		deviceName, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}

		err := handler(inst)
		if err != nil {
			logger.Error("Instance ready state event hook failed", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "device": deviceName, "err": err})
		}
	}
}
