package main

import (
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdInfo struct {
	global *cmdGlobal

	flagShowLog   bool
	flagResources bool
}

func (c *cmdInfo) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("info [<remote>:][<container>]")
	cmd.Short = i18n.G("Show container or server information")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show container or server information`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc info [<remote>:]<container> [--show-log]
    For container information.

lxc info [<remote>:] [--resources]
    For LXD server information.`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagShowLog, "show-log", false, i18n.G("Show the container's last 100 log lines?"))
	cmd.Flags().BoolVar(&c.flagResources, "resources", false, i18n.G("Show the resources available to the server"))

	return cmd
}

func (c *cmdInfo) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
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

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	if cName == "" {
		return c.remoteInfo(d)
	}

	return c.containerInfo(d, conf.Remotes[remote], cName, c.flagShowLog)
}

func (c *cmdInfo) remoteInfo(d lxd.ContainerServer) error {
	if c.flagResources {
		resources, err := d.GetServerResources()
		if err != nil {
			return err
		}

		resourceData, err := yaml.Marshal(&resources)
		if err != nil {
			return err
		}

		fmt.Printf("%s", resourceData)

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

func (c *cmdInfo) containerInfo(d lxd.ContainerServer, remote config.Remote, name string, showLog bool) error {
	ct, _, err := d.GetContainer(name)
	if err != nil {
		return err
	}

	cs, _, err := d.GetContainerState(name)
	if err != nil {
		return err
	}

	const layout = "2006/01/02 15:04 UTC"

	fmt.Printf(i18n.G("Name: %s")+"\n", ct.Name)
	if ct.Location != "" {
		fmt.Printf(i18n.G("Location: %s")+"\n", ct.Location)
	}
	if remote.Addr != "" {
		fmt.Printf(i18n.G("Remote: %s")+"\n", remote.Addr)
	}

	fmt.Printf(i18n.G("Architecture: %s")+"\n", ct.Architecture)
	if shared.TimeIsSet(ct.CreatedAt) {
		fmt.Printf(i18n.G("Created: %s")+"\n", ct.CreatedAt.UTC().Format(layout))
	}

	fmt.Printf(i18n.G("Status: %s")+"\n", ct.Status)
	if ct.Ephemeral {
		fmt.Printf(i18n.G("Type: ephemeral") + "\n")
	} else {
		fmt.Printf(i18n.G("Type: persistent") + "\n")
	}
	fmt.Printf(i18n.G("Profiles: %s")+"\n", strings.Join(ct.Profiles, ", "))
	if cs.Pid != 0 {
		fmt.Printf(i18n.G("Pid: %d")+"\n", cs.Pid)

		// IP addresses
		ipInfo := ""
		if cs.Network != nil {
			for netName, net := range cs.Network {
				vethStr := ""
				if net.HostName != "" {
					vethStr = fmt.Sprintf("\t%s", net.HostName)
				}

				for _, addr := range net.Addresses {
					ipInfo += fmt.Sprintf("  %s:\t%s\t%s%s\n", netName, addr.Family, addr.Address, vethStr)
				}
			}
		}

		if ipInfo != "" {
			fmt.Println(i18n.G("Ips:"))
			fmt.Printf(ipInfo)
		}
		fmt.Println(i18n.G("Resources:"))

		// Processes
		fmt.Printf("  "+i18n.G("Processes: %d")+"\n", cs.Processes)

		// Disk usage
		diskInfo := ""
		if cs.Disk != nil {
			for entry, disk := range cs.Disk {
				if disk.Usage != 0 {
					diskInfo += fmt.Sprintf("    %s: %s\n", entry, shared.GetByteSizeString(disk.Usage, 2))
				}
			}
		}

		if diskInfo != "" {
			fmt.Println(fmt.Sprintf("  %s", i18n.G("Disk usage:")))
			fmt.Printf(diskInfo)
		}

		// CPU usage
		cpuInfo := ""
		if cs.CPU.Usage != 0 {
			cpuInfo += fmt.Sprintf("    %s: %v\n", i18n.G("CPU usage (in seconds)"), cs.CPU.Usage/1000000000)
		}

		if cpuInfo != "" {
			fmt.Println(fmt.Sprintf("  %s", i18n.G("CPU usage:")))
			fmt.Printf(cpuInfo)
		}

		// Memory usage
		memoryInfo := ""
		if cs.Memory.Usage != 0 {
			memoryInfo += fmt.Sprintf("    %s: %s\n", i18n.G("Memory (current)"), shared.GetByteSizeString(cs.Memory.Usage, 2))
		}

		if cs.Memory.UsagePeak != 0 {
			memoryInfo += fmt.Sprintf("    %s: %s\n", i18n.G("Memory (peak)"), shared.GetByteSizeString(cs.Memory.UsagePeak, 2))
		}

		if cs.Memory.SwapUsage != 0 {
			memoryInfo += fmt.Sprintf("    %s: %s\n", i18n.G("Swap (current)"), shared.GetByteSizeString(cs.Memory.SwapUsage, 2))
		}

		if cs.Memory.SwapUsagePeak != 0 {
			memoryInfo += fmt.Sprintf("    %s: %s\n", i18n.G("Swap (peak)"), shared.GetByteSizeString(cs.Memory.SwapUsagePeak, 2))
		}

		if memoryInfo != "" {
			fmt.Println(fmt.Sprintf("  %s", i18n.G("Memory usage:")))
			fmt.Printf(memoryInfo)
		}

		// Network usage
		networkInfo := ""
		if cs.Network != nil {
			for netName, net := range cs.Network {
				networkInfo += fmt.Sprintf("    %s:\n", netName)
				networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("Bytes received"), shared.GetByteSizeString(net.Counters.BytesReceived, 2))
				networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("Bytes sent"), shared.GetByteSizeString(net.Counters.BytesSent, 2))
				networkInfo += fmt.Sprintf("      %s: %d\n", i18n.G("Packets received"), net.Counters.PacketsReceived)
				networkInfo += fmt.Sprintf("      %s: %d\n", i18n.G("Packets sent"), net.Counters.PacketsSent)
			}
		}

		if networkInfo != "" {
			fmt.Println(fmt.Sprintf("  %s", i18n.G("Network usage:")))
			fmt.Printf(networkInfo)
		}
	}

	// List snapshots
	firstSnapshot := true
	snaps, err := d.GetContainerSnapshots(name)
	if err != nil {
		return nil
	}

	for _, snap := range snaps {
		if firstSnapshot {
			fmt.Println(i18n.G("Snapshots:"))
		}

		fields := strings.Split(snap.Name, shared.SnapshotDelimiter)
		fmt.Printf("  %s", fields[len(fields)-1])

		if shared.TimeIsSet(snap.CreationDate) {
			fmt.Printf(" ("+i18n.G("taken at %s")+")", snap.CreationDate.UTC().Format(layout))
		}

		if snap.Stateful {
			fmt.Printf(" (" + i18n.G("stateful") + ")")
		} else {
			fmt.Printf(" (" + i18n.G("stateless") + ")")
		}
		fmt.Printf("\n")

		firstSnapshot = false
	}

	if showLog {
		log, err := d.GetContainerLogfile(name, "lxc.log")
		if err != nil {
			return err
		}

		stuff, err := ioutil.ReadAll(log)
		if err != nil {
			return err
		}

		fmt.Printf("\n"+i18n.G("Log:")+"\n\n%s\n", string(stuff))
	}

	return nil
}
