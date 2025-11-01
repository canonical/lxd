package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

var devLXDStoragePoolEndpoint = devLXDAPIEndpoint{
	Path: "storage-pools/{pool}",
	Get:  devLXDAPIEndpointAction{Handler: devLXDStoragePoolGetHandler},
}

func devLXDStoragePoolGetHandler(d *Daemon, r *http.Request) *devLXDResponse {
	poolName, err := url.PathUnescape(r.PathValue("pool"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	client, err := getDevLXDVsockClient(d, r)
	if err != nil {
		return smartResponse(err)
	}

	defer client.Disconnect()

	pool, etag, err := client.GetStoragePool(poolName)
	if err != nil {
		return smartResponse(err)
	}

	return okResponseETag(pool, "json", etag)
}

var devLXDStoragePoolVolumesEndpoint = devLXDAPIEndpoint{
	Path: "storage-pools/{pool}/volumes",
	Get:  devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumesGetHandler},
	Post: devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumesPostHandler},
}

var devLXDStoragePoolVolumesTypeEndpoint = devLXDAPIEndpoint{
	Path: "storage-pools/{pool}/volumes/{type}",
	Get:  devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumesGetHandler},
	Post: devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumesPostHandler},
}

func devLXDStoragePoolVolumesGetHandler(d *Daemon, r *http.Request) *devLXDResponse {
	poolName, err := url.PathUnescape(r.PathValue("pool"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	client, err := getDevLXDVsockClient(d, r)
	if err != nil {
		return smartResponse(err)
	}

	client = client.UseTarget(r.URL.Query().Get("target"))
	defer client.Disconnect()

	vols, err := client.GetStoragePoolVolumes(poolName)
	if err != nil {
		return smartResponse(err)
	}

	return okResponse(vols, "json")
}

func devLXDStoragePoolVolumesPostHandler(d *Daemon, r *http.Request) *devLXDResponse {
	poolName, err := url.PathUnescape(r.PathValue("pool"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	volType, err := url.PathUnescape(r.PathValue("type"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	var vol api.DevLXDStorageVolumesPost
	err = json.NewDecoder(r.Body).Decode(&vol)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed to parse request: %w", err))
	}

	if vol.Type == "" {
		vol.Type = volType
	}

	client, err := getDevLXDVsockClient(d, r)
	if err != nil {
		return smartResponse(err)
	}

	client = client.UseTarget(r.URL.Query().Get("target"))
	defer client.Disconnect()

	var op lxd.Operation

	if vol.Source.Type != "" {
		op, err = client.CreateStoragePoolVolumeFromSource(poolName, vol)
	} else {
		err = client.CreateStoragePoolVolume(poolName, vol)
	}

	if err != nil {
		return smartResponse(err)
	}

	if op != nil {
		// Return DevLXD operation response.
		respOp := api.DevLXDOperation{
			ID:         op.Get().ID,
			Status:     op.Get().Status,
			StatusCode: op.Get().StatusCode,
			Err:        op.Get().Err,
		}

		return okResponse(respOp, "json")
	}

	return okResponse("", "raw")
}

var devLXDStoragePoolVolumeTypeEndpoint = devLXDAPIEndpoint{
	Path:   "storage-pools/{pool}/volumes/{type}/{volume}",
	Get:    devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumeGetHandler},
	Patch:  devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumePatchHandler},
	Delete: devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumeDeleteHandler},
}

func devLXDStoragePoolVolumeGetHandler(d *Daemon, r *http.Request) *devLXDResponse {
	poolName, err := url.PathUnescape(r.PathValue("pool"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	volType, err := url.PathUnescape(r.PathValue("type"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	volName, err := url.PathUnescape(r.PathValue("volume"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	client, err := getDevLXDVsockClient(d, r)
	if err != nil {
		return smartResponse(err)
	}

	client = client.UseTarget(r.URL.Query().Get("target"))
	defer client.Disconnect()

	vol, etag, err := client.GetStoragePoolVolume(poolName, volType, volName)
	if err != nil {
		return smartResponse(err)
	}

	return okResponseETag(vol, "json", etag)
}

func devLXDStoragePoolVolumePatchHandler(d *Daemon, r *http.Request) *devLXDResponse {
	poolName, err := url.PathUnescape(r.PathValue("pool"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	volType, err := url.PathUnescape(r.PathValue("type"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	volName, err := url.PathUnescape(r.PathValue("volume"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	etag := r.Header.Get("If-Match")

	var vol api.DevLXDStorageVolumePut
	err = json.NewDecoder(r.Body).Decode(&vol)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed to parse request: %w", err))
	}

	client, err := getDevLXDVsockClient(d, r)
	if err != nil {
		return smartResponse(err)
	}

	client = client.UseTarget(r.URL.Query().Get("target"))
	defer client.Disconnect()

	err = client.UpdateStoragePoolVolume(poolName, volType, volName, vol, etag)
	if err != nil {
		return smartResponse(err)
	}

	return okResponse("", "raw")
}

func devLXDStoragePoolVolumeDeleteHandler(d *Daemon, r *http.Request) *devLXDResponse {
	poolName, err := url.PathUnescape(r.PathValue("pool"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	volType, err := url.PathUnescape(r.PathValue("type"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	volName, err := url.PathUnescape(r.PathValue("volume"))
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error())
	}

	client, err := getDevLXDVsockClient(d, r)
	if err != nil {
		return smartResponse(err)
	}

	client = client.UseTarget(r.URL.Query().Get("target"))
	defer client.Disconnect()

	err = client.DeleteStoragePoolVolume(poolName, volType, volName)
	if err != nil {
		return smartResponse(err)
	}

	return okResponse("", "raw")
}
