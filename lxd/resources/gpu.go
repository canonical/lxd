package resources

import (
	"encoding/csv"
	"fmt"
	"io"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jaypipes/pcidb"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared/api"
)

var sysClassDrm = "/sys/class/drm"

func loadNvidiaContainer() (map[string]*api.ResourcesGPUCardNvidia, error) {
	// Check for nvidia-container-cli
	_, err := exec.LookPath("nvidia-container-cli")
	if err != nil {
		return nil, errors.Wrap(err, "Failed to locate nvidia-container-cli")
	}

	// Prepare nvidia-container-cli call
	cmd := exec.Command("nvidia-container-cli", "info", "--csv")
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to setup PIPE for nvidia-container-cli")
	}

	// Run the command
	err = cmd.Start()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to start nvidia-container-cli")
	}

	// Parse the data
	r := csv.NewReader(outPipe)
	r.FieldsPerRecord = -1

	nvidiaCards := map[string]*api.ResourcesGPUCardNvidia{}
	nvidiaNVRM := ""
	nvidiaCUDA := ""

	line := 0
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		line++

		if err != nil {
			continue
		}

		if line == 2 && len(record) >= 2 {
			nvidiaNVRM = record[0]
			nvidiaCUDA = record[1]
		} else if line >= 4 {
			nvidiaCards[record[5]] = &api.ResourcesGPUCardNvidia{
				NVRMVersion:  nvidiaNVRM,
				CUDAVersion:  nvidiaCUDA,
				Brand:        record[3],
				Model:        record[2],
				UUID:         record[4],
				Architecture: record[6],
				CardName:     fmt.Sprintf("nvidia%s", record[1]),
				CardDevice:   fmt.Sprintf("195:%s", record[1]),
			}
		}
	}

	// wait for nvidia-container-cli
	err = cmd.Wait()
	if err != nil {
		return nil, errors.Wrap(err, "nvidia-container-cli failed")
	}

	return nvidiaCards, nil
}

