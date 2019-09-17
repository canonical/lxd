package resources

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared/api"
)

var devDiskByPath = "/dev/disk/by-path"
var runUdevData = "/run/udev/data"
var sysClassBlock = "/sys/class/block"

func storageAddDriveInfo(devicePath string, disk *api.ResourcesStorageDisk) error {
	// Attempt to open the device path
	f, err := os.Open(devicePath)
	if err == nil {
		defer f.Close()
		fd := int(f.Fd())

		// Retrieve the block size
		res, err := unix.IoctlGetInt(fd, unix.BLKPBSZGET)
		if err != nil {
			return err
		}

		disk.BlockSize = uint64(res)
	}

	// Retrieve udev information
	udevInfo := filepath.Join(runUdevData, fmt.Sprintf("b%s", disk.Device))
	if sysfsExists(udevInfo) {
		// Get the udev information
		f, err := os.Open(udevInfo)
		if err != nil {
			return errors.Wrapf(err, "Failed to open \"%s\"", udevInfo)
		}
		defer f.Close()

		udevProperties := map[string]string{}
		udevInfo := bufio.NewScanner(f)
		for udevInfo.Scan() {
			line := strings.TrimSpace(udevInfo.Text())

			if !strings.HasPrefix(line, "E:") {
				continue
			}

			fields := strings.SplitN(line, "=", 2)
			if len(fields) != 2 {
				continue
			}

			key := strings.TrimSpace(fields[0])
			value := strings.TrimSpace(fields[1])
			udevProperties[key] = value
		}

		// Finer grained disk type
		if udevProperties["E:ID_CDROM"] == "1" {
			disk.Type = "cdrom"
		} else if udevProperties["E:ID_USB_DRIVER"] == "usb-storage" {
			disk.Type = "usb"
		} else if udevProperties["E:ID_ATA_SATA"] == "1" {
			disk.Type = "sata"
		}

		// Firmware version
		if udevProperties["E:ID_REVISION"] != "" && disk.FirmwareVersion == "" {
			disk.FirmwareVersion = udevProperties["E:ID_REVISION"]
		}

		// Serial number
		if udevProperties["E:ID_SERIAL_SHORT"] != "" && disk.Serial == "" {
			disk.Serial = udevProperties["E:ID_SERIAL_SHORT"]
		}

		// Rotation per minute
		if udevProperties["E:ID_ATA_ROTATION_RATE_RPM"] != "" && disk.RPM == 0 {
			valueUint, err := strconv.ParseUint(udevProperties["E:ID_ATA_ROTATION_RATE_RPM"], 10, 64)
			if err != nil {
				return errors.Wrap(err, "Failed to parse RPM value")
			}

			disk.RPM = valueUint
		}
	}

	return nil
}

