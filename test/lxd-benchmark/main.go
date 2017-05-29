package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
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
		return spawnContainers(c, *argCount, *argImage, *argPrivileged)
	case "delete":
		return deleteContainers(c)
	}

	return nil
}

func logf(format string, args ...interface{}) {
	fmt.Printf(fmt.Sprintf("[%s] %s\n", time.Now().Format(time.StampMilli), format), args...)
}

func spawnContainers(c lxd.ContainerServer, count int, image string, privileged bool) error {
	batch := *argParallel
	if batch < 1 {
		// Detect the number of parallel actions
		cpus, err := ioutil.ReadDir("/sys/bus/cpu/devices")
		if err != nil {
			return err
		}

		batch = len(cpus)
	}

	batches := count / batch
	remainder := count % batch

	// Print the test header
	st, _, err := c.GetServer()
	if err != nil {
		return err
	}

	privilegedStr := "unprivileged"
	if privileged {
		privilegedStr = "privileged"
	}

	mode := "normal startup"
	if *argFreeze {
		mode = "start and freeze"
	}

	fmt.Printf("Test environment:\n")
	fmt.Printf("  Server backend: %s\n", st.Environment.Server)
	fmt.Printf("  Server version: %s\n", st.Environment.ServerVersion)
	fmt.Printf("  Kernel: %s\n", st.Environment.Kernel)
	fmt.Printf("  Kernel architecture: %s\n", st.Environment.KernelArchitecture)
	fmt.Printf("  Kernel version: %s\n", st.Environment.KernelVersion)
	fmt.Printf("  Storage backend: %s\n", st.Environment.Storage)
	fmt.Printf("  Storage version: %s\n", st.Environment.StorageVersion)
	fmt.Printf("  Container backend: %s\n", st.Environment.Driver)
	fmt.Printf("  Container version: %s\n", st.Environment.DriverVersion)
	fmt.Printf("\n")
	fmt.Printf("Test variables:\n")
	fmt.Printf("  Container count: %d\n", count)
	fmt.Printf("  Container mode: %s\n", privilegedStr)
	fmt.Printf("  Startup mode: %s\n", mode)
	fmt.Printf("  Image: %s\n", image)
	fmt.Printf("  Batches: %d\n", batches)
	fmt.Printf("  Batch size: %d\n", batch)
	fmt.Printf("  Remainder: %d\n", remainder)
	fmt.Printf("\n")

	// Pre-load the image
	var fingerprint string
	if strings.Contains(image, ":") {
		var remote string

		defaultConfig := config.DefaultConfig
		defaultConfig.UserAgent = version.UserAgent

		remote, fingerprint, err = defaultConfig.ParseRemote(image)
		if err != nil {
			return err
		}

		d, err := defaultConfig.GetImageServer(remote)
		if err != nil {
			return err
		}

		if fingerprint == "" {
			fingerprint = "default"
		}

		alias, _, err := d.GetImageAlias(fingerprint)
		if err == nil {
			fingerprint = alias.Target
		}

		_, _, err = c.GetImage(fingerprint)
		if err != nil {
			logf("Importing image into local store: %s", fingerprint)
			image, _, err := d.GetImage(fingerprint)
			if err != nil {
				logf(fmt.Sprintf("Failed to import image: %s", err))
				return err
			}

			op, err := c.CopyImage(d, *image, nil)
			if err != nil {
				logf(fmt.Sprintf("Failed to import image: %s", err))
				return err
			}

			err = op.Wait()
			if err != nil {
				logf(fmt.Sprintf("Failed to import image: %s", err))
				return err
			}
		} else {
			logf("Found image in local store: %s", fingerprint)
		}
	} else {
		fingerprint = image
		logf("Found image in local store: %s", fingerprint)
	}

	// Start the containers
	spawnedCount := 0
	nameFormat := "benchmark-%." + fmt.Sprintf("%d", len(fmt.Sprintf("%d", count))) + "d"
	wgBatch := sync.WaitGroup{}
	nextStat := batch

	startContainer := func(name string) {
		defer wgBatch.Done()

		// Configure
		config := map[string]string{}
		if privileged {
			config["security.privileged"] = "true"
		}
		config["user.lxd-benchmark"] = "true"

		// Create
		req := api.ContainersPost{
			Name: name,
			Source: api.ContainerSource{
				Type:        "image",
				Fingerprint: fingerprint,
			},
		}
		req.Config = config

		op, err := c.CreateContainer(req)
		if err != nil {
			logf(fmt.Sprintf("Failed to spawn container '%s': %s", name, err))
			return
		}

		err = op.Wait()
		if err != nil {
			logf(fmt.Sprintf("Failed to spawn container '%s': %s", name, err))
			return
		}

		// Start
		op, err = c.UpdateContainerState(name, api.ContainerStatePut{Action: "start", Timeout: -1}, "")
		if err != nil {
			logf(fmt.Sprintf("Failed to spawn container '%s': %s", name, err))
			return
		}

		err = op.Wait()
		if err != nil {
			logf(fmt.Sprintf("Failed to spawn container '%s': %s", name, err))
			return
		}

		// Freeze
		if *argFreeze {
			op, err := c.UpdateContainerState(name, api.ContainerStatePut{Action: "freeze", Timeout: -1}, "")
			if err != nil {
				logf(fmt.Sprintf("Failed to spawn container '%s': %s", name, err))
				return
			}

			err = op.Wait()
			if err != nil {
				logf(fmt.Sprintf("Failed to spawn container '%s': %s", name, err))
				return
			}
		}
	}

	logf("Starting the test")
	timeStart := time.Now()

	for i := 0; i < batches; i++ {
		for j := 0; j < batch; j++ {
			spawnedCount = spawnedCount + 1
			name := fmt.Sprintf(nameFormat, spawnedCount)

			wgBatch.Add(1)
			go startContainer(name)
		}
		wgBatch.Wait()

		if spawnedCount >= nextStat {
			interval := time.Since(timeStart).Seconds()
			logf("Started %d containers in %.3fs (%.3f/s)", spawnedCount, interval, float64(spawnedCount)/interval)
			nextStat = nextStat * 2
		}
	}

	for k := 0; k < remainder; k++ {
		spawnedCount = spawnedCount + 1
		name := fmt.Sprintf(nameFormat, spawnedCount)

		wgBatch.Add(1)
		go startContainer(name)
	}
	wgBatch.Wait()

	logf("Test completed in %.3fs", time.Since(timeStart).Seconds())

	return nil
}