func gpuAddDeviceInfo(devicePath string, nvidiaCards map[string]*api.ResourcesGPUCardNvidia, pciDB *pcidb.PCIDB, uname unix.Utsname, card *api.ResourcesGPUCard) error {
	// SRIOV
	if sysfsExists(filepath.Join(devicePath, "sriov_numvfs")) {
		sriov := api.ResourcesGPUCardSRIOV{}

		// Get maximum and current VF count
		vfMaximum, err := readUint(filepath.Join(devicePath, "sriov_totalvfs"))
		if err != nil {
			return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(devicePath, "sriov_totalvfs"))
		}

		vfCurrent, err := readUint(filepath.Join(devicePath, "sriov_numvfs"))
		if err != nil {
			return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(devicePath, "sriov_numvfs"))
		}

		sriov.MaximumVFs = vfMaximum
		sriov.CurrentVFs = vfCurrent

		// Add the SRIOV data to the card
		card.SRIOV = &sriov
	}

	// NUMA node
	if sysfsExists(filepath.Join(devicePath, "numa_node")) {
		numaNode, err := readInt(filepath.Join(devicePath, "numa_node"))
		if err != nil {
			return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(devicePath, "numa_node"))
		}

		if numaNode > 0 {
			card.NUMANode = uint64(numaNode)
		}
	}

	// Vendor and product
	deviceVendorPath := filepath.Join(devicePath, "vendor")
	if sysfsExists(deviceVendorPath) {
		id, err := ioutil.ReadFile(deviceVendorPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to read \"%s\"", deviceVendorPath)
		}

		card.VendorID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
	}

	deviceDevicePath := filepath.Join(devicePath, "device")
	if sysfsExists(deviceDevicePath) {
		id, err := ioutil.ReadFile(deviceDevicePath)
		if err != nil {
			return errors.Wrapf(err, "Failed to read \"%s\"", deviceDevicePath)
		}

		card.ProductID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
	}

	// Fill vendor and product names
	if pciDB != nil {
		vendor, ok := pciDB.Vendors[card.VendorID]
		if ok {
			card.Vendor = vendor.Name

			for _, product := range vendor.Products {
				if product.ID == card.ProductID {
					card.Product = product.Name
					break
				}
			}
		}
	}

	// Driver information
	driverPath := filepath.Join(devicePath, "driver")
	if sysfsExists(driverPath) {
		linkTarget, err := filepath.EvalSymlinks(driverPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to track down \"%s\"", driverPath)
		}

		// Set the driver name
		card.Driver = filepath.Base(linkTarget)

		// Try to get the version, fallback to kernel version
		out, err := ioutil.ReadFile(filepath.Join(driverPath, "module", "version"))
		if err == nil {
			card.DriverVersion = strings.TrimSpace(string(out))
		} else {
			card.DriverVersion = strings.TrimRight(string(uname.Release[:]), "\x00")
		}
	}

	// NVIDIA specific stuff
	if card.Driver == "nvidia" && card.PCIAddress != "" {
		nvidia, ok := nvidiaCards[card.PCIAddress]
		if ok {
			card.Nvidia = nvidia
		} else {
			nvidia, ok := nvidiaCards[fmt.Sprintf("0000%s", card.PCIAddress)]
			if ok {
				card.Nvidia = nvidia
			}
		}
	}

	// DRM information
	drmPath := filepath.Join(devicePath, "drm")
	if sysfsExists(drmPath) {
		drm := api.ResourcesGPUCardDRM{}

		// List all the devices
		entries, err := ioutil.ReadDir(drmPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to list \"%s\"", drmPath)
		}

		// Fill in the struct
		for _, entry := range entries {
			entryName := entry.Name()
			entryPath := filepath.Join(drmPath, entryName)

			if strings.HasPrefix(entryName, "card") {
				// Get the card ID
				idStr := strings.TrimPrefix(entryName, "card")
				id, err := strconv.ParseUint(idStr, 10, 64)
				if err != nil {
					return errors.Wrap(err, "Failed to parse card number")
				}

				dev, err := ioutil.ReadFile(filepath.Join(entryPath, "dev"))
				if err != nil {
					return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "dev"))
				}

				drm.ID = id
				drm.CardName = entryName
				drm.CardDevice = strings.TrimSpace(string(dev))
			}

			if strings.HasPrefix(entryName, "controlD") {
				dev, err := ioutil.ReadFile(filepath.Join(entryPath, "dev"))
				if err != nil {
					return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "dev"))
				}

				drm.ControlName = entryName
				drm.ControlDevice = strings.TrimSpace(string(dev))
			}

			if strings.HasPrefix(entryName, "renderD") {
				dev, err := ioutil.ReadFile(filepath.Join(entryPath, "dev"))
				if err != nil {
					return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "dev"))
				}

				drm.RenderName = entryName
				drm.RenderDevice = strings.TrimSpace(string(dev))
			}
		}

		card.DRM = &drm
	}

	return nil
}

