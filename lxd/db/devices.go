// +build linux,cgo,!agent

package db

import (
	"fmt"
)

func deviceTypeToString(t int) (string, error) {
	switch t {
	case 0:
		return "none", nil
	case 1:
		return "nic", nil
	case 2:
		return "disk", nil
	case 3:
		return "unix-char", nil
	case 4:
		return "unix-block", nil
	case 5:
		return "usb", nil
	case 6:
		return "gpu", nil
	case 7:
		return "infiniband", nil
	case 8:
		return "proxy", nil
	case 9:
		return "unix-hotplug", nil
	case 10:
		return "tpm", nil
	case 11:
		return "pci", nil
	default:
		return "", fmt.Errorf("Invalid device type %d", t)
	}
}

func deviceTypeToInt(t string) (int, error) {
	switch t {
	case "none":
		return 0, nil
	case "nic":
		return 1, nil
	case "disk":
		return 2, nil
	case "unix-char":
		return 3, nil
	case "unix-block":
		return 4, nil
	case "usb":
		return 5, nil
	case "gpu":
		return 6, nil
	case "infiniband":
		return 7, nil
	case "proxy":
		return 8, nil
	case "unix-hotplug":
		return 9, nil
	case "tpm":
		return 10, nil
	case "pci":
		return 11, nil
	default:
		return -1, fmt.Errorf("Invalid device type %s", t)
	}
}
