package cdi

import (
	"fmt"

	"tags.cncf.io/container-device-interface/pkg/parser"
)

// Vendor represents the compatible CDI vendor.
type Vendor string

const (
	// NVIDIA represents the Nvidia CDI vendor.
	NVIDIA Vendor = "nvidia.com"
)

// ToVendor converts a string to a CDI vendor.
func ToVendor(vendor string) (Vendor, error) {
	switch vendor {
	case string(NVIDIA):
		return NVIDIA, nil
	default:
		return "", fmt.Errorf("Invalid CDI vendor (%q)", vendor)
	}
}

// Class represents the compatible CDI class.
type Class string

const (
	// GPU is a single discrete GPU.
	GPU Class = "gpu"
	// IGPU is an integrated GPU.
	IGPU Class = "igpu"
	// MIG is a single MIG compatible GPU.
	MIG Class = "mig"
)

// ToClass converts a string to a CDI class.
func ToClass(c string) (Class, error) {
	switch c {
	case string(GPU):
		return GPU, nil
	case string(IGPU):
		return IGPU, nil
	case string(MIG):
		return MIG, nil
	default:
		return "", fmt.Errorf("Invalid CDI class (%q)", c)
	}
}

// ID represents a Container Device Interface (CDI) identifier.
//
// +------------+-------+------------------------------------------+
// |   Vendor   | Class |                Name                      |
// +---------------------------------------------------------------+
// | nvidia.com |  gpu  | [dev_idx], [dev_uuid] or `all`           |
// |            |  mig  | [dev_idx]:[mig_idx], [dev_uuid] or `all` |
// |            |  igpu | [dev_idx], [dev_uuid] or `all`           |
// +------------+-------+------------------------------------------+
//
// Examples:
//   - nvidia.com/gpu=0
//   - nvidia.com/gpu=d1f1c76e-7a72-487e-b121-e6d2e5555dc8
//   - nvidia.com/gpu=all
//   - nvidia.com/mig=0:1
//   - nvidia.com/igpu=0
type ID struct {
	Vendor Vendor
	Class  Class
	Name   string
}

// String returns the string representation of the ID.
func (id ID) String() string {
	return fmt.Sprintf("%s/%s=%s", id.Vendor, id.Class, id.Name)
}

// Empty returns true if the ID is empty.
func (id ID) Empty() bool {
	return id.Vendor == "" && id.Class == "" && id.Name == ""
}

// ToCDI converts a string identifier to a CDI ID.
func ToCDI(id string) (ID, error) {
	vendor, class, name, err := parser.ParseQualifiedName(id)
	if err != nil {
		// The ID is not a valid CDI qualified name but it could be a valid DRM device ID.
		return ID{}, nil
	}

	vendorType, err := ToVendor(vendor)
	if err != nil {
		return ID{}, err
	}

	classType, err := ToClass(class)
	if err != nil {
		return ID{}, err
	}

	return ID{Vendor: vendorType, Class: classType, Name: name}, nil
}
