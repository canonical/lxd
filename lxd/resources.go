package main

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
)

var api10ResourcesCmd = Command{
	name: "resources",
	get:  api10ResourcesGet,
}

var storagePoolResourcesCmd = Command{
	name: "storage-pools/{name}/resources",
	get:  storagePoolResourcesGet,
}

// /1.0/resources
// Get system resources
func api10ResourcesGet(d *Daemon, r *http.Request) Response {
	// If a target was specified, forward the request to the relevant node.
	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	// Get the local resource usage
	res := api.Resources{}

	cpu, err := util.CPUResource()
	if err != nil {
		return SmartError(err)
	}

	cards, _, err := deviceLoadGpu(false)
	if err != nil {
		return SmartError(err)
	}

	gpus := api.ResourcesGPU{}
	gpus.Cards = []api.ResourcesGPUCard{}

	processedCards := map[uint64]bool{}
	for _, card := range cards {
		id, err := strconv.ParseUint(card.id, 10, 64)
		if err != nil {
			continue
		}

		if processedCards[id] {
			continue
		}

		gpu := api.ResourcesGPUCard{}
		gpu.ID = id
		gpu.Driver = card.driver
		gpu.DriverVersion = card.driverVersion
		gpu.PCIAddress = card.pci
		gpu.Vendor = card.vendorName
		gpu.VendorID = card.vendorID
		gpu.Product = card.productName
		gpu.ProductID = card.productID
		gpu.NUMANode = card.numaNode

		if card.isNvidia {
			gpu.Nvidia = &api.ResourcesGPUCardNvidia{
				CUDAVersion:  card.nvidia.cudaVersion,
				NVRMVersion:  card.nvidia.nvrmVersion,
				Brand:        card.nvidia.brand,
				Model:        card.nvidia.model,
				UUID:         card.nvidia.uuid,
				Architecture: card.nvidia.architecture,
			}
		}

		gpus.Cards = append(gpus.Cards, gpu)
		gpus.Total += 1
		processedCards[id] = true
	}

	mem, err := util.MemoryResource()
	if err != nil {
		return SmartError(err)
	}

	res.CPU = *cpu
	res.GPU = gpus
	res.Memory = *mem

	return SyncResponse(true, res)
}

// /1.0/storage-pools/{name}/resources
// Get resources for a specific storage pool
func storagePoolResourcesGet(d *Daemon, r *http.Request) Response {
	// If a target was specified, forward the request to the relevant node.
	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	// Get the existing storage pool
	poolName := mux.Vars(r)["name"]
	s, err := storagePoolInit(d.State(), poolName)
	if err != nil {
		return InternalError(err)
	}

	err = s.StoragePoolCheck()
	if err != nil {
		return InternalError(err)
	}

	res, err := s.StoragePoolResources()
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, &res)
}