// GetGPU returns a filled api.ResourcesGPU struct ready for use by LXD
func GetGPU() (*api.ResourcesGPU, error) {
	gpu := api.ResourcesGPU{}
	gpu.Cards = []api.ResourcesGPUCard{}

	// Get uname for driver version
	uname := unix.Utsname{}
	err := unix.Uname(&uname)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to get uname")
	}

	// Load PCI database
	pciDB, err := pcidb.New()
	if err != nil {
		pciDB = nil
	}

	// Load NVIDIA information
	nvidiaCards, err := loadNvidiaContainer()
	if err != nil {
		nvidiaCards = map[string]*api.ResourcesGPUCardNvidia{}
	}

	// Temporary variables
	pciKnown := []string{}
	pciVFs := map[string][]api.ResourcesGPUCard{}

	// Detect all GPUs available through kernel drm interface
	if sysfsExists(sysClassDrm) {
		entries, err := ioutil.ReadDir(sysClassDrm)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to list \"%s\"", sysClassDrm)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			entryPath := filepath.Join(sysClassDrm, entryName)
			devicePath := filepath.Join(entryPath, "device")

			// Only care about cards not renderers
			if !strings.HasPrefix(entryName, "card") {
				continue
			}

			// Only keep the main entries not sub-cards
			if !sysfsExists(filepath.Join(entryPath, "dev")) {
				continue
			}

			// Setup the entry
			card := api.ResourcesGPUCard{}

			// PCI address
			linkTarget, err := filepath.EvalSymlinks(devicePath)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to track down \"%s\"", devicePath)
			}

			if strings.HasPrefix(linkTarget, "/sys/devices/pci") && sysfsExists(filepath.Join(devicePath, "subsystem")) {
				virtio := strings.HasPrefix(filepath.Base(linkTarget), "virtio")
				if virtio {
					linkTarget = filepath.Dir(linkTarget)
				}

				subsystem, err := filepath.EvalSymlinks(filepath.Join(devicePath, "subsystem"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to track down \"%s\"", filepath.Join(devicePath, "subsystem"))
				}

				if filepath.Base(subsystem) == "pci" || virtio {
					card.PCIAddress = filepath.Base(linkTarget)

					// Skip devices we already know about
					if stringInSlice(card.PCIAddress, pciKnown) {
						continue
					}

					pciKnown = append(pciKnown, card.PCIAddress)
				}
			}

			// Add device information
			err = gpuAddDeviceInfo(devicePath, nvidiaCards, pciDB, uname, &card)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to add device information for \"%s\"", devicePath)
			}

			// Add to list
			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Virtual functions need to be added to the parent
				linkTarget, err := filepath.EvalSymlinks(filepath.Join(devicePath, "physfn"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to track down \"%s\"", filepath.Join(devicePath, "physfn"))
				}
				parentAddress := filepath.Base(linkTarget)

				_, ok := pciVFs[parentAddress]
				if !ok {
					pciVFs[parentAddress] = []api.ResourcesGPUCard{}
				}
				pciVFs[parentAddress] = append(pciVFs[parentAddress], card)
			} else {
				gpu.Cards = append(gpu.Cards, card)
			}
		}
	}

	// Detect remaining GPUs on PCI bus
	if sysfsExists(sysBusPci) {
		entries, err := ioutil.ReadDir(sysBusPci)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to list \"%s\"", sysBusPci)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			devicePath := filepath.Join(sysBusPci, entryName)

			// Skip devices we already know about
			if stringInSlice(entryName, pciKnown) {
				continue
			}

			// Only care about identifiable devices
			if !sysfsExists(filepath.Join(devicePath, "class")) {
				continue
			}

			class, err := ioutil.ReadFile(filepath.Join(devicePath, "class"))
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(devicePath, "class"))
			}

			// Only care about VGA devices
			if !strings.HasPrefix(string(class), "0x03") {
				continue
			}

			// Start building up data
			card := api.ResourcesGPUCard{}
			card.PCIAddress = entryName

			// Add device information
			err = gpuAddDeviceInfo(devicePath, nvidiaCards, pciDB, uname, &card)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to add device information for \"%s\"", devicePath)
			}

			// Add to list
			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Virtual functions need to be added to the parent
				linkTarget, err := filepath.EvalSymlinks(filepath.Join(devicePath, "physfn"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to track down \"%s\"", filepath.Join(devicePath, "physfn"))
				}
				parentAddress := filepath.Base(linkTarget)

				_, ok := pciVFs[parentAddress]
				if !ok {
					pciVFs[parentAddress] = []api.ResourcesGPUCard{}
				}
				pciVFs[parentAddress] = append(pciVFs[parentAddress], card)
			} else {
				gpu.Cards = append(gpu.Cards, card)
			}
		}
	}

	// Add SRIOV devices and count devices
	gpu.Total = 0
	for _, card := range gpu.Cards {
		if card.SRIOV != nil {
			card.SRIOV.VFs = pciVFs[card.PCIAddress]
			gpu.Total += uint64(len(card.SRIOV.VFs))
		}

		gpu.Total++
	}

	return &gpu, nil
}
