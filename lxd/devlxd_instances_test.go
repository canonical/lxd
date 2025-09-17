package main

import (
	"reflect"
	"testing"

	"github.com/canonical/lxd/lxd/device/filters"
	"github.com/canonical/lxd/shared/api"
)

func Test_generateDevLXDInstanceDevices(t *testing.T) {
	type Devices map[string]map[string]string
	type Config map[string]string

	identityID := "test123"

	// AccessValidator that checks if volume management is enabled and
	// if the device is a custom volume disk.
	allowCustomVolumeAccess := func(isVolumeManagementEnabled bool) func(d map[string]string) bool {
		return func(device map[string]string) bool {
			return isVolumeManagementEnabled && filters.IsCustomVolumeDisk(device)
		}
	}

	// Returns a sample custom volume disk device.
	customVolumeDevice := func(keyValuePairs ...string) map[string]string {
		device := map[string]string{
			"type":   "disk",
			"source": "testvol",
			"pool":   "testpool",
		}

		for i := 0; i < len(keyValuePairs)-1; i += 2 {
			if i+1 >= len(keyValuePairs) {
				break
			}

			device[keyValuePairs[i]] = keyValuePairs[i+1]
		}

		return device
	}

	tests := []struct {
		TestName        string
		CurrentInstance api.Instance                   // Existing instance data.
		RequestInstance api.DevLXDInstancePut          // Instance data from the request.
		AccessValidator func(d map[string]string) bool // Function that determines if devLXD can manage the device.
		ExpectConfig    map[string]string              // expected configuration after patch.
		ExpectDevices   map[string]map[string]string   // expected Devices after patch.
		ExpectErr       string
	}{
		{
			TestName: "Create device adds device and owner",
			RequestInstance: api.DevLXDInstancePut{
				Devices: Devices{"disk1": customVolumeDevice()},
			},
			AccessValidator: allowCustomVolumeAccess(true),
			ExpectDevices:   Devices{"disk1": customVolumeDevice()},
			ExpectConfig:    Config{"volatile.disk1.devlxd.owner": identityID},
		},
		{
			TestName: "Update device modifies device and sets owner",
			CurrentInstance: api.Instance{
				Devices: Devices{"disk1": customVolumeDevice("size", "5GiB")},
				Config:  Config{"volatile.disk1.devlxd.owner": identityID},
			},
			RequestInstance: api.DevLXDInstancePut{
				Devices: Devices{"disk1": customVolumeDevice("size", "10GiB")},
			},
			AccessValidator: allowCustomVolumeAccess(true),
			ExpectDevices:   Devices{"disk1": customVolumeDevice("size", "10GiB")},
			ExpectConfig:    Config{"volatile.disk1.devlxd.owner": identityID},
		},
		{
			TestName: "Unowned device cannot be updated",
			CurrentInstance: api.Instance{
				ExpandedDevices: Devices{"disk1": customVolumeDevice("size", "5GiB")},
				Config:          Config{"volatile.disk1.devlxd.owner": "another-id"},
			},
			RequestInstance: api.DevLXDInstancePut{
				Devices: Devices{"disk1": customVolumeDevice("size", "10GiB")},
			},
			AccessValidator: allowCustomVolumeAccess(true),
			ExpectErr:       "Not authorized to manage device \"disk1\"",
		},
		{
			TestName: "Delete device removes device and owner",
			CurrentInstance: api.Instance{
				Devices: Devices{"disk1": customVolumeDevice()},
				Config:  Config{"volatile.disk1.devlxd.owner": identityID},
			},
			RequestInstance: api.DevLXDInstancePut{
				Devices: Devices{"disk1": nil},
			},
			AccessValidator: allowCustomVolumeAccess(true),
			ExpectDevices:   Devices{"disk1": nil},
			ExpectConfig:    Config{"volatile.disk1.devlxd.owner": ""},
		},
		{
			TestName: "Removal of device with no owner is ignored",
			CurrentInstance: api.Instance{
				Devices: Devices{"disk1": customVolumeDevice()},
			},
			RequestInstance: api.DevLXDInstancePut{
				Devices: Devices{"disk1": nil},
			},
			AccessValidator: allowCustomVolumeAccess(true),
			ExpectDevices:   Devices{},
			ExpectConfig:    Config{},
		},
		{
			TestName: "Removal of unowned device is ignored",
			CurrentInstance: api.Instance{
				Devices: Devices{"disk1": customVolumeDevice()},
				Config:  Config{"volatile.disk1.devlxd.owner": "another-id"},
			},
			RequestInstance: api.DevLXDInstancePut{
				Devices: Devices{"disk1": nil},
			},
			AccessValidator: allowCustomVolumeAccess(true),
			ExpectDevices:   Devices{},
			ExpectConfig:    Config{},
		},
		{
			TestName: "Failed access validator denies device creation",
			RequestInstance: api.DevLXDInstancePut{
				Devices: Devices{"disk1": customVolumeDevice()},
			},
			AccessValidator: allowCustomVolumeAccess(false),
			ExpectErr:       "Not authorized to manage device \"disk1\"",
		},
		{
			TestName: "Failed access validator denies device update",
			CurrentInstance: api.Instance{
				Devices: Devices{"disk1": customVolumeDevice("size", "5GiB")},
				Config:  Config{"volatile.disk1.devlxd.owner": identityID},
			},
			RequestInstance: api.DevLXDInstancePut{
				Devices: Devices{"disk1": customVolumeDevice("size", "10GiB")},
			},
			AccessValidator: allowCustomVolumeAccess(false),
			ExpectErr:       "Not authorized to manage device \"disk1\"",
		},
		{
			TestName: "Failed access validator denies device deletion",
			CurrentInstance: api.Instance{
				Devices: Devices{"disk1": customVolumeDevice()},
				Config:  Config{"volatile.disk1.devlxd.owner": identityID},
			},
			RequestInstance: api.DevLXDInstancePut{
				Devices: Devices{"disk1": nil},
			},
			AccessValidator: allowCustomVolumeAccess(false),
			ExpectErr:       "Not authorized to delete device \"disk1\"",
		},
	}

	for _, test := range tests {
		t.Run(test.TestName, func(t *testing.T) {
			if test.CurrentInstance.Config == nil {
				test.CurrentInstance.Config = map[string]string{}
			}

			if test.CurrentInstance.Devices == nil {
				test.CurrentInstance.Devices = map[string]map[string]string{}
			}

			newDevices, newConfig, err := generateDevLXDInstanceDevices(test.CurrentInstance, test.RequestInstance, identityID, test.AccessValidator)

			if test.ExpectErr != "" {
				if err == nil {
					t.Fatal("Test expects and error, but error is nil!")
				}

				if err.Error() != test.ExpectErr {
					t.Fatalf("Test expects error %q, but got %q", test.ExpectErr, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("Test expects no error, but error is %q", err.Error())
				}

				// Confirm expected config keys are present in patched instance.
				if test.ExpectConfig != nil && !reflect.DeepEqual(test.ExpectConfig, newConfig) {
					t.Fatalf("Expected config to be:\n%+v\nFound:\n%+v", test.ExpectConfig, newConfig)
				}

				// Confirm expected devices are present in patched instance.
				if test.ExpectDevices != nil && !reflect.DeepEqual(test.ExpectDevices, newDevices) {
					t.Fatalf("Expected devices to be:\n%+v\nFound:\n%+v", test.ExpectDevices, newDevices)
				}
			}
		})
	}
}