// GetStorage returns a filled api.ResourcesStorage struct ready for use by LXD
func GetStorage() (*api.ResourcesStorage, error) {
	storage := api.ResourcesStorage{}
	storage.Disks = []api.ResourcesStorageDisk{}

	// Detect all block devices
	if sysfsExists(sysClassBlock) {
		entries, err := ioutil.ReadDir(sysClassBlock)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to list \"%s\"", sysClassBlock)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			entryPath := filepath.Join(sysClassBlock, entryName)
			devicePath := filepath.Join(entryPath, "device")

			// Only keep the main entries not partitions
			if !sysfsExists(devicePath) {
				continue
			}

			// Setup the entry
			disk := api.ResourcesStorageDisk{}
			disk.ID = entryName

			// Firmware revision
			if sysfsExists(filepath.Join(devicePath, "firmware_rev")) {
				firmwareRevision, err := ioutil.ReadFile(filepath.Join(devicePath, "firmware_rev"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(devicePath, "firmware_rev"))
				}

				disk.FirmwareVersion = strings.TrimSpace(string(firmwareRevision))
			}

			// Device node
			diskDev, err := ioutil.ReadFile(filepath.Join(entryPath, "dev"))
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "dev"))
			}
			disk.Device = strings.TrimSpace(string(diskDev))

			// NUMA node
			if sysfsExists(filepath.Join(devicePath, "numa_node")) {
				numaNode, err := readInt(filepath.Join(devicePath, "numa_node"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(devicePath, "numa_node"))
				}

				if numaNode > 0 {
					disk.NUMANode = uint64(numaNode)
				}
			}

			// Disk model
			if sysfsExists(filepath.Join(devicePath, "model")) {
				diskModel, err := ioutil.ReadFile(filepath.Join(devicePath, "model"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(devicePath, "model"))
				}

				disk.Model = strings.TrimSpace(string(diskModel))
			}

			// Disk type
			if sysfsExists(filepath.Join(devicePath, "subsystem")) {
				diskSubsystem, err := filepath.EvalSymlinks(filepath.Join(devicePath, "subsystem"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to track down \"%s\"", filepath.Join(devicePath, "subsystem"))
				}

				disk.Type = filepath.Base(diskSubsystem)
			}

			// Read-only
			diskRo, err := readUint(filepath.Join(entryPath, "ro"))
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "ro"))
			}
			disk.ReadOnly = diskRo == 1

			// Size
			diskSize, err := readUint(filepath.Join(entryPath, "size"))
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "size"))
			}
			disk.Size = diskSize * 512

			// Removable
			diskRemovable, err := readUint(filepath.Join(entryPath, "removable"))
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "removable"))
			}
			disk.Removable = diskRemovable == 1

			// WWN
			if sysfsExists(filepath.Join(entryPath, "wwid")) {
				diskWWN, err := ioutil.ReadFile(filepath.Join(entryPath, "wwid"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(entryPath, "wwid"))
				}
				disk.WWN = strings.TrimSpace(string(diskWWN))
			}

			// Look for partitions
			disk.Partitions = []api.ResourcesStorageDiskPartition{}
			for _, subEntry := range entries {
				subEntryName := subEntry.Name()
				subEntryPath := filepath.Join(sysClassBlock, subEntryName)

				if !strings.HasPrefix(subEntryName, entryName) {
					continue
				}

				if !sysfsExists(filepath.Join(subEntryPath, "partition")) {
					continue
				}

				// Setup the entry
				partition := api.ResourcesStorageDiskPartition{}
				partition.ID = subEntryName

				// Parse the partition number
				partitionNumber, err := readUint(filepath.Join(subEntryPath, "partition"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(subEntryPath, "partition"))
				}
				partition.Partition = partitionNumber

				// Device node
				partitionDev, err := ioutil.ReadFile(filepath.Join(subEntryPath, "dev"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(subEntryPath, "dev"))
				}
				partition.Device = strings.TrimSpace(string(partitionDev))

				// Read-only
				partitionRo, err := readUint(filepath.Join(subEntryPath, "ro"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(subEntryPath, "ro"))
				}
				partition.ReadOnly = partitionRo == 1

				// Size
				partitionSize, err := readUint(filepath.Join(subEntryPath, "size"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(subEntryPath, "size"))
				}
				partition.Size = partitionSize * 512

				// Add to list
				disk.Partitions = append(disk.Partitions, partition)
			}

			// Try to find the udev device path
			if sysfsExists(devDiskByPath) {
				links, err := ioutil.ReadDir(devDiskByPath)
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to list the links in \"%s\"", devDiskByPath)
				}

				for _, link := range links {
					linkName := link.Name()
					linkPath := filepath.Join(devDiskByPath, linkName)

					linkTarget, err := filepath.EvalSymlinks(linkPath)
					if err != nil {
						return nil, errors.Wrapf(err, "Failed to track down \"%s\"", linkPath)
					}

					if linkTarget == filepath.Join("/dev", entryName) {
						disk.DevicePath = linkName
					}
				}
			}

			// Pull direct disk information
			err = storageAddDriveInfo(filepath.Join("/dev", entryName), &disk)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to retrieve disk information from \"%s\"", filepath.Join("/dev", entryName))
			}

			// Add to list
			storage.Disks = append(storage.Disks, disk)
		}
	}

	storage.Total = 0
	for _, card := range storage.Disks {
		if storage.Disks != nil {
			storage.Total += uint64(len(card.Partitions))
		}

		storage.Total++
	}

	return &storage, nil
}
