package resources

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

var sysClassDMIID = "/sys/class/dmi/id"
var systemType string

// HasDMI returns true if the system exposes DMI information via sysfs.
func HasDMI() bool {
	return pathExists(sysClassDMIID)
}

// GetSystem returns a filled api.ResourcesSystem struct ready for use by LXD.
func GetSystem() (*api.ResourcesSystem, error) {
	// Cache the system type
	if systemType == "" {
		systemType = systemGetType()
	}

	if !HasDMI() {
		lshwSystem := getSystemFromLshw()
		if lshwSystem == nil {
			return &api.ResourcesSystem{Type: systemType}, nil
		}

		lshwSystem.Type = systemType

		return lshwSystem, nil
	}

	var err error
	system := api.ResourcesSystem{Type: systemType}

	// Product UUID
	productUUIDPath := filepath.Join(sysClassDMIID, "product_uuid")
	content, err := os.ReadFile(productUUIDPath)
	if err != nil && !os.IsNotExist(err) && !os.IsPermission(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", productUUIDPath, err)
	}

	system.UUID = strings.TrimSpace(string(content))

	// Vendor
	vendorPath := filepath.Join(sysClassDMIID, "sys_vendor")
	content, err = os.ReadFile(vendorPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", vendorPath, err)
	}

	system.Vendor = strings.TrimSpace(string(content))

	// Product name
	productNamePath := filepath.Join(sysClassDMIID, "product_name")
	content, err = os.ReadFile(productNamePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", productNamePath, err)
	}

	system.Product = strings.TrimSpace(string(content))

	// Product family
	productFamilyPath := filepath.Join(sysClassDMIID, "product_family")
	content, err = os.ReadFile(productFamilyPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", productFamilyPath, err)
	}

	system.Family = strings.TrimSpace(string(content))

	// Product version
	productVersionPath := filepath.Join(sysClassDMIID, "product_version")
	content, err = os.ReadFile(productVersionPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", productVersionPath, err)
	}

	system.Version = strings.TrimSpace(string(content))

	// Product SKU
	productSKUPath := filepath.Join(sysClassDMIID, "product_sku")
	content, err = os.ReadFile(productSKUPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", productSKUPath, err)
	}

	system.Sku = strings.TrimSpace(string(content))

	// Product serial
	productSerialPath := filepath.Join(sysClassDMIID, "product_serial")
	content, err = os.ReadFile(productSerialPath)
	if err != nil && !os.IsNotExist(err) && !os.IsPermission(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", productSerialPath, err)
	}

	system.Serial = strings.TrimSpace(string(content))

	system.Firmware, err = systemGetFirmware()
	if err != nil {
		return nil, err
	}

	system.Chassis, err = systemGetChassis()
	if err != nil {
		return nil, err
	}

	system.Motherboard, err = systemGetMotherboard()
	if err != nil {
		return nil, err
	}

	return &system, nil
}

// getSystemFromLshw gets the system information from the `lshw` command.
func getSystemFromLshw() *api.ResourcesSystem {
	output, err := shared.RunCommandCLocale("lshw", "-json")
	if err != nil {
		return nil
	}

	type systemConfiguration struct {
		Chassis string `json:"chassis"` // system.chassis.type
		Family  string `json:"family"`  // system.family
		SKU     string `json:"sku"`     // system.sku
		UUID    string `json:"uuid"`    // system.uuid
	}

	type systemChildren struct {
		Children    []systemChildren `json:"children"`
		Description string           `json:"description"`
		ID          string           `json:"id"`
		Date        string           `json:"date"`    // system.firmware.date
		Product     string           `json:"product"` // system.motherboard.product
		Serial      string           `json:"serial"`  // system.motherboard.serial
		Vendor      string           `json:"vendor"`  // system.firmware.vendor, system.motherboard.vendor
		Version     string           `json:"version"` // system.firmware.version, system.motherboard.version
	}

	type system struct {
		Configuration systemConfiguration `json:"configuration"`
		Children      []systemChildren    `json:"children"`
		Product       string              `json:"product"` // system.product
		Serial        string              `json:"serial"`  // system.serial
		Vendor        string              `json:"vendor"`  // system.vendor
		Version       string              `json:"version"` // system.version
	}

	systemInfo := system{}

	err = json.Unmarshal([]byte(output), &systemInfo)
	if err != nil {
		return nil
	}

	ret := api.ResourcesSystem{
		Chassis: &api.ResourcesSystemChassis{
			Type: systemInfo.Configuration.Chassis,
		},
		Family:  systemInfo.Configuration.Family,
		Product: systemInfo.Product,
		Serial:  systemInfo.Serial,
		Sku:     systemInfo.Configuration.SKU,
		UUID:    systemInfo.Configuration.UUID,
		Vendor:  systemInfo.Vendor,
		Version: systemInfo.Version,
	}

	for _, child := range systemInfo.Children {
		if child.Description != "Motherboard" {
			continue
		}

		for _, subChild := range child.Children {
			if subChild.ID != "firmware" {
				continue
			}

			ret.Firmware = &api.ResourcesSystemFirmware{
				Vendor:  subChild.Vendor,
				Date:    subChild.Date,
				Version: subChild.Version,
			}

			break
		}

		ret.Motherboard = &api.ResourcesSystemMotherboard{
			Product: child.Product,
			Serial:  child.Serial,
			Vendor:  child.Vendor,
			Version: child.Version,
		}

		break
	}

	return &ret
}

