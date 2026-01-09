package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/units"
)

type cmdInfo struct {
	global *cmdGlobal

	flagShowLog   bool
	flagResources bool
	flagTarget    string
}

func (c *cmdInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", "[<remote>:][<instance>]")
	cmd.Short = "Show instance or server information"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc info [<remote>:]<instance> [--show-log]
    For instance information.

lxc info [<remote>:] [--resources]
    For LXD server information.`)

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagShowLog, "show-log", false, "Show the instance's last 100 log lines")
	cmd.Flags().BoolVar(&c.flagResources, "resources", false, "Show the resources available to the server")
	cmd.Flags().StringVar(&c.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("instance", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdInfo) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	var remote string
	var cName string
	if len(args) == 1 {
		remote, cName, err = conf.ParseRemote(args[0])
		if err != nil {
			return err
		}
	} else {
		remote, cName, err = conf.ParseRemote("")
		if err != nil {
			return err
		}
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	if cName == "" {
		return c.remoteInfo(d)
	}

	return c.instanceInfo(d, cName, c.flagShowLog)
}

func (c *cmdInfo) renderGPU(gpu api.ResourcesGPUCard, prefix string, initial bool) {
	if initial {
		fmt.Print(prefix)
	}

	fmt.Printf("NUMA node: %v\n", gpu.NUMANode)

	if gpu.Vendor != "" {
		fmt.Printf(prefix+"Vendor: %v (%v)\n", gpu.Vendor, gpu.VendorID)
	}

	if gpu.Product != "" {
		fmt.Printf(prefix+"Product: %v (%v)\n", gpu.Product, gpu.ProductID)
	}

	if gpu.PCIAddress != "" {
		fmt.Printf(prefix+"PCI address: %v\n", gpu.PCIAddress)
	}

	if gpu.Driver != "" {
		fmt.Printf(prefix+"Driver: %v (%v)\n", gpu.Driver, gpu.DriverVersion)
	}

	if gpu.DRM != nil {
		fmt.Print(prefix + "DRM:\n")
		fmt.Printf(prefix+"  ID: %d\n", gpu.DRM.ID)

		if gpu.DRM.CardName != "" {
			fmt.Printf(prefix+"  Card: %s (%s)\n", gpu.DRM.CardName, gpu.DRM.CardDevice)
		}

		if gpu.DRM.ControlName != "" {
			fmt.Printf(prefix+"  Control: %s (%s)\n", gpu.DRM.ControlName, gpu.DRM.ControlDevice)
		}

		if gpu.DRM.RenderName != "" {
			fmt.Printf(prefix+"  Render: %s (%s)\n", gpu.DRM.RenderName, gpu.DRM.RenderDevice)
		}
	}

	if gpu.Nvidia != nil {
		fmt.Print(prefix + "NVIDIA information:\n")
		fmt.Printf(prefix+"  Architecture: %v\n", gpu.Nvidia.Architecture)
		fmt.Printf(prefix+"  Brand: %v\n", gpu.Nvidia.Brand)
		fmt.Printf(prefix+"  Model: %v\n", gpu.Nvidia.Model)
		fmt.Printf(prefix+"  CUDA Version: %v\n", gpu.Nvidia.CUDAVersion)
		fmt.Printf(prefix+"  NVRM Version: %v\n", gpu.Nvidia.NVRMVersion)
		fmt.Printf(prefix+"  UUID: %v\n", gpu.Nvidia.UUID)
	}

	if gpu.SRIOV != nil {
		fmt.Print(prefix + "SR-IOV information:\n")
		fmt.Printf(prefix+"  Current number of VFs: %d\n", gpu.SRIOV.CurrentVFs)
		fmt.Printf(prefix+"  Maximum number of VFs: %d\n", gpu.SRIOV.MaximumVFs)
		if len(gpu.SRIOV.VFs) > 0 {
			fmt.Printf(prefix+"  VFs: %d\n", gpu.SRIOV.MaximumVFs)
			for _, vf := range gpu.SRIOV.VFs {
				fmt.Print(prefix + "  - ")
				c.renderGPU(vf, prefix+"    ", false)
			}
		}
	}

	if gpu.Mdev != nil {
		fmt.Print(prefix + "Mdev profiles:\n")

		keys := make([]string, 0, len(gpu.Mdev))
		for k := range gpu.Mdev {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		for _, k := range keys {
			v := gpu.Mdev[k]

			fmt.Println(prefix + "  - " + fmt.Sprintf("%s (%s) (%d available)", k, v.Name, v.Available))
			if v.Description != "" {
				for line := range strings.SplitSeq(v.Description, "\n") {
					fmt.Printf(prefix+"      %s\n", line)
				}
			}
		}
	}
}

func (c *cmdInfo) renderNIC(nic api.ResourcesNetworkCard, prefix string, initial bool) {
	if initial {
		fmt.Print(prefix)
	}

	fmt.Printf("NUMA node: %v\n", nic.NUMANode)

	if nic.Vendor != "" {
		fmt.Printf(prefix+"Vendor: %v (%v)\n", nic.Vendor, nic.VendorID)
	}

	if nic.Product != "" {
		fmt.Printf(prefix+"Product: %v (%v)\n", nic.Product, nic.ProductID)
	}

	if nic.PCIAddress != "" {
		fmt.Printf(prefix+"PCI address: %v\n", nic.PCIAddress)
	}

	if nic.Driver != "" {
		fmt.Printf(prefix+"Driver: %v (%v)\n", nic.Driver, nic.DriverVersion)
	}

	if len(nic.Ports) > 0 {
		fmt.Print(prefix + "Ports:\n")
		for _, port := range nic.Ports {
			fmt.Printf(prefix+"  - Port %d (%s)\n", port.Port, port.Protocol)
			fmt.Printf(prefix+"    ID: %s\n", port.ID)

			if port.Address != "" {
				fmt.Printf(prefix+"    Address: %s\n", port.Address)
			}

			if port.SupportedModes != nil {
				fmt.Printf(prefix+"    Supported modes: %s\n", strings.Join(port.SupportedModes, ", "))
			}

			if port.SupportedPorts != nil {
				fmt.Printf(prefix+"    Supported ports: %s\n", strings.Join(port.SupportedPorts, ", "))
			}

			if port.PortType != "" {
				fmt.Printf(prefix+"    Port type: %s\n", port.PortType)
			}

			if port.TransceiverType != "" {
				fmt.Printf(prefix+"    Transceiver type: %s\n", port.TransceiverType)
			}

			fmt.Printf(prefix+"    Auto negotiation: %v\n", port.AutoNegotiation)
			fmt.Printf(prefix+"    Link detected: %v\n", port.LinkDetected)
			if port.LinkSpeed > 0 {
				fmt.Printf(prefix+"    Link speed: %dMbit/s (%s duplex)\n", port.LinkSpeed, port.LinkDuplex)
			}

			if port.Infiniband != nil {
				fmt.Print(prefix + "    " + "Infiniband:\n")

				if port.Infiniband.IsSMName != "" {
					fmt.Printf(prefix+"      "+"IsSM: %s (%s)\n", port.Infiniband.IsSMName, port.Infiniband.IsSMDevice)
				}

				if port.Infiniband.MADName != "" {
					fmt.Printf(prefix+"      "+"MAD: %s (%s)\n", port.Infiniband.MADName, port.Infiniband.MADDevice)
				}

				if port.Infiniband.VerbName != "" {
					fmt.Printf(prefix+"      "+"Verb: %s (%s)\n", port.Infiniband.VerbName, port.Infiniband.VerbDevice)
				}
			}
		}
	}

	if nic.SRIOV != nil {
		fmt.Print(prefix + "SR-IOV information:\n")
		fmt.Printf(prefix+"  Current number of VFs: %d\n", nic.SRIOV.CurrentVFs)
		fmt.Printf(prefix+"  Maximum number of VFs: %d\n", nic.SRIOV.MaximumVFs)
		if len(nic.SRIOV.VFs) > 0 {
			fmt.Printf(prefix+"  VFs: %d\n", nic.SRIOV.MaximumVFs)
			for _, vf := range nic.SRIOV.VFs {
				fmt.Print(prefix + "  - ")
				c.renderNIC(vf, prefix+"    ", false)
			}
		}
	}
}

func (c *cmdInfo) renderDisk(disk api.ResourcesStorageDisk, prefix string, initial bool) {
	if initial {
		fmt.Print(prefix)
	}

	fmt.Printf("NUMA node: %v\n", disk.NUMANode)

	fmt.Printf(prefix+"ID: %s\n", disk.ID)
	fmt.Printf(prefix+"Device: %s\n", disk.Device)

	if disk.Model != "" {
		fmt.Printf(prefix+"Model: %s\n", disk.Model)
	}

	if disk.Type != "" {
		fmt.Printf(prefix+"Type: %s\n", disk.Type)
	}

	fmt.Printf(prefix+"Size: %s\n", units.GetByteSizeStringIEC(int64(disk.Size), 2))

	if disk.WWN != "" {
		fmt.Printf(prefix+"WWN: %s\n", disk.WWN)
	}

	fmt.Printf(prefix+"Read-Only: %v\n", disk.ReadOnly)
	fmt.Printf(prefix+"Mounted: %v\n", disk.Mounted)
	fmt.Printf(prefix+"Removable: %v\n", disk.Removable)

	if len(disk.Partitions) != 0 {
		fmt.Print(prefix + "Partitions:\n")
		for _, partition := range disk.Partitions {
			fmt.Printf(prefix+"  - Partition %d\n", partition.Partition)
			fmt.Printf(prefix+"    ID: %s\n", partition.ID)
			fmt.Printf(prefix+"    Device: %s\n", partition.Device)
			fmt.Printf(prefix+"    Read-Only: %v\n", partition.ReadOnly)
			fmt.Printf(prefix+"    Mounted: %v\n", partition.Mounted)
			fmt.Printf(prefix+"    Size: %s\n", units.GetByteSizeStringIEC(int64(partition.Size), 2))
		}
	}
}

func (c *cmdInfo) renderCPU(cpu api.ResourcesCPUSocket, prefix string) {
	if cpu.Vendor != "" {
		fmt.Printf(prefix+"Vendor: %v\n", cpu.Vendor)
	}

	if cpu.Name != "" {
		fmt.Printf(prefix+"Name: %v\n", cpu.Name)
	}

	if cpu.Cache != nil {
		fmt.Print(prefix + "Caches:\n")
		for _, cache := range cpu.Cache {
			fmt.Printf(prefix+"  - Level %d (type: %s): %s\n", cache.Level, cache.Type, units.GetByteSizeStringIEC(int64(cache.Size), 0))
		}
	}

	fmt.Print(prefix + "Cores:\n")
	for _, core := range cpu.Cores {
		fmt.Printf(prefix+"  - Core %d\n", core.Core)
		fmt.Printf(prefix+"    Frequency: %vMhz\n", core.Frequency)
		fmt.Print(prefix + "    Threads:\n")
		for _, thread := range core.Threads {
			fmt.Printf(prefix+"      - %d (id: %d, online: %v, NUMA node: %v)\n", thread.Thread, thread.ID, thread.Online, thread.NUMANode)
		}
	}

	if cpu.Frequency > 0 {
		if cpu.FrequencyTurbo > 0 && cpu.FrequencyMinimum > 0 {
			fmt.Printf(prefix+"Frequency: %vMhz (min: %vMhz, max: %vMhz)\n", cpu.Frequency, cpu.FrequencyMinimum, cpu.FrequencyTurbo)
		} else {
			fmt.Printf(prefix+"Frequency: %vMhz\n", cpu.Frequency)
		}
	}
}

func (c *cmdInfo) remoteInfo(d lxd.InstanceServer) error {
	// Targeting
	if c.flagTarget != "" {
		if !d.IsClustered() {
			return errors.New("To use --target, the destination remote must be a cluster")
		}

		d = d.UseTarget(c.flagTarget)
	}

	if c.flagResources {
		if !d.HasExtension("resources_v2") {
			return errors.New("The server doesn't implement the newer v2 resources API")
		}

		resources, err := d.GetServerResources()
		if err != nil {
			return err
		}

		// CPU
		if len(resources.CPU.Sockets) == 1 {
			fmt.Printf("CPU (%s):\n", resources.CPU.Architecture)
			c.renderCPU(resources.CPU.Sockets[0], "  ")
		} else if len(resources.CPU.Sockets) > 1 {
			fmt.Printf("CPUs (%s):\n", resources.CPU.Architecture)
			for _, cpu := range resources.CPU.Sockets {
				fmt.Printf("  Socket %d:\n", cpu.Socket)
				c.renderCPU(cpu, "    ")
			}
		}

		// Memory
		fmt.Print("\nMemory:\n")
		if resources.Memory.HugepagesTotal > 0 {
			fmt.Print("  Hugepages:\n")
			fmt.Printf("    Free: %v\n", units.GetByteSizeStringIEC(int64(resources.Memory.HugepagesTotal-resources.Memory.HugepagesUsed), 2))
			fmt.Printf("    Used: %v\n", units.GetByteSizeStringIEC(int64(resources.Memory.HugepagesUsed), 2))
			fmt.Printf("    Total: %v\n", units.GetByteSizeStringIEC(int64(resources.Memory.HugepagesTotal), 2))
		}

		if len(resources.Memory.Nodes) > 1 {
			fmt.Print("  NUMA nodes:\n")
			for _, node := range resources.Memory.Nodes {
				fmt.Printf("    Node %d:\n", node.NUMANode)
				if node.HugepagesTotal > 0 {
					fmt.Print("      Hugepages:\n")
					fmt.Printf("        Free: %v\n", units.GetByteSizeStringIEC(int64(node.HugepagesTotal-node.HugepagesUsed), 2))
					fmt.Printf("        Used: %v\n", units.GetByteSizeStringIEC(int64(node.HugepagesUsed), 2))
					fmt.Printf("        Total: %v\n", units.GetByteSizeStringIEC(int64(node.HugepagesTotal), 2))
				}

				fmt.Printf("      Free: %v\n", units.GetByteSizeStringIEC(int64(node.Total-node.Used), 2))
				fmt.Printf("      Used: %v\n", units.GetByteSizeStringIEC(int64(node.Used), 2))
				fmt.Printf("      Total: %v\n", units.GetByteSizeStringIEC(int64(node.Total), 2))
			}
		}

		fmt.Printf("  Free: %v\n", units.GetByteSizeStringIEC(int64(resources.Memory.Total-resources.Memory.Used), 2))
		fmt.Printf("  Used: %v\n", units.GetByteSizeStringIEC(int64(resources.Memory.Used), 2))
		fmt.Printf("  Total: %v\n", units.GetByteSizeStringIEC(int64(resources.Memory.Total), 2))

		// GPUs
		if len(resources.GPU.Cards) == 1 {
			fmt.Print("\nGPU:\n")
			c.renderGPU(resources.GPU.Cards[0], "  ", true)
		} else if len(resources.GPU.Cards) > 1 {
			fmt.Print("\nGPUs:\n")
			for id, gpu := range resources.GPU.Cards {
				fmt.Printf("  Card %d:\n", id)
				c.renderGPU(gpu, "    ", true)
			}
		}

		// Network interfaces
		if len(resources.Network.Cards) == 1 {
			fmt.Print("\nNIC:\n")
			c.renderNIC(resources.Network.Cards[0], "  ", true)
		} else if len(resources.Network.Cards) > 1 {
			fmt.Print("\nNICs:\n")
			for id, nic := range resources.Network.Cards {
				fmt.Printf("  Card %d:\n", id)
				c.renderNIC(nic, "    ", true)
			}
		}

		// Storage
		if len(resources.Storage.Disks) == 1 {
			fmt.Print("\nDisk:\n")
			c.renderDisk(resources.Storage.Disks[0], "  ", true)
		} else if len(resources.Storage.Disks) > 1 {
			fmt.Print("\nDisks:\n")
			for id, nic := range resources.Storage.Disks {
				fmt.Printf("  Disk %d:\n", id)
				c.renderDisk(nic, "    ", true)
			}
		}

		return nil
	}

	serverStatus, _, err := d.GetServer()
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&serverStatus)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

func (c *cmdInfo) instanceInfo(d lxd.InstanceServer, name string, showLog bool) error {
	// Quick checks.
	if c.flagTarget != "" {
		return errors.New("--target cannot be used with instances")
	}

	// Get the full instance data.
	inst, _, err := d.GetInstanceFull(name)
	if err != nil {
		return err
	}

	const layout = "2006/01/02 15:04 MST"

	fmt.Printf("Name: %s\n", inst.Name)

	fmt.Printf("Status: %s\n", strings.ToUpper(inst.Status))

	if inst.Type == "" {
		inst.Type = "container"
	}

	if inst.Ephemeral {
		fmt.Printf("Type: %s (ephemeral)\n", inst.Type)
	} else {
		fmt.Printf("Type: %s\n", inst.Type)
	}

	fmt.Printf("Architecture: %s\n", inst.Architecture)

	if inst.Location != "" && d.IsClustered() {
		fmt.Printf("Location: %s\n", inst.Location)
	}

	if inst.State.Pid != 0 {
		fmt.Printf("PID: %d\n", inst.State.Pid)
	}

	if shared.TimeIsSet(inst.CreatedAt) {
		fmt.Printf("Created: %s\n", inst.CreatedAt.Local().Format(layout))
	}

	if shared.TimeIsSet(inst.LastUsedAt) {
		fmt.Printf("Last Used: %s\n", inst.LastUsedAt.Local().Format(layout))
	}

	if inst.State.Pid != 0 {
		fmt.Println("\nResources:")
		// Processes
		fmt.Printf("  Processes: %d\n", inst.State.Processes)

		// Disk usage
		var diskUsage strings.Builder
		var diskTotal strings.Builder
		if inst.State.Disk != nil {
			for entry, disk := range inst.State.Disk {
				// Only show usage when supported.
				if disk.Usage != -1 {
					diskUsage.WriteString(fmt.Sprintf("    %s: %s\n", entry, units.GetByteSizeStringIEC(disk.Usage, 2)))
				}
			}

			for entry, disk := range inst.State.Disk {
				// Only show total for disks that are bounded within the pool.
				if disk.Total != -1 {
					diskTotal.WriteString(fmt.Sprintf("    %s: %s\n", entry, units.GetByteSizeStringIEC(disk.Usage, 2)))
				}
			}
		}

		if diskUsage.Len() > 0 {
			fmt.Printf("  Disk usage:\n%s", diskUsage.String())
		}

		if diskTotal.Len() > 0 {
			fmt.Printf("  Disk total:\n%s", diskTotal.String())
		}

		// CPU usage
		var cpuInfo strings.Builder
		if inst.State.CPU.Usage != 0 {
			cpuInfo.WriteString(fmt.Sprintf("    CPU usage (in seconds): %v\n", inst.State.CPU.Usage/1000000000))
		}

		if cpuInfo.Len() > 0 {
			fmt.Printf("  CPU usage:\n")
			fmt.Print(cpuInfo.String())
		}

		// Memory usage
		var memoryInfo strings.Builder
		if inst.State.Memory.Usage != 0 {
			memoryInfo.WriteString(fmt.Sprintf("    Memory (current): %s\n", units.GetByteSizeStringIEC(inst.State.Memory.Usage, 2)))
		}

		if inst.State.Memory.UsagePeak != 0 {
			memoryInfo.WriteString(fmt.Sprintf("    Memory (peak): %s\n", units.GetByteSizeStringIEC(inst.State.Memory.UsagePeak, 2)))
		}

		if inst.State.Memory.SwapUsage != 0 {
			memoryInfo.WriteString(fmt.Sprintf("    Swap (current): %s\n", units.GetByteSizeStringIEC(inst.State.Memory.SwapUsage, 2)))
		}

		if inst.State.Memory.SwapUsagePeak != 0 {
			memoryInfo.WriteString(fmt.Sprintf("    Swap (peak): %s\n", units.GetByteSizeStringIEC(inst.State.Memory.SwapUsagePeak, 2)))
		}

		if memoryInfo.Len() > 0 {
			fmt.Printf("  Memory usage:\n")
			fmt.Print(memoryInfo.String())
		}

		// Network usage and IP info
		var networkInfo strings.Builder
		if inst.State.Network != nil {
			for netName, net := range inst.State.Network {
				networkInfo.WriteString(fmt.Sprintf("    %s:\n", netName))
				networkInfo.WriteString(fmt.Sprintf("      Type: %s\n", net.Type))
				networkInfo.WriteString(fmt.Sprintf("      State: %s\n", strings.ToUpper(net.State)))
				if net.HostName != "" {
					networkInfo.WriteString(fmt.Sprintf("      Host interface: %s\n", net.HostName))
				}

				if net.Hwaddr != "" {
					networkInfo.WriteString(fmt.Sprintf("      MAC address: %s\n", net.Hwaddr))
				}

				if net.Mtu != 0 {
					networkInfo.WriteString(fmt.Sprintf("      MTU: %d\n", net.Mtu))
				}

				networkInfo.WriteString(fmt.Sprintf("      Bytes received: %s\n", units.GetByteSizeString(net.Counters.BytesReceived, 2)))
				networkInfo.WriteString(fmt.Sprintf("      Bytes sent: %s\n", units.GetByteSizeString(net.Counters.BytesSent, 2)))
				networkInfo.WriteString(fmt.Sprintf("      Packets received: %d\n", net.Counters.PacketsReceived))
				networkInfo.WriteString(fmt.Sprintf("      Packets sent: %d\n", net.Counters.PacketsSent))

				networkInfo.WriteString(fmt.Sprintf("      IP addresses:\n"))

				for _, addr := range net.Addresses {
					if addr.Family == "inet" {
						networkInfo.WriteString(fmt.Sprintf("        %s:  %s/%s (%s)\n", addr.Family, addr.Address, addr.Netmask, addr.Scope))
					} else {
						networkInfo.WriteString(fmt.Sprintf("        %s: %s/%s (%s)\n", addr.Family, addr.Address, addr.Netmask, addr.Scope))
					}
				}
			}
		}

		if networkInfo.Len() > 0 {
			fmt.Printf("  Network usage:\n")
			fmt.Print(networkInfo.String())
		}
	}

	type volKey struct {
		pool string
		name string
	}

	storageVolumeSnapshotsCache := make(map[volKey][]api.StorageVolumeSnapshot)
	// List snapshots
	firstSnapshot := true
	if len(inst.Snapshots) > 0 {
		snapData := [][]string{}

		for _, snap := range inst.Snapshots {
			if firstSnapshot {
				fmt.Println("\nSnapshots:")
			}

			var row []string

			fields := strings.Split(snap.Name, shared.SnapshotDelimiter)
			row = append(row, fields[len(fields)-1])

			if shared.TimeIsSet(snap.CreatedAt) {
				row = append(row, snap.CreatedAt.Local().Format(layout))
			} else {
				row = append(row, " ")
			}

			if shared.TimeIsSet(snap.ExpiresAt) {
				row = append(row, snap.ExpiresAt.Local().Format(layout))
			} else {
				row = append(row, " ")
			}

			if snap.Stateful {
				row = append(row, "YES")
			} else {
				row = append(row, "NO")
			}

			// Display attached volume snapshots
			if snap.Config["volatile.attached_volumes"] != "" {
				// Parse the JSON map (device name -> snapshot UUID).
				var volatileAttachedVolumes map[string]string
				err := json.Unmarshal([]byte(snap.Config["volatile.attached_volumes"]), &volatileAttachedVolumes)
				if err != nil {
					return fmt.Errorf(`Failed parsing "volatile.attached_volumes" from snapshot %q: %w`, snap.Name, err)
				}

				attachedVolumeSnapshotNames := make([]string, 0, len(volatileAttachedVolumes))
				for deviceName, snapshotUUID := range volatileAttachedVolumes {
					dev, newFormat := snap.Devices[deviceName]
					if !newFormat {
						// Old "volatile.attached_volumes" format (map of volume UUID -> snapshot UUID).
						continue
					}

					// Handle new "volatile.attached_volumes" format (map of device name -> snapshot UUID).

					// Get storage volume snapshots, using cache if possible.
					storageVolumeSnapshots, found := storageVolumeSnapshotsCache[volKey{dev["pool"], dev["source"]}]
					if !found {
						// Storage volume snapshots not in cache yet, fetch them.
						storageVolumeSnapshots, err = d.GetStoragePoolVolumeSnapshots(dev["pool"], "custom", dev["source"])
						if err != nil {
							return err
						}

						// Cache the storage volume snapshots.
						storageVolumeSnapshotsCache[volKey{dev["pool"], dev["source"]}] = storageVolumeSnapshots
					}

					// Find the snapshot with matching UUID
					for _, volSnap := range storageVolumeSnapshots {
						if volSnap.Config["volatile.uuid"] == snapshotUUID {
							attachedVolumeSnapshotNames = append(attachedVolumeSnapshotNames, volSnap.Name)
							break
						}
					}
				}

				if len(attachedVolumeSnapshotNames) != 0 {
					row = append(row, strings.Join(attachedVolumeSnapshotNames, "\n"))
				} else {
					row = append(row, " ")
				}
			}

			firstSnapshot = false
			snapData = append(snapData, row)
		}

		snapHeader := []string{
			"Name",
			"Taken at",
			"Expires at",
			"Stateful",
			"Volume snapshots",
		}

		_ = cli.RenderTable(cli.TableFormatTable, snapHeader, snapData, inst.Snapshots)
	}

	// List backups
	firstBackup := true
	if len(inst.Backups) > 0 {
		backupData := [][]string{}

		for _, backup := range inst.Backups {
			if firstBackup {
				fmt.Println("\nBackups:")
			}

			var row []string
			row = append(row, backup.Name)

			if shared.TimeIsSet(backup.CreatedAt) {
				row = append(row, backup.CreatedAt.Local().Format(layout))
			} else {
				row = append(row, " ")
			}

			if shared.TimeIsSet(backup.ExpiresAt) {
				row = append(row, backup.ExpiresAt.Local().Format(layout))
			} else {
				row = append(row, " ")
			}

			if backup.InstanceOnly {
				row = append(row, "YES")
			} else {
				row = append(row, "NO")
			}

			if backup.OptimizedStorage {
				row = append(row, "YES")
			} else {
				row = append(row, "NO")
			}

			firstBackup = false
			backupData = append(backupData, row)
		}

		backupHeader := []string{
			"Name",
			"Taken at",
			"Expires at",
			"Instance Only",
			"Optimized Storage",
		}

		_ = cli.RenderTable(cli.TableFormatTable, backupHeader, backupData, inst.Backups)
	}

	if showLog {
		var log io.Reader
		switch inst.Type {
		case "container":
			log, err = d.GetInstanceLogfile(name, "lxc.log")
			if err != nil {
				return err
			}

		case "virtual-machine":
			log, err = d.GetInstanceLogfile(name, "qemu.log")
			if err != nil {
				return err
			}

		default:
			return fmt.Errorf("Unsupported instance type: %s", inst.Type)
		}

		stuff, err := io.ReadAll(log)
		if err != nil {
			return err
		}

		fmt.Printf("\nLog:\n\n%s\n", string(stuff))
	}

	return nil
}
