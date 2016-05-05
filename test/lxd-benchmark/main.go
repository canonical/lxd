package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
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
	gnuflag.Parse(true)

	if len(os.Args) == 1 || !shared.StringInSlice(os.Args[1], []string{"spawn", "delete"}) {
		fmt.Printf("Usage: %s spawn [--count=COUNT] [--image=IMAGE] [--privileged=BOOL] [--parallel=COUNT]\n", os.Args[0])
		fmt.Printf("       %s delete [--parallel=COUNT]\n\n", os.Args[0])
		gnuflag.Usage()
		fmt.Printf("\n")
		return fmt.Errorf("An action (spawn or delete) must be passed.")
	}

	// Connect to LXD
	c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
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

func spawnContainers(c *lxd.Client, count int, image string, privileged bool) error {
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
	st, err := c.ServerStatus()
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
		remote, fingerprint = lxd.DefaultConfig.ParseRemoteAndContainer(image)

		if fingerprint == "" {
			fingerprint = "default"
		}

		d, err := lxd.NewClient(&lxd.DefaultConfig, remote)
		if err != nil {
			return err
		}

		target := d.GetAlias(fingerprint)
		if target != "" {
			fingerprint = target
		}

		_, err = c.GetImageInfo(fingerprint)
		if err != nil {
			logf("Importing image into local store: %s", fingerprint)
			err := d.CopyImage(fingerprint, c, false, nil, false, false, nil)
			if err != nil {
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
		resp, err := c.Init(name, "local", fingerprint, nil, config, nil, false)
		if err != nil {
			logf(fmt.Sprintf("Failed to spawn container '%s': %s", name, err))
			return
		}

		err = c.WaitForSuccess(resp.Operation)
		if err != nil {
			logf(fmt.Sprintf("Failed to spawn container '%s': %s", name, err))
			return
		}

		// Start
		resp, err = c.Action(name, "start", -1, false, false)
		if err != nil {
			logf(fmt.Sprintf("Failed to spawn container '%s': %s", name, err))
			return
		}

		err = c.WaitForSuccess(resp.Operation)
		if err != nil {
			logf(fmt.Sprintf("Failed to spawn container '%s': %s", name, err))
			return
		}

		// Freeze
		if *argFreeze {
			resp, err = c.Action(name, "freeze", -1, false, false)
			if err != nil {
				logf(fmt.Sprintf("Failed to spawn container '%s': %s", name, err))
				return
			}

			err = c.WaitForSuccess(resp.Operation)
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

func deleteContainers(c *lxd.Client) error {
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
	allContainers, err := c.ListContainers()
	if err != nil {
		return err
	}

	containers := []shared.ContainerInfo{}
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

	deleteContainer := func(ct shared.ContainerInfo) {
		defer wgBatch.Done()

		// Stop
		if ct.IsActive() {
			resp, err := c.Action(ct.Name, "stop", -1, true, false)
			if err != nil {
				logf("Failed to delete container: %s", ct.Name)
				return
			}

			err = c.WaitForSuccess(resp.Operation)
			if err != nil {
				logf("Failed to delete container: %s", ct.Name)
				return
			}
		}

		// Delete
		resp, err := c.Delete(ct.Name)
		if err != nil {
			logf("Failed to delete container: %s", ct.Name)
			return
		}

		err = c.WaitForSuccess(resp.Operation)
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
