package main

import (
	"fmt"
	"os"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd-benchmark/benchmark"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/version"
)

var argCount = gnuflag.Int("count", 100, "Number of containers to create")
var argParallel = gnuflag.Int("parallel", -1, "Number of threads to use")
var argImage = gnuflag.String("image", "ubuntu:", "Image to use for the test")
var argPrivileged = gnuflag.Bool("privileged", false, "Use privileged containers")
var argFreeze = gnuflag.Bool("freeze", false, "Freeze the container right after start")

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
	if len(os.Args) == 1 || !shared.StringInSlice(os.Args[1], []string{"spawn", "delete"}) {
		if len(os.Args) > 1 && os.Args[1] == "--version" {
			fmt.Println(version.Version)
			return nil
		}

		out := os.Stderr
		if len(os.Args) > 1 && os.Args[1] == "--help" {
			out = os.Stdout
		}
		gnuflag.SetOut(out)

		fmt.Fprintf(out, "Usage: %s spawn [--count=COUNT] [--image=IMAGE] [--privileged=BOOL] [--parallel=COUNT]\n", os.Args[0])
		fmt.Fprintf(out, "       %s delete [--parallel=COUNT]\n\n", os.Args[0])
		gnuflag.PrintDefaults()
		fmt.Fprintf(out, "\n")

		if len(os.Args) > 1 && os.Args[1] == "--help" {
			return nil
		}

		return fmt.Errorf("An valid action (spawn or delete) must be passed.")
	}

	gnuflag.Parse(true)

	// Connect to LXD
	c, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	switch os.Args[1] {
	case "spawn":
		return benchmark.SpawnContainers(c, *argCount, *argParallel, *argImage, *argPrivileged, *argFreeze)
	case "delete":
		return benchmark.DeleteContainers(c, *argParallel)
	}

	return nil
}
