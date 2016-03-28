package main

import (
	"fmt"
	"io/ioutil"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/codegangsta/cli"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

var commandInfo = cli.Command{
	Name:        "info",
	Usage:       i18n.G("List information on containers."),
	ArgsUsage:   i18n.G("[<remote>:]container [--show-log]"),
	Description: i18n.G("This will support remotes and images as well, but only containers for now."),

	Flags: append(commandGlobalFlags, cli.BoolFlag{
		Name:  "show-log",
		Usage: i18n.G("Also show the log."),
	}),

	Action: commandWrapper(commandActionInfo),
}

func commandActionInfo(config *lxd.Config, context *cli.Context) error {
	var cmd = &infoCmd{
		showLog: context.Bool("show-log"),
	}

	return cmd.run(config, context.Args())
}

type infoCmd struct {
	showLog bool
}

func (c *infoCmd) run(config *lxd.Config, args []string) error {
	var remote string
	var cName string
	if len(args) == 1 {
		remote, cName = config.ParseRemoteAndContainer(args[0])
	} else {
		remote, cName = config.ParseRemoteAndContainer("")
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	if cName == "" {
		return c.remoteInfo(d)
	}

	return c.containerInfo(d, cName, c.showLog)
}

func (c *infoCmd) remoteInfo(d *lxd.Client) error {
	serverStatus, err := d.ServerStatus()
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

func (c *infoCmd) containerInfo(d *lxd.Client, name string, showLog bool) error {
	ct, err := d.ContainerInfo(name)
	if err != nil {
		return err
	}

	cs, err := d.ContainerState(name)
	if err != nil {
		return err
	}

	const layout = "2006/01/02 15:04 UTC"

	fmt.Printf(i18n.G("Name: %s")+"\n", ct.Name)
	fmt.Printf(i18n.G("Architecture: %s")+"\n", ct.Architecture)
	if ct.CreationDate.UTC().Unix() != 0 {
		fmt.Printf(i18n.G("Created: %s")+"\n", ct.CreationDate.UTC().Format(layout))
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
		for netName, net := range cs.Network {
			vethStr := ""
			if net.HostName != "" {
				vethStr = fmt.Sprintf("\t%s", net.HostName)
			}

			for _, addr := range net.Addresses {
				ipInfo += fmt.Sprintf("  %s:\t%s\t%s%s\n", netName, addr.Family, addr.Address, vethStr)
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
		for entry, disk := range cs.Disk {
			if disk.Usage != 0 {
				diskInfo += fmt.Sprintf("    %s: %s\n", entry, shared.GetByteSizeString(disk.Usage))
			}
		}

		if diskInfo != "" {
			fmt.Println(i18n.G("  Disk usage:"))
			fmt.Printf(diskInfo)
		}

		// Memory usage
		memoryInfo := ""
		if cs.Memory.Usage != 0 {
			memoryInfo += fmt.Sprintf("    %s: %s\n", i18n.G("Memory (current)"), shared.GetByteSizeString(cs.Memory.Usage))
		}

		if cs.Memory.UsagePeak != 0 {
			memoryInfo += fmt.Sprintf("    %s: %s\n", i18n.G("Memory (peak)"), shared.GetByteSizeString(cs.Memory.UsagePeak))
		}

		if cs.Memory.SwapUsage != 0 {
			memoryInfo += fmt.Sprintf("    %s: %s\n", i18n.G("Swap (current)"), shared.GetByteSizeString(cs.Memory.SwapUsage))
		}

		if cs.Memory.SwapUsagePeak != 0 {
			memoryInfo += fmt.Sprintf("    %s: %s\n", i18n.G("Swap (peak)"), shared.GetByteSizeString(cs.Memory.SwapUsagePeak))
		}

		if memoryInfo != "" {
			fmt.Println(i18n.G("  Memory usage:"))
			fmt.Printf(memoryInfo)
		}

		// Network usage
		networkInfo := ""
		for netName, net := range cs.Network {
			networkInfo += fmt.Sprintf("    %s:\n", netName)
			networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("Bytes received"), shared.GetByteSizeString(net.Counters.BytesReceived))
			networkInfo += fmt.Sprintf("      %s: %s\n", i18n.G("Bytes sent"), shared.GetByteSizeString(net.Counters.BytesSent))
			networkInfo += fmt.Sprintf("      %s: %d\n", i18n.G("Packets received"), net.Counters.PacketsReceived)
			networkInfo += fmt.Sprintf("      %s: %d\n", i18n.G("Packets sent"), net.Counters.PacketsReceived)
		}

		if networkInfo != "" {
			fmt.Println(i18n.G("  Network usage:"))
			fmt.Printf(networkInfo)
		}
	}

	// List snapshots
	firstSnapshot := true
	snaps, err := d.ListSnapshots(name)
	if err != nil {
		return nil
	}

	for _, snap := range snaps {
		if firstSnapshot {
			fmt.Println(i18n.G("Snapshots:"))
		}
		fmt.Printf("  %s", snap.Name)

		if snap.CreationDate.UTC().Unix() != 0 {
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
		log, err := d.GetLog(name, "lxc.log")
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
