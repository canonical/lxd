package nodes

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/r3labs/diff/v3"
)

var (
	ProfilePrefix  = "profile_"
	DevicePrefix   = "device_"
	InstancePrefix = "instance_"
)

type DeviceNode struct {
	baseNode

	Project string
	Type    string
	Name    string
}

func (d *DeviceNode) Diff(n any) (diff.Changelog, error) {
	return nil, nil
}

func GenerateDeviceHumanID(project string, location string, deviceType string, deviceName string) string {
	return humanIDEncode(DevicePrefix, project, location, deviceType, deviceName)
}

func NewDeviceNode(project string, location string, deviceType string, deviceName string, device map[string]string, id int64) *DeviceNode {
	return &DeviceNode{
		baseNode: baseNode{
			data:    device,
			id:      id,
			humanID: GenerateDeviceHumanID(project, location, deviceType, deviceName),
		},
		Project: project,
		Type:    deviceType,
		Name:    deviceName,
	}
}

type ProfileNode struct {
	baseNode

	Project string
	Name    string
}

func (p *ProfileNode) Diff(n any) (diff.Changelog, error) {
	return nil, nil
}

func (pn *ProfileNode) Renamable() bool {
	return true
}

func GenerateProfileHumanID(project string, name string) string {
	return humanIDEncode(ProfilePrefix, project, name)
}

func NewProfileNode(project string, name string, profile api.Profile, id int64) *ProfileNode {
	return &ProfileNode{
		baseNode: baseNode{
			data:    profile,
			id:      id,
			humanID: GenerateProfileHumanID(project, name),
		},
		Project: project,
		Name:    profile.Name,
	}
}

type InstanceNode struct {
	baseNode

	Project string
	Name    string
}

func (in *InstanceNode) Diff(n any) (diff.Changelog, error) {
	return nil, nil
}

func (in *InstanceNode) Renamable() bool {
	return true
}

func GenerateInstanceHumanID(project string, name string) string {
	return humanIDEncode(InstancePrefix, project, name)
}

func NewInstanceNode(project string, name string, instance api.Instance, id int64) *InstanceNode {
	return &InstanceNode{
		baseNode: baseNode{
			data:    instance,
			id:      id,
			humanID: GenerateInstanceHumanID(project, name),
		},
		Project: project,
		Name:    name,
	}
}
