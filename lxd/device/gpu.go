package device

import (
	"encoding/csv"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jaypipes/pcidb"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/shared"
)

type gpu struct {
	deviceCommon
}

// /dev/dri/card0. If we detect that vendor == nvidia, then nvidia will contain
// the corresponding nvidia car, e.g. {/dev/dri/card1 to /dev/nvidia1}.
type gpuDevice struct {
	// DRM node information
	id    string
	path  string
	major uint32
	minor uint32

	// Device information
	vendorID    string
	vendorName  string
	productID   string
	productName string
	numaNode    uint64

	// If related devices have the same PCI address as the GPU we should
	// mount them all. Meaning if we detect /dev/dri/card0,
	// /dev/dri/controlD64, and /dev/dri/renderD128 with the same PCI
	// address, then they should all be made available in the container.
	pci           string
	driver        string
	driverVersion string

	// NVIDIA specific handling
	isNvidia bool
	nvidia   nvidiaGpuCard
}

func (g *gpuDevice) isNvidiaGpu() bool {
	return strings.EqualFold(g.vendorID, "10de")
}

// /dev/nvidia[0-9]+
type nvidiaGpuCard struct {
	path  string
	major uint32
	minor uint32
	id    string

	nvrmVersion  string
	cudaVersion  string
	model        string
	brand        string
	uuid         string
	architecture string
}

// {/dev/nvidiactl, /dev/nvidia-uvm, ...}
type nvidiaGpuDevice struct {
	isCard bool
	path   string
	major  uint32
	minor  uint32
}

// Nvidia container info
type nvidiaContainerInfo struct {
	Cards       map[string]*nvidiaContainerCardInfo
	NVRMVersion string
	CUDAVersion string
}

type nvidiaContainerCardInfo struct {
	DeviceIndex  string
	DeviceMinor  string
	Model        string
	Brand        string
	UUID         string
	PCIAddress   string
	Architecture string
}

type cardIds struct {
	id  string
	pci string
}

