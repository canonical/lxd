package cdi

import (
	"fmt"
	"regexp"
)

var generalCDIRegex = regexp.MustCompile(`^(\S+)\/(\S+)=(\S+)$`)
var locatorRegex = regexp.MustCompile(`^([a-zA-Z]+)([0-9]+)?:?([0-9]+)?$`)

// Vendor represents the compatible CDI vendor.
type Vendor string

const (
	// Nvidia represents the Nvidia CDI vendor.
	Nvidia Vendor = "nvidia.com"
)

// ToVendor converts a string to a CDI vendor.
func ToVendor(vendor string) (Vendor, error) {
	switch vendor {
	case string(Nvidia):
		return Nvidia, nil
	default:
		return "", fmt.Errorf("invalid CDI vendor (%q)", vendor)
	}
}

// Product represents the compatible CDI product.
type Product string

const (
	// GPU is a general GPU product.
	GPU Product = "gpu"
)

// ToProduct converts a string to a CDI product.
func ToProduct(product string) (Product, error) {
	switch product {
	case string(GPU):
		return GPU, nil
	default:
		return "", fmt.Errorf("invalid CDI product (%q)", product)
	}
}

// DeviceType represents the compatible CDI device type.
type DeviceType string

const (
	// SimpleGPU represents a single discrete GPU card.
	SimpleGPU DeviceType = "gpu"
	// All represents all the GPUs on a system.
	All DeviceType = "all"
	// MIG represents a single MIG compatible GPU card.
	MIG DeviceType = "mig"
	// IGPU represents a single iGPU device.
	IGPU DeviceType = "igpu"
)

// ToDeviceType converts a string to a CDI device type.
func ToDeviceType(deviceType string) (DeviceType, error) {
	switch deviceType {
	case string(SimpleGPU):
		return SimpleGPU, nil
	case string(All):
		return All, nil
	case string(MIG):
		return MIG, nil
	case string(IGPU):
		return IGPU, nil
	default:
		return "", fmt.Errorf("invalid CDI device type (%q)", deviceType)
	}
}

// ID represents a Container Device Interface (CDI) identifier.
//
// +------------+---------+------------+---------------------+
// |   Vendor   | Product | DeviceType |       Locator       |
// +---------------------------------------------------------+
// | nvidia.com |   gpu   |    gpu     | [dev_idx]           |
// |            |         |    all     | nil                 |
// |            |         |    mig     | [dev_idx]:[mig_idx] |
// |            |         |    igpu    | [dev_idx]           |
// +------------+---------+------------+---------------------+
//
// Examples:
//   - nvidia.com/gpu=gpu0
type ID interface {
	Vendor() Vendor
	Product() Product
	DeviceType() DeviceType
	Locator() []string
}

// commonGPUID represents a common GPU CDI identifier.
type commonGPUID struct {
	vendor     Vendor
	product    Product
	deviceType DeviceType
	deviceIdx  string
}

// Vendor returns the vendor of the GPU.
func (id commonGPUID) Vendor() Vendor {
	return id.vendor
}

// Product returns the product type.
func (id commonGPUID) Product() Product {
	return id.product
}

// DeviceType returns the device type.
func (id commonGPUID) DeviceType() DeviceType {
	return id.deviceType
}

// Locator returns the device location.
func (id commonGPUID) Locator() []string {
	if id.deviceType == All {
		return nil
	}

	return []string{id.deviceIdx}
}

// migGPUID represents a MIG GPU CDI identifier.
type migGPUID struct {
	commonGPUID

	migIdx string
}

// Locator returns a slice containing the device index
// in the first position and the MIG index in the second position.
func (id migGPUID) Locator() []string {
	return []string{id.deviceIdx, id.migIdx}
}

// ToCDI converts a string identifier to a CDI ID (this is vendor agnostic). Examples:
//
//	"nvidia.com/gpu=gpu0" -> commonGPUID{vendor: "nvidia.com", product: "gpu", deviceName: "gpu", deviceIdx: 0}
//	"nvidia.com/gpu=mig0:0" -> migGPUID{commonGPUID: commonGPUID{vendor: "nvidia.com", product: "gpu", deviceName: "mig", deviceIdx: 0}, migIdx: 0}
//	"amd.com/gpu=all" -> commonGPUID{vendor: "amd.com", product: "gpu", deviceName: "all", all: true}
func ToCDI(id string) (ID, error) {
	matches := generalCDIRegex.FindStringSubmatch(id)
	if len(matches) != 4 {
		// If the identifier does not match the general CDI format,
		// we do not return an error as this could be a valid DRM card ID
		return nil, nil
	}

	vendor, err := ToVendor(matches[1])
	if err != nil {
		return nil, err
	}

	product, err := ToProduct(matches[2])
	if err != nil {
		return nil, err
	}

	locatorMatches := locatorRegex.FindStringSubmatch(matches[3])
	if len(locatorMatches) < 2 {
		return nil, fmt.Errorf("invalid CDI locator (%q)", matches[3])
	}

	deviceType, err := ToDeviceType(locatorMatches[1])
	if err != nil {
		return nil, err
	}

	if deviceType == All {
		return commonGPUID{vendor: vendor, product: product, deviceType: deviceType}, nil
	}

	// The MIG nomenclature is specific to NVIDIA GPUs
	if deviceType == MIG && vendor == Nvidia {
		if len(locatorMatches) != 4 {
			return nil, fmt.Errorf("invalid MIG CDI locator (%q)", locatorMatches)
		}

		return migGPUID{commonGPUID: commonGPUID{vendor: vendor, product: product, deviceType: deviceType, deviceIdx: locatorMatches[2]}, migIdx: locatorMatches[3]}, nil
	}

	return commonGPUID{vendor: vendor, product: product, deviceType: deviceType, deviceIdx: locatorMatches[2]}, nil
}
