package benchmark

import (
	"fmt"
	"io/ioutil"
	"strings"
	"sync"
	"time"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// PrintServerInfo prints out information about the server.
func PrintServerInfo(c lxd.ContainerServer) error {
	server, _, err := c.GetServer()
	if err != nil {
		return err
	}
	env := server.Environment
	fmt.Printf("Test environment:\n")
	fmt.Printf("  Server backend: %s\n", env.Server)
	fmt.Printf("  Server version: %s\n", env.ServerVersion)
	fmt.Printf("  Kernel: %s\n", env.Kernel)
	fmt.Printf("  Kernel architecture: %s\n", env.KernelArchitecture)
	fmt.Printf("  Kernel version: %s\n", env.KernelVersion)
	fmt.Printf("  Storage backend: %s\n", env.Storage)
	fmt.Printf("  Storage version: %s\n", env.StorageVersion)
	fmt.Printf("  Container backend: %s\n", env.Driver)
	fmt.Printf("  Container version: %s\n", env.DriverVersion)
	fmt.Printf("\n")
	return nil
}

// SpawnContainers launches a set of containers.
func SpawnContainers(c lxd.ContainerServer, count int, parallel int, image string, privileged bool, freeze bool) (time.Duration, error) {
	var duration time.Duration

	batchSize, err := getBatchSize(parallel)
	if err != nil {
		return duration, err
	}

	printTestConfig(count, batchSize, image, privileged, freeze)

	fingerprint, err := ensureImage(c, image)
	if err != nil {
		return duration, err
	}

	startContainer := func(index int, wg *sync.WaitGroup) {
		defer wg.Done()

		// Configure
		config := map[string]string{}
		if privileged {
			config["security.privileged"] = "true"
		}
		config["user.lxd-benchmark"] = "true"

		// Create
		nameFormat := "benchmark-%." + fmt.Sprintf("%d", len(fmt.Sprintf("%d", count))) + "d"
		name := fmt.Sprintf(nameFormat, index+1)
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
			logf("Failed to spawn container '%s': %s", name, err)
			return
		}

		err = op.Wait()
		if err != nil {
			logf("Failed to spawn container '%s': %s", name, err)
			return
		}

		// Start
		op, err = c.UpdateContainerState(name, api.ContainerStatePut{Action: "start", Timeout: -1}, "")
		if err != nil {
			logf("Failed to spawn container '%s': %s", name, err)
			return
		}

		err = op.Wait()
		if err != nil {
			logf("Failed to spawn container '%s': %s", name, err)
			return
		}

		// Freeze
		if freeze {
			op, err := c.UpdateContainerState(name, api.ContainerStatePut{Action: "freeze", Timeout: -1}, "")
			if err != nil {
				logf("Failed to spawn container '%s': %s", name, err)
				return
			}

			err = op.Wait()
			if err != nil {
				logf("Failed to spawn container '%s': %s", name, err)
				return
			}
		}
	}

	duration = processBatch(count, batchSize, startContainer)
	return duration, nil
}

// GetContainers returns containers created by the benchmark.
func GetContainers(c lxd.ContainerServer) ([]api.Container, error) {
	containers := []api.Container{}

	allContainers, err := c.GetContainers()
	if err != nil {
		return containers, err
	}

	for _, container := range allContainers {
		if container.Config["user.lxd-benchmark"] != "true" {
			continue
		}

		containers = append(containers, container)
	}

	return containers, nil
}

// StopContainers stops containers created by the benchmark.
func StopContainers(c lxd.ContainerServer, containers []api.Container, parallel int) (time.Duration, error) {
	var duration time.Duration

	batchSize, err := getBatchSize(parallel)
	if err != nil {
		return duration, err
	}

	count := len(containers)
	logf("Stopping %d containers", count)

	stopContainer := func(index int, wg *sync.WaitGroup) {
		defer wg.Done()

		container := containers[index]
		if container.IsActive() {
			err := stopContainer(c, container.Name)
			if err != nil {
				logf("Failed to stop container '%s': %s", container.Name, err)
				return
			}
		}
	}

	duration = processBatch(count, batchSize, stopContainer)
	return duration, nil
}

