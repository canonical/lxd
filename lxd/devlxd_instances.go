package main

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/device/filters"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

type devLXDDeviceAccessValidator func(device map[string]string) bool

var devLXDInstanceEndpoint = devLXDAPIEndpoint{
	Path:  "instances/{name}",
	Get:   devLXDAPIEndpointAction{Handler: devLXDInstanceGetHandler, AccessHandler: allowDevLXDPermission(entity.TypeInstance, auth.EntitlementCanView, "name")},
	Patch: devLXDAPIEndpointAction{Handler: devLXDInstancePatchHandler, AccessHandler: allowDevLXDPermission(entity.TypeInstance, auth.EntitlementCanEdit, "name")},
}

func devLXDInstanceGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityMgmtVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Allow access only to the projectName where current instance is running.
	projectName := inst.Project().Name
	targetInstName := mux.Vars(r)["name"]

	// Get identity from the request context.
	identity, err := getDevLXDIdentity(r.Context())
	if identity == nil {
		return response.DevLXDErrorResponse(err)
	}

	// Fetch instance.
	targetInst := api.Instance{}

	url := api.NewURL().Path("1.0", "instances", targetInstName).Project(projectName).WithQuery("recursion", "1").URL
	req, err := request.NewRequestWithContext(r.Context(), http.MethodGet, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := instanceGet(d, req)
	etag, err := RenderToStruct(req, resp, &targetInst)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Get owned devices.
	ownedDevices := getDevLXDOwnedDevices(targetInst, identity.Identifier)

	// Filter devices that are not accessible to devLXD.
	deviceAccessChecker := newDevLXDDeviceAccessValidator(inst)
	for name, device := range ownedDevices {
		if !deviceAccessChecker(device) {
			delete(ownedDevices, name)
		}
	}

	// Convert to devLXD type.
	respInst := api.DevLXDInstance{
		Name:    targetInst.Name,
		Devices: ownedDevices,
	}

	return response.DevLXDResponseETag(http.StatusOK, respInst, "json", etag)
}

func devLXDInstancePatchHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityMgmtVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Allow access only to the project where current instance is running.
	projectName := inst.Project().Name
	targetInstName := mux.Vars(r)["name"]

	// Get identity from the request context.
	identity, err := getDevLXDIdentity(r.Context())
	if identity == nil {
		return response.DevLXDErrorResponse(err)
	}

	var reqInst api.DevLXDInstancePut

	err = json.NewDecoder(r.Body).Decode(&reqInst)
	if err != nil {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "Failed decoding request body: %w", err))
	}

	// Fetch instance.
	targetInst := api.Instance{}

	url := api.NewURL().Path("1.0", "instances", targetInstName).Project(projectName).WithQuery("recursion", "1").URL
	req, err := request.NewRequestWithContext(r.Context(), http.MethodGet, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := instanceGet(d, req)
	etag, err := RenderToStruct(req, resp, &targetInst)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Use etag from the request, if provided. Otherwise, use the etag
	// returned when fetching the existing instance.
	reqETag := r.Header.Get("If-Match")
	if reqETag != "" {
		etag = reqETag
	}

	// Merge new devices with existing ones.
	deviceChecker := newDevLXDDeviceAccessValidator(inst)
	err = patchDevLXDInstanceDevices(&targetInst, reqInst, identity.Identifier, deviceChecker)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Update instance.
	reqBody := api.InstancePut{
		Config:  targetInst.Config,  // Update device ownership.
		Devices: targetInst.Devices, // Update devices.
	}

	url = api.NewURL().Path("1.0", "instances", targetInstName).Project(projectName).URL
	req, err = request.NewRequestWithContext(r.Context(), http.MethodPatch, url.String(), reqBody, etag)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp = instancePatch(d, req)
	err = Render(req, resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDResponse(http.StatusOK, "", "raw")
}

// patchDevLXDInstanceDevices updates an existing instance (api.Instance) with devices from the
// request instance (api.DevLXDInstancePut), and adjusts the device ownership configuration
// accordingly.
//
// Device actions are determined as follows:
// - Add:
//   - Condition: New device that devLXD can manage and is not present in the existing devices.
//   - Action:    Adds new device to the instance and set its owner in the instance config.
//
// - Update:
//   - Condition: Existing device that devLXD can manage and is present in the new devices.
//   - Action:    Updates existing device in the instance.
//
// - Remove:
//   - Condition: Existing device that devLXD can manage is set to "null" in the request.
//   - Action:    Removes existing device from the instance and removes its owner from the instance config.
func patchDevLXDInstanceDevices(inst *api.Instance, req api.DevLXDInstancePut, identityID string, isDeviceAccessible devLXDDeviceAccessValidator) error {
	newDevices := make(map[string]map[string]string)
	newConfig := make(map[string]string)

	// Pass local devices, as non-local devices cannot be owned.
	ownedDevices := getDevLXDOwnedDevices(*inst, identityID)

	// Merge new devices into existing ones.
	for name, device := range req.Devices {
		ownedDevice, isOwned := ownedDevices[name]

		if device == nil {
			// Device is being removed. Check if the device is owned.
			// For consistency with LXD API, we do not error out if
			// the device is not found.
			if !isOwned {
				continue
			}

			// Ensure devLXD has sufficient permissions to manage the device.
			// Pass old device to the validator, as new device is nil.
			if isDeviceAccessible != nil && !isDeviceAccessible(ownedDevice) {
				return api.StatusErrorf(http.StatusForbidden, "Not authorized to delete device %q", name)
			}

			// Device is removed, so remove the owner config key.
			newConfig["volatile."+name+".devlxd.owner"] = ""
		} else {
			_, exists := inst.ExpandedDevices[name]

			// Device is being either added or updated.
			// Ensure devLXD has sufficient permissions to manage the device.
			// If the device already exists (update), ensure that it is owned.
			if (exists && !isOwned) || (isDeviceAccessible != nil && !isDeviceAccessible(device)) {
				return api.StatusErrorf(http.StatusForbidden, "Not authorized to manage device %q", name)
			}

			// At this point we know that the ownership is correct (there is
			// no existing unowned device), so we can safely set the device owner
			// in instance configuration.
			newConfig["volatile."+name+".devlxd.owner"] = identityID
		}

		newDevices[name] = device
	}

	inst.Devices = newDevices
	inst.Config = newConfig
	return nil
}

// getDevLXDOwnedDevices extracts instance devices that are owned by the provided identity.
func getDevLXDOwnedDevices(inst api.Instance, identityID string) map[string]map[string]string {
	ownedDevices := make(map[string]map[string]string)

	for name, device := range inst.Devices {
		if inst.Config["volatile."+name+".devlxd.owner"] == identityID {
			ownedDevices[name] = device
		}
	}

	return ownedDevices
}

// newDevLXDDeviceAccessValidator returns a device validator function that
// checks if the given device is accessible by the devLXD.
//
// For example, disk device is accessible if the appropriate security flag
// is enabled on the instance and the device represents a custom volume.
func newDevLXDDeviceAccessValidator(inst instance.Instance) devLXDDeviceAccessValidator {
	diskDeviceAllowed := hasInstanceSecurityFeatures(inst, devLXDSecurityMgmtVolumesKey)

	return func(device map[string]string) bool {
		return diskDeviceAllowed && filters.IsCustomVolumeDisk(device)
	}
}