// validateConfig checks the supplied config for correctness.
func (d *gpu) validateConfig() error {
	if d.instance.Type() != instance.TypeContainer {
		return ErrUnsupportedDevType
	}

	rules := map[string]func(string) error{
		"vendorid":  shared.IsAny,
		"productid": shared.IsAny,
		"id":        shared.IsAny,
		"pci":       shared.IsAny,
		"uid":       shared.IsUnixUserID,
		"gid":       shared.IsUnixUserID,
		"mode":      shared.IsOctalFileMode,
	}

	err := config.ValidateDevice(rules, d.config)
	if err != nil {
		return err
	}

	if d.config["pci"] != "" && (d.config["id"] != "" || d.config["productid"] != "" || d.config["vendorid"] != "") {
		return fmt.Errorf("Cannot use id, productid or vendorid when pci is set")
	}

	if d.config["id"] != "" && (d.config["pci"] != "" || d.config["productid"] != "" || d.config["vendorid"] != "") {
		return fmt.Errorf("Cannot use pci, productid or vendorid when id is set")
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *gpu) validateEnvironment() error {
	if d.config["pci"] != "" && !shared.PathExists(fmt.Sprintf("/sys/bus/pci/devices/%s", d.config["pci"])) {
		return fmt.Errorf("Invalid PCI address (no device found): %s", d.config["pci"])
	}

	return nil
}

// Start is run when the device is added to the container.
func (d *gpu) Start() (*RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	runConf := RunConfig{}

	allGpus := d.deviceWantsAllGPUs(d.config)
	gpus, nvidiaDevices, err := d.deviceLoadGpu(allGpus)
	if err != nil {
		return nil, err
	}

	sawNvidia := false
	found := false
	for _, gpu := range gpus {
		if (d.config["vendorid"] != "" && gpu.vendorID != d.config["vendorid"]) ||
			(d.config["pci"] != "" && gpu.pci != d.config["pci"]) ||
			(d.config["productid"] != "" && gpu.productID != d.config["productid"]) ||
			(d.config["id"] != "" && gpu.id != d.config["id"]) {
			continue
		}

		found = true
		err := unixDeviceSetupCharNum(d.state, d.instance.DevicesPath(), "unix", d.name, d.config, gpu.major, gpu.minor, gpu.path, false, &runConf)
		if err != nil {
			return nil, err
		}

		if !gpu.isNvidia {
			continue
		}

		if gpu.nvidia.path != "" {
			err = unixDeviceSetupCharNum(d.state, d.instance.DevicesPath(), "unix", d.name, d.config, gpu.nvidia.major, gpu.nvidia.minor, gpu.nvidia.path, false, &runConf)
			if err != nil {
				return nil, err
			}
		} else if !allGpus {
			return nil, fmt.Errorf("Failed to detect correct \"/dev/nvidia\" path")
		}

		sawNvidia = true
	}

	if sawNvidia {
		for _, gpu := range nvidiaDevices {
			instanceConfig := d.instance.ExpandedConfig()

			// No need to mount additional nvidia non-card devices as the nvidia.runtime
			// setting will do this for us.
			if shared.IsTrue(instanceConfig["nvidia.runtime"]) {
				if !gpu.isCard {
					continue
				}
			}

			prefix := unixDeviceJoinPath("unix", d.name)
			if UnixDeviceExists(d.instance.DevicesPath(), prefix, gpu.path) {
				continue
			}

			err = unixDeviceSetupCharNum(d.state, d.instance.DevicesPath(), "unix", d.name, d.config, gpu.major, gpu.minor, gpu.path, false, &runConf)
			if err != nil {
				return nil, err
			}
		}
	}

	if !found {
		return nil, fmt.Errorf("Failed to detect requested GPU device")
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the instance.
func (d *gpu) Stop() (*RunConfig, error) {
	runConf := RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	err := unixDeviceRemove(d.instance.DevicesPath(), "unix", d.name, &runConf)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *gpu) postStop() error {
	// Remove host files for this device.
	err := unixDeviceDeleteFiles(d.state, d.instance.DevicesPath(), "unix", d.name)
	if err != nil {
		return fmt.Errorf("Failed to delete files for device '%s': %v", d.name, err)
	}

	return nil
}

// deviceWantsAllGPUs whether the LXD device wants to passthrough all GPUs on the host.
func (d *gpu) deviceWantsAllGPUs(m map[string]string) bool {
	return m["vendorid"] == "" && m["productid"] == "" && m["id"] == "" && m["pci"] == ""
}

// deviceLoadGpu probes the system for information about the available GPUs.
func (d *gpu) deviceLoadGpu(all bool) ([]gpuDevice, []nvidiaGpuDevice, error) {
	const drmPath = "/sys/class/drm/"
	var gpus []gpuDevice
	var nvidiaDevices []nvidiaGpuDevice
	var cards []cardIds

	// Load NVIDIA information (if available)
	var nvidiaContainer *nvidiaContainerInfo

	_, err := exec.LookPath("nvidia-container-cli")
	if err == nil {
		out, err := shared.RunCommand("nvidia-container-cli", "info", "--csv")
		if err == nil {
			r := csv.NewReader(strings.NewReader(out))
			r.FieldsPerRecord = -1

			nvidiaContainer = &nvidiaContainerInfo{}
			nvidiaContainer.Cards = map[string]*nvidiaContainerCardInfo{}
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
					nvidiaContainer.NVRMVersion = record[0]
					nvidiaContainer.CUDAVersion = record[1]
				} else if line >= 4 {
					nvidiaContainer.Cards[record[5]] = &nvidiaContainerCardInfo{
						DeviceIndex:  record[0],
						DeviceMinor:  record[1],
						Model:        record[2],
						Brand:        record[3],
						UUID:         record[4],
						PCIAddress:   record[5],
						Architecture: record[6],
					}
				}
			}
		}
	}

	// Load PCI database
	pciDB, err := pcidb.New()
	if err != nil {
		pciDB = nil
	}

	// Get the list of DRM devices
	ents, err := ioutil.ReadDir(drmPath)
	if err != nil {
		// No GPUs
		if os.IsNotExist(err) {
			return nil, nil, nil
		}

		return nil, nil, err
	}

	// Get the list of cards
	devices := []string{}
	for _, ent := range ents {
		dev, err := filepath.EvalSymlinks(fmt.Sprintf("%s/%s/device", drmPath, ent.Name()))
		if err != nil {
			continue
		}

		if !shared.StringInSlice(dev, devices) {
			devices = append(devices, dev)
		}
	}

	isNvidia := false
	for _, device := range devices {
		// The pci address == the name of the directory. So let's use
		// this cheap way of retrieving it.
		pciAddr := filepath.Base(device)

		// Make sure that we are dealing with a GPU by looking whether
		// the "drm" subfolder exists.
		drm := filepath.Join(device, "drm")
		drmEnts, err := ioutil.ReadDir(drm)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
		}

		// Retrieve vendor ID.
		vendorIDPath := filepath.Join(device, "vendor")
		vendorID, err := ioutil.ReadFile(vendorIDPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
		}

		// Retrieve device ID.
		productIDPath := filepath.Join(device, "device")
		productID, err := ioutil.ReadFile(productIDPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
		}

		// Retrieve node ID
		numaPath := fmt.Sprintf(filepath.Join(device, "numa_node"))
		numaNode := uint64(0)
		if shared.PathExists(numaPath) {
			numaID, err := shared.ParseNumberFromFile(numaPath)
			if err != nil {
				continue
			}

			if numaID > 0 {
				numaNode = uint64(numaID)
			}
		}

		// Retrieve driver
		driver := ""
		driverVersion := ""
		driverPath := filepath.Join(device, "driver")
		if shared.PathExists(driverPath) {
			target, err := os.Readlink(driverPath)
			if err != nil {
				continue
			}

			driver = filepath.Base(target)

			out, err := ioutil.ReadFile(filepath.Join(driverPath, "module", "version"))
			if err == nil {
				driverVersion = strings.TrimSpace(string(out))
			} else {
				uname, err := shared.Uname()
				if err != nil {
					continue
				}
				driverVersion = uname.Release
			}
		}

		// Store all associated subdevices, e.g. controlD64, renderD128.
		// The name of the directory == the last part of the
		// /dev/dri/controlD64 path. So drmEnt.Name() will give us
		// controlD64.
		for _, drmEnt := range drmEnts {
			vendorTmp := strings.TrimSpace(string(vendorID))
			productTmp := strings.TrimSpace(string(productID))
			vendorTmp = strings.TrimPrefix(vendorTmp, "0x")
			productTmp = strings.TrimPrefix(productTmp, "0x")
			tmpGpu := gpuDevice{
				pci:           pciAddr,
				vendorID:      vendorTmp,
				productID:     productTmp,
				numaNode:      numaNode,
				driver:        driver,
				driverVersion: driverVersion,
				path:          filepath.Join("/dev/dri", drmEnt.Name()),
			}

			// Fill vendor and product names
			if pciDB != nil {
				vendor, ok := pciDB.Vendors[tmpGpu.vendorID]
				if ok {
					tmpGpu.vendorName = vendor.Name

					for _, product := range vendor.Products {
						if product.ID == tmpGpu.productID {
							tmpGpu.productName = product.Name
							break
						}
					}
				}
			}

			majMinPath := filepath.Join(drm, drmEnt.Name(), "dev")
			majMinByte, err := ioutil.ReadFile(majMinPath)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
			}

			majMin := strings.TrimSpace(string(majMinByte))
			majMinSlice := strings.Split(string(majMin), ":")
			if len(majMinSlice) != 2 {
				continue
			}

			majorInt, err := strconv.ParseUint(majMinSlice[0], 10, 32)
			if err != nil {
				continue
			}

			minorInt, err := strconv.ParseUint(majMinSlice[1], 10, 32)
			if err != nil {
				continue
			}

			tmpGpu.major = uint32(majorInt)
			tmpGpu.minor = uint32(minorInt)

			isCard, err := regexp.MatchString("^card[0-9]+", drmEnt.Name())
			if err != nil {
				continue
			}

			// Find matching /dev/nvidia* entry for /dev/dri/card*
			if tmpGpu.isNvidiaGpu() && isCard {
				if !isNvidia {
					isNvidia = true
				}
				tmpGpu.isNvidia = true

				if !all {
					minor, err := d.findNvidiaMinor(tmpGpu.pci)
					if err == nil {
						nvidiaPath := "/dev/nvidia" + minor
						stat := unix.Stat_t{}
						err = unix.Stat(nvidiaPath, &stat)
						if err != nil {
							if os.IsNotExist(err) {
								continue
							}

							return nil, nil, err
						}

						tmpGpu.nvidia.path = nvidiaPath
						tmpGpu.nvidia.major = unix.Major(stat.Rdev)
						tmpGpu.nvidia.minor = unix.Minor(stat.Rdev)
						tmpGpu.nvidia.id = strconv.FormatInt(int64(tmpGpu.nvidia.minor), 10)

						if nvidiaContainer != nil {
							tmpGpu.nvidia.nvrmVersion = nvidiaContainer.NVRMVersion
							tmpGpu.nvidia.cudaVersion = nvidiaContainer.CUDAVersion
							nvidiaInfo, ok := nvidiaContainer.Cards[tmpGpu.pci]
							if !ok {
								nvidiaInfo, ok = nvidiaContainer.Cards[fmt.Sprintf("0000%v", tmpGpu.pci)]
							}
							if ok {
								tmpGpu.nvidia.brand = nvidiaInfo.Brand
								tmpGpu.nvidia.model = nvidiaInfo.Model
								tmpGpu.nvidia.uuid = nvidiaInfo.UUID
								tmpGpu.nvidia.architecture = nvidiaInfo.Architecture
							}
						}
					}
				}
			}

			if isCard {
				// If it is a card it's minor number will be its id.
				tmpGpu.id = strconv.FormatInt(int64(minorInt), 10)
				tmp := cardIds{
					id:  tmpGpu.id,
					pci: tmpGpu.pci,
				}

				cards = append(cards, tmp)
			}

			gpus = append(gpus, tmpGpu)
		}
	}

	// We detected a Nvidia card, so let's collect all other nvidia devices
	// that are not /dev/nvidia[0-9]+.
	if isNvidia {
		nvidiaEnts, err := ioutil.ReadDir("/dev")
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil, err
			}
		}

		validNvidia, err := regexp.Compile(`^nvidia[^0-9]+`)
		if err != nil {
			return nil, nil, err
		}

		for _, nvidiaEnt := range nvidiaEnts {
			if all {
				if !strings.HasPrefix(nvidiaEnt.Name(), "nvidia") {
					continue
				}
			} else {
				if !validNvidia.MatchString(nvidiaEnt.Name()) {
					continue
				}
			}

			nvidiaPath := filepath.Join("/dev", nvidiaEnt.Name())
			stat := unix.Stat_t{}
			err = unix.Stat(nvidiaPath, &stat)
			if err != nil {
				continue
			}

			tmpNividiaGpu := nvidiaGpuDevice{
				isCard: !validNvidia.MatchString(nvidiaEnt.Name()),
				path:   nvidiaPath,
				major:  unix.Major(stat.Rdev),
				minor:  unix.Minor(stat.Rdev),
			}

			nvidiaDevices = append(nvidiaDevices, tmpNividiaGpu)
		}
	}

	// Since we'll give users to ability to specify and id we need to group
	// devices on the same PCI that belong to the same card by id.
	for _, card := range cards {
		for i := 0; i < len(gpus); i++ {
			if gpus[i].pci == card.pci {
				gpus[i].id = card.id
			}
		}
	}

	return gpus, nvidiaDevices, nil
}