// DeleteContainers removes containers created by the benchmark.
func DeleteContainers(c lxd.ContainerServer, containers []api.Container, parallel int) (time.Duration, error) {
	var duration time.Duration

	batchSize, err := getBatchSize(parallel)
	if err != nil {
		return duration, err
	}

	count := len(containers)
	logf("Deleting %d containers", count)

	deleteContainer := func(index int, wg *sync.WaitGroup) {
		defer wg.Done()

		ct := containers[index]

		if ct.IsActive() {
			err := stopContainer(c, ct.Name)
			if err != nil {
				logf("Failed to stop container '%s': %s", ct.Name, err)
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

	duration = processBatch(count, batchSize, deleteContainer)
	return duration, nil
}

func getBatchSize(parallel int) (int, error) {
	batchSize := parallel
	if batchSize < 1 {
		// Detect the number of parallel actions
		cpus, err := ioutil.ReadDir("/sys/bus/cpu/devices")
		if err != nil {
			return -1, err
		}

		batchSize = len(cpus)
	}

	return batchSize, nil
}

func ensureImage(c lxd.ContainerServer, image string) (string, error) {
	var fingerprint string

	if strings.Contains(image, ":") {
		defaultConfig := config.DefaultConfig
		defaultConfig.UserAgent = version.UserAgent

		remote, fp, err := defaultConfig.ParseRemote(image)
		if err != nil {
			return "", err
		}
		fingerprint = fp

		imageServer, err := defaultConfig.GetImageServer(remote)
		if err != nil {
			return "", err
		}

		if fingerprint == "" {
			fingerprint = "default"
		}

		alias, _, err := imageServer.GetImageAlias(fingerprint)
		if err == nil {
			fingerprint = alias.Target
		}

		_, _, err = c.GetImage(fingerprint)
		if err != nil {
			logf("Importing image into local store: %s", fingerprint)
			image, _, err := imageServer.GetImage(fingerprint)
			if err != nil {
				logf("Failed to import image: %s", err)
				return "", err
			}

			op, err := c.CopyImage(imageServer, *image, nil)
			if err != nil {
				logf("Failed to import image: %s", err)
				return "", err
			}

			err = op.Wait()
			if err != nil {
				logf("Failed to import image: %s", err)
				return "", err
			}
		} else {
			logf("Found image in local store: %s", fingerprint)
		}
	} else {
		fingerprint = image
		logf("Found image in local store: %s", fingerprint)
	}
	return fingerprint, nil
}

func processBatch(count int, batchSize int, process func(index int, wg *sync.WaitGroup)) time.Duration {
	batches := count / batchSize
	remainder := count % batchSize
	processed := 0
	wg := sync.WaitGroup{}
	nextStat := batchSize

	logf("Batch processing start")
	timeStart := time.Now()

	for i := 0; i < batches; i++ {
		for j := 0; j < batchSize; j++ {
			wg.Add(1)
			go process(processed, &wg)
			processed++
		}
		wg.Wait()

		if processed >= nextStat {
			interval := time.Since(timeStart).Seconds()
			logf("Processed %d containers in %.3fs (%.3f/s)", processed, interval, float64(processed)/interval)
			nextStat = nextStat * 2
		}

	}

	for k := 0; k < remainder; k++ {
		wg.Add(1)
		go process(processed, &wg)
		processed++
	}
	wg.Wait()

	timeEnd := time.Now()
	duration := timeEnd.Sub(timeStart)
	logf("Batch processing completed in %.3fs", duration.Seconds())
	return duration
}

func logf(format string, args ...interface{}) {
	fmt.Printf(fmt.Sprintf("[%s] %s\n", time.Now().Format(time.StampMilli), format), args...)
}

func printTestConfig(count int, batchSize int, image string, privileged bool, freeze bool) {
	privilegedStr := "unprivileged"
	if privileged {
		privilegedStr = "privileged"
	}
	mode := "normal startup"
	if freeze {
		mode = "start and freeze"
	}

	batches := count / batchSize
	remainder := count % batchSize
	fmt.Printf("Test variables:\n")
	fmt.Printf("  Container count: %d\n", count)
	fmt.Printf("  Container mode: %s\n", privilegedStr)
	fmt.Printf("  Startup mode: %s\n", mode)
	fmt.Printf("  Image: %s\n", image)
	fmt.Printf("  Batches: %d\n", batches)
	fmt.Printf("  Batch size: %d\n", batchSize)
	fmt.Printf("  Remainder: %d\n", remainder)
	fmt.Printf("\n")
}
