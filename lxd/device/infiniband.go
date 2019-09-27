package device

import (
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
)

// infinibandTypes defines the supported infiniband type devices and defines their creation functions.
var infinibandTypes = map[string]func() device{
	"physical": func() device { return &infinibandPhysical{} },
	"sriov":    func() device { return &infinibandSRIOV{} },
}

// infinibandLoadByType returns an Infiniband device instantiated with supplied config.
func infinibandLoadByType(c deviceConfig.Device) device {
	f := infinibandTypes[c["nictype"]]
	if f != nil {
		return f()
	}
	return nil
}