// findNvidiaMinorOld fallback for old drivers which don't provide "Device Minor:".
func (d *gpu) findNvidiaMinorOld() (string, error) {
	var minor string

	// For now, just handle most common case (single nvidia card)
	ents, err := ioutil.ReadDir("/dev")
	if err != nil {
		return "", err
	}

	rp := regexp.MustCompile("^nvidia([0-9]+)$")
	for _, ent := range ents {
		matches := rp.FindStringSubmatch(ent.Name())
		if matches == nil {
			continue
		}

		if minor != "" {
			return "", fmt.Errorf("No device minor index detected, and more than one NVIDIA card present")
		}
		minor = matches[1]
	}

	if minor == "" {
		return "", fmt.Errorf("No device minor index detected, and no NVIDIA card present")
	}

	return minor, nil
}

// findNvidiaMinor returns minor number of nvidia device corresponding to the given pci id.
func (d *gpu) findNvidiaMinor(pci string) (string, error) {
	nvidiaPath := fmt.Sprintf("/proc/driver/nvidia/gpus/%s/information", pci)
	buf, err := ioutil.ReadFile(nvidiaPath)
	if err != nil {
		return "", err
	}

	strBuf := strings.TrimSpace(string(buf))
	idx := strings.Index(strBuf, "Device Minor:")
	if idx != -1 {
		idx += len("Device Minor:")
		strBuf = strBuf[idx:]
		strBuf = strings.TrimSpace(strBuf)
		parts := strings.SplitN(strBuf, "\n", 2)
		_, err = strconv.Atoi(parts[0])
		if err == nil {
			return parts[0], nil
		}
	}

	minor, err := d.findNvidiaMinorOld()
	if err == nil {
		return minor, nil
	}

	return "", err
}