func deleteContainers(c lxd.ContainerServer) error {
	batch := *argParallel
	if batch < 1 {
		// Detect the number of parallel actions
		cpus, err := ioutil.ReadDir("/sys/bus/cpu/devices")
		if err != nil {
			return err
		}

		batch = len(cpus)
	}

	// List all the containers
	allContainers, err := c.GetContainers()
	if err != nil {
		return err
	}

	containers := []api.Container{}
	for _, container := range allContainers {
		if container.Config["user.lxd-benchmark"] != "true" {
			continue
		}

		containers = append(containers, container)
	}

	// Delete them all
	count := len(containers)
	logf("%d containers to delete", count)

	batches := count / batch

	deletedCount := 0
	wgBatch := sync.WaitGroup{}
	nextStat := batch

	deleteContainer := func(ct api.Container) {
		defer wgBatch.Done()

		// Stop
		if ct.IsActive() {
			op, err := c.UpdateContainerState(ct.Name, api.ContainerStatePut{Action: "stop", Timeout: -1, Force: true}, "")
			if err != nil {
				logf(fmt.Sprintf("Failed to delete container '%s': %s", ct.Name, err))
				return
			}

			err = op.Wait()
			if err != nil {
				logf(fmt.Sprintf("Failed to delete container '%s': %s", ct.Name, err))
				return
			}
		}

		// Delete
		op, err := c.DeleteContainer(ct.Name)
		if err != nil {
			logf("Failed to delete container: %s", ct.Name)
			return
		}

		err = op.Wait()
		if err != nil {
			logf("Failed to delete container: %s", ct.Name)
			return
		}
	}

	logf("Starting the cleanup")
	timeStart := time.Now()

	for i := 0; i < batches; i++ {
		for j := 0; j < batch; j++ {
			wgBatch.Add(1)
			go deleteContainer(containers[deletedCount])

			deletedCount = deletedCount + 1
		}
		wgBatch.Wait()

		if deletedCount >= nextStat {
			interval := time.Since(timeStart).Seconds()
			logf("Deleted %d containers in %.3fs (%.3f/s)", deletedCount, interval, float64(deletedCount)/interval)
			nextStat = nextStat * 2
		}
	}

	for k := deletedCount; k < count; k++ {
		wgBatch.Add(1)
		go deleteContainer(containers[deletedCount])

		deletedCount = deletedCount + 1
	}
	wgBatch.Wait()

	logf("Cleanup completed")

	return nil
}
