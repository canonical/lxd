package resources

import (
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/shared/api"
)

var sysClassBlock = "/sys/class/block"

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
