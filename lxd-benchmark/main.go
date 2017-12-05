package main

import (
	"fmt"
	"os"
	"time"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd-benchmark/benchmark"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/version"
)

var argCount = gnuflag.Int("count", 1, "Number of containers to create")
var argParallel = gnuflag.Int("parallel", -1, "Number of threads to use")
var argImage = gnuflag.String("image", "ubuntu:", "Image to use for the test")
var argPrivileged = gnuflag.Bool("privileged", false, "Use privileged containers")
var argStart = gnuflag.Bool("start", true, "Start the container after creation")
var argFreeze = gnuflag.Bool("freeze", false, "Freeze the container right after start")
var argReportFile = gnuflag.String("report-file", "", "A CSV file to write test file to. If the file is present, it will be appended to.")
var argReportLabel = gnuflag.String("report-label", "", "A label for the report entry. By default, the action is used.")

func main() {
	err := run(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}

	os.Exit(0)
}

func run(args []string) error {
	// Parse command line
	// "spawn" is being deprecated, use "launch" instead.
	if len(os.Args) == 1 || !shared.StringInSlice(os.Args[1], []string{"launch", "spawn", "start", "stop", "delete"}) {
		if len(os.Args) > 1 && os.Args[1] == "--version" {
			fmt.Println(version.Version)
			return nil
		}

		out := os.Stderr
		if len(os.Args) > 1 && os.Args[1] == "--help" {
			out = os.Stdout
		}
		gnuflag.SetOut(out)

		fmt.Fprintf(out, "Usage: %s launch [--count=COUNT] [--image=IMAGE] [--privileged=BOOL] [--start=BOOL] [--freeze=BOOL] [--parallel=COUNT]\n", os.Args[0])
		fmt.Fprintf(out, "       %s start [--parallel=COUNT]\n", os.Args[0])
		fmt.Fprintf(out, "       %s stop [--parallel=COUNT]\n", os.Args[0])
		fmt.Fprintf(out, "       %s delete [--parallel=COUNT]\n\n", os.Args[0])
		gnuflag.PrintDefaults()
		fmt.Fprintf(out, "\n")

		if len(os.Args) > 1 && os.Args[1] == "--help" {
			return nil
		}

		return fmt.Errorf("A valid action (launch, start, stop, delete) must be passed.")
	}

	gnuflag.Parse(true)

	// Connect to LXD
	c, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	benchmark.PrintServerInfo(c)

	var report *benchmark.CSVReport
	if *argReportFile != "" {
		report = &benchmark.CSVReport{Filename: *argReportFile}
		if shared.PathExists(*argReportFile) {
			err := report.Load()
			if err != nil {
				return err
			}
		}
	}

	action := os.Args[1]
	var duration time.Duration
	switch action {
	// "spawn" is being deprecated.
	case "launch", "spawn":
		duration, err = benchmark.LaunchContainers(
			c, *argCount, *argParallel, *argImage, *argPrivileged, *argStart, *argFreeze)
		if err != nil {
			return err
		}
	case "start":
		containers, err := benchmark.GetContainers(c)
		if err != nil {
			return err
		}
		duration, err = benchmark.StartContainers(c, containers, *argParallel)
		if err != nil {
			return err
		}
	case "stop":
		containers, err := benchmark.GetContainers(c)
		if err != nil {
			return err
		}
		duration, err = benchmark.StopContainers(c, containers, *argParallel)
		if err != nil {
			return err
		}
	case "delete":
		containers, err := benchmark.GetContainers(c)
		if err != nil {
			return err
		}
		duration, err = benchmark.DeleteContainers(c, containers, *argParallel)
		if err != nil {
			return err
		}
	}

	if report != nil {
		label := action
		if *argReportLabel != "" {
			label = *argReportLabel
		}
		report.AddRecord(label, duration)
		err := report.Write()
		if err != nil {
			return err
		}
	}
	return nil
}