func systemGetType() string {
	// If systemd-detect-virt is unavailable, the system type is unknown.
	_, err := exec.LookPath("systemd-detect-virt")
	if err != nil {
		return "unknown"
	}

	runDetectVirt := func(flag string) error {
		cmd := exec.Command("systemd-detect-virt", flag)
		return cmd.Run()
	}

	// If this returns 0, we're in a container.
	err = runDetectVirt("--container")
	if err == nil {
		return "container"
	}

	// If this returns 0, we're in a VM.
	err = runDetectVirt("--vm")
	if err == nil {
		return "virtual-machine"
	}

	// Since we're neither in a container nor a VM, we must be on a physical
	// machine.
	return "physical"
}

func systemGetFirmware() (*api.ResourcesSystemFirmware, error) {
	firmware := api.ResourcesSystemFirmware{}

	// Firmware vendor
	biosVendorPath := filepath.Join(sysClassDMIID, "bios_vendor")
	content, err := os.ReadFile(biosVendorPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", biosVendorPath, err)
	}

	firmware.Vendor = strings.TrimSpace(string(content))

	// Firmware date
	biosDatePath := filepath.Join(sysClassDMIID, "bios_date")
	content, err = os.ReadFile(biosDatePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", biosDatePath, err)
	}

	firmware.Date = strings.TrimSpace(string(content))

	// Firmware version
	biosVersionPath := filepath.Join(sysClassDMIID, "bios_version")
	content, err = os.ReadFile(biosVersionPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", biosVersionPath, err)
	}

	firmware.Version = strings.TrimSpace(string(content))

	return &firmware, nil
}

func systemGetChassis() (*api.ResourcesSystemChassis, error) {
	chassis := api.ResourcesSystemChassis{}

	// Chassis vendor
	chassisVendorPath := filepath.Join(sysClassDMIID, "chassis_vendor")
	content, err := os.ReadFile(chassisVendorPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", chassisVendorPath, err)
	}

	chassis.Vendor = strings.TrimSpace(string(content))

	// Chassis types according to the DMTF SMBIOS Spec
	chassisTypes := map[uint64]string{
		0x1:  "Other",
		0x2:  "Unknown",
		0x3:  "Desktop",
		0x4:  "Low Profile Desktop",
		0x5:  "Pizza Box",
		0x6:  "Mini Tower",
		0x7:  "Tower",
		0x8:  "Portable",
		0x9:  "Laptop",
		0xA:  "Notebook",
		0xB:  "Hand Held",
		0xC:  "Docking Station",
		0xD:  "All in One",
		0xE:  "Sub Notebook",
		0xF:  "Space-saving",
		0x10: "Lunch Box",
		0x11: "Main Server Chassis",
		0x12: "Expansion Chassis",
		0x13: "SubChassis",
		0x14: "Bus Expansion Chassis",
		0x15: "Peripheral Chassis",
		0x16: "RAID Chassis",
		0x17: "Rack Mount Chassis",
		0x18: "Sealed-case PC",
		0x19: "Multi-system chassis",
		0x1A: "Compact PCI",
		0x1B: "Advanced TCA",
		0x1C: "Blade",
		0x1D: "Blade Enclosure",
		0x1E: "Tablet",
		0x1F: "Convertible",
		0x20: "Detachable",
		0x21: "IoT Gateway",
		0x22: "Embedded PC",
		0x23: "Mini PC",
		0x24: "Stick PC",
	}

	// Chassis type
	chassisTypePath := filepath.Join(sysClassDMIID, "chassis_type")
	chassisType, err := readUint(chassisTypePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed parsing %q: %w", chassisTypePath, err)
	}

	chassis.Type = chassisTypes[chassisType]

	// Chassis serial
	chassisSerialPath := filepath.Join(sysClassDMIID, "chassis_serial")
	content, err = os.ReadFile(chassisSerialPath)
	if err != nil && !os.IsNotExist(err) && !os.IsPermission(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", chassisSerialPath, err)
	}

	chassis.Serial = strings.TrimSpace(string(content))

	// Chassis version
	chassisVersionPath := filepath.Join(sysClassDMIID, "chassis_version")
	content, err = os.ReadFile(chassisVersionPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", chassisVersionPath, err)
	}

	chassis.Version = strings.TrimSpace(string(content))

	return &chassis, nil
}

func systemGetMotherboard() (*api.ResourcesSystemMotherboard, error) {
	motherboard := api.ResourcesSystemMotherboard{}

	// Motherboard vendor name
	boardVendorPath := filepath.Join(sysClassDMIID, "board_vendor")
	content, err := os.ReadFile(boardVendorPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", boardVendorPath, err)
	}

	motherboard.Vendor = strings.TrimSpace(string(content))

	// Motherboard product name
	boardNamePath := filepath.Join(sysClassDMIID, "board_name")
	content, err = os.ReadFile(boardNamePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", boardNamePath, err)
	}

	motherboard.Product = strings.TrimSpace(string(content))

	// Motherboard serial
	boardSerialPath := filepath.Join(sysClassDMIID, "board_serial")
	content, err = os.ReadFile(boardSerialPath)
	if err != nil && !os.IsNotExist(err) && !os.IsPermission(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", boardSerialPath, err)
	}

	motherboard.Serial = strings.TrimSpace(string(content))

	// Motherboard version
	boardVersionPath := filepath.Join(sysClassDMIID, "board_version")
	content, err = os.ReadFile(boardVersionPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Failed reading %q: %w", boardVersionPath, err)
	}

	motherboard.Version = strings.TrimSpace(string(content))

	return &motherboard, nil
}
