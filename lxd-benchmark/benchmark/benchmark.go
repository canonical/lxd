package benchmark

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

const userConfigKey = "user.lxd-benchmark"

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

// LaunchContainers launches a set of containers.
func LaunchContainers(c lxd.ContainerServer, count int, parallel int, image string, privileged bool, start bool, freeze bool) (time.Duration, error) {
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

		name := getContainerName(count, index)

		err := createContainer(c, fingerprint, name, privileged)
		if err != nil {
			logf("Failed to launch container '%s': %s", name, err)
			return
		}

		if start {
			err := startContainer(c, name)
			if err != nil {
				logf("Failed to start container '%s': %s", name, err)
				return
			}

			if freeze {
				err := freezeContainer(c, name)
				if err != nil {
					logf("Failed to freeze container '%s': %s", name, err)
					return
				}
			}
		}
	}

	duration = processBatch(count, batchSize, startContainer)
	return duration, nil
}

// CreateContainers create the specified number of containers.
func CreateContainers(c lxd.ContainerServer, count int, parallel int, fingerprint string, privileged bool) (time.Duration, error) {
	var duration time.Duration

	batchSize, err := getBatchSize(parallel)
	if err != nil {
		return duration, err
	}

	createContainer := func(index int, wg *sync.WaitGroup) {
		defer wg.Done()

		name := getContainerName(count, index)

		err := createContainer(c, fingerprint, name, privileged)
		if err != nil {
			logf("Failed to launch container '%s': %s", name, err)
			return
		}
	}

	duration = processBatch(count, batchSize, createContainer)

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
		if container.Config[userConfigKey] == "true" {
			containers = append(containers, container)
		}
	}

	return containers, nil
}

// StartContainers starts containers created by the benchmark.
func StartContainers(c lxd.ContainerServer, containers []api.Container, parallel int) (time.Duration, error) {
	var duration time.Duration

	batchSize, err := getBatchSize(parallel)
	if err != nil {
		return duration, err
	}

	count := len(containers)
	logf("Starting %d containers", count)

	startContainer := func(index int, wg *sync.WaitGroup) {
		defer wg.Done()

		container := containers[index]
		if !container.IsActive() {
			err := startContainer(c, container.Name)
			if err != nil {
				logf("Failed to start container '%s': %s", container.Name, err)
				return
			}
		}
	}

	duration = processBatch(count, batchSize, startContainer)
	return duration, nil
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

		container := containers[index]
		name := container.Name
		if container.IsActive() {
			err := stopContainer(c, name)
			if err != nil {
				logf("Failed to stop container '%s': %s", name, err)
				return
			}
		}

		err = deleteContainer(c, name)
		if err != nil {
			logf("Failed to delete container: %s", name)
			return
		}
	}

	duration = processBatch(count, batchSize, deleteContainer)
	return duration, nil
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

			err = copyImage(c, imageServer, *image)
			if err != nil {
				logf("Failed to import image: %s", err)
				return "", err
			}
		}
	} else {
		fingerprint = image
		alias, _, err := c.GetImageAlias(image)
		if err == nil {
			fingerprint = alias.Target
		} else {
			_, _, err = c.GetImage(image)
		}

		if err != nil {
			logf("Image not found in local store: %s", image)
			return "", err
		}
	}

	logf("Found image in local store: %s", fingerprint)
	return fingerprint, nil
}
