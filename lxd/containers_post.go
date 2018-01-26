package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"

	"github.com/dustinkirkland/golang-petname"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"

	log "github.com/lxc/lxd/shared/log15"
)

func createFromImage(d *Daemon, req *api.ContainersPost) Response {
	var hash string
	var err error

	if req.Source.Fingerprint != "" {
		hash = req.Source.Fingerprint
	} else if req.Source.Alias != "" {
		if req.Source.Server != "" {
			hash = req.Source.Alias
		} else {
			_, alias, err := d.cluster.ImageAliasGet(req.Source.Alias, true)
			if err != nil {
				return SmartError(err)
			}

			hash = alias.Target
		}
	} else if req.Source.Properties != nil {
		if req.Source.Server != "" {
			return BadRequest(fmt.Errorf("Property match is only supported for local images"))
		}

		hashes, err := d.cluster.ImagesGet(false)
		if err != nil {
			return SmartError(err)
		}

		var image *api.Image

		for _, imageHash := range hashes {
			_, img, err := d.cluster.ImageGet(imageHash, false, true)
			if err != nil {
				continue
			}

			if image != nil && img.CreatedAt.Before(image.CreatedAt) {
				continue
			}

			match := true
			for key, value := range req.Source.Properties {
				if img.Properties[key] != value {
					match = false
					break
				}
			}

			if !match {
				continue
			}

			image = img
		}

		if image == nil {
			return BadRequest(fmt.Errorf("No matching image could be found"))
		}

		hash = image.Fingerprint
	} else {
		return BadRequest(fmt.Errorf("Must specify one of alias, fingerprint or properties for init from image"))
	}

	run := func(op *operation) error {
		args := db.ContainerArgs{
			Config:    req.Config,
			Ctype:     db.CTypeRegular,
			Devices:   req.Devices,
			Ephemeral: req.Ephemeral,
			Name:      req.Name,
			Profiles:  req.Profiles,
		}

		var info *api.Image
		if req.Source.Server != "" {
			autoUpdate, err := cluster.ConfigGetBool(d.cluster, "images.auto_update_cached")
			if err != nil {
				return err
			}
			info, err = d.ImageDownload(
				op, req.Source.Server, req.Source.Protocol, req.Source.Certificate,
				req.Source.Secret, hash, true, autoUpdate, "", true)
			if err != nil {
				return err
			}
		} else {
			_, info, err = d.cluster.ImageGet(hash, false, false)
			if err != nil {
				return err
			}
		}

		args.Architecture, err = osarch.ArchitectureId(info.Architecture)
		if err != nil {
			return err
		}

		_, err = containerCreateFromImage(d, args, info.Fingerprint)
		return err
	}

	resources := map[string][]string{}
	resources["containers"] = []string{req.Name}

	op, err := operationCreate(d.cluster, operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func createFromNone(d *Daemon, req *api.ContainersPost) Response {
	args := db.ContainerArgs{
		Config:    req.Config,
		Ctype:     db.CTypeRegular,
		Devices:   req.Devices,
		Ephemeral: req.Ephemeral,
		Name:      req.Name,
		Profiles:  req.Profiles,
	}

	if req.Architecture != "" {
		architecture, err := osarch.ArchitectureId(req.Architecture)
		if err != nil {
			return InternalError(err)
		}
		args.Architecture = architecture
	}

	run := func(op *operation) error {
		_, err := containerCreateAsEmpty(d, args)
		return err
	}

	resources := map[string][]string{}
	resources["containers"] = []string{req.Name}

	op, err := operationCreate(d.cluster, operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func createFromMigration(d *Daemon, req *api.ContainersPost) Response {
	// Validate migration mode
	if req.Source.Mode != "pull" && req.Source.Mode != "push" {
		return NotImplemented
	}

	var c container

	// Parse the architecture name
	architecture, err := osarch.ArchitectureId(req.Architecture)
	if err != nil {
		return BadRequest(err)
	}

	// Prepare the container creation request
	args := db.ContainerArgs{
		Architecture: architecture,
		BaseImage:    req.Source.BaseImage,
		Config:       req.Config,
		Ctype:        db.CTypeRegular,
		Devices:      req.Devices,
		Ephemeral:    req.Ephemeral,
		Name:         req.Name,
		Profiles:     req.Profiles,
		Stateful:     req.Stateful,
	}

	// Grab the container's root device if one is specified
	storagePool := ""
	storagePoolProfile := ""

	localRootDiskDeviceKey, localRootDiskDevice, _ := containerGetRootDiskDevice(req.Devices)
	if localRootDiskDeviceKey != "" {
		storagePool = localRootDiskDevice["pool"]
	}

	// Handle copying/moving between two storage-api LXD instances.
	if storagePool != "" {
		_, err := d.cluster.StoragePoolGetID(storagePool)
		if err == db.NoSuchObjectError {
			storagePool = ""
			// Unset the local root disk device storage pool if not
			// found.
			localRootDiskDevice["pool"] = ""
		}
	}

	// If we don't have a valid pool yet, look through profiles
	if storagePool == "" {
		for _, pName := range req.Profiles {
			_, p, err := d.cluster.ProfileGet(pName)
			if err != nil {
				return SmartError(err)
			}

			k, v, _ := containerGetRootDiskDevice(p.Devices)
			if k != "" && v["pool"] != "" {
				// Keep going as we want the last one in the profile chain
				storagePool = v["pool"]
				storagePoolProfile = pName
			}
		}
	}

	logger.Debugf("No valid storage pool in the container's local root disk device and profiles found.")
	// If there is just a single pool in the database, use that
	if storagePool == "" {
		pools, err := d.cluster.StoragePools()
		if err != nil {
			if err == db.NoSuchObjectError {
				return BadRequest(fmt.Errorf("This LXD instance does not have any storage pools configured."))
			}
			return SmartError(err)
		}

		if len(pools) == 1 {
			storagePool = pools[0]
		}
	}

	if storagePool == "" {
		return BadRequest(fmt.Errorf("Can't find a storage pool for the container to use"))
	}

	if localRootDiskDeviceKey == "" && storagePoolProfile == "" {
		// Give the container it's own local root disk device with a
		// pool property.
		rootDev := map[string]string{}
		rootDev["type"] = "disk"
		rootDev["path"] = "/"
		rootDev["pool"] = storagePool
		if args.Devices == nil {
			args.Devices = map[string]map[string]string{}
		}

		// Make sure that we do not overwrite a device the user
		// is currently using under the name "root".
		rootDevName := "root"
		for i := 0; i < 100; i++ {
			if args.Devices[rootDevName] == nil {
				break
			}
			rootDevName = fmt.Sprintf("root%d", i)
			continue
		}

		args.Devices["root"] = rootDev
	} else if localRootDiskDeviceKey != "" && localRootDiskDevice["pool"] == "" {
		args.Devices[localRootDiskDeviceKey]["pool"] = storagePool
	}

	/* Only create a container from an image if we're going to
	 * rsync over the top of it. In the case of a better file
	 * transfer mechanism, let's just use that.
	 *
	 * TODO: we could invent some negotiation here, where if the
	 * source and sink both have the same image, we can clone from
	 * it, but we have to know before sending the snapshot that
	 * we're sending the whole thing or just a delta from the
	 * image, so one extra negotiation round trip is needed. An
	 * alternative is to move actual container object to a later
	 * point and just negotiate it over the migration control
	 * socket. Anyway, it'll happen later :)
	 */
	_, _, err = d.cluster.ImageGet(req.Source.BaseImage, false, true)
	if err != nil {
		c, err = containerCreateAsEmpty(d, args)
		if err != nil {
			return InternalError(err)
		}
	} else {
		// Retrieve the future storage pool
		cM, err := containerLXCLoad(d.State(), args)
		if err != nil {
			return InternalError(err)
		}

		_, rootDiskDevice, err := containerGetRootDiskDevice(cM.ExpandedDevices())
		if err != nil {
			return InternalError(err)
		}

		if rootDiskDevice["pool"] == "" {
			return BadRequest(fmt.Errorf("The container's root device is missing the pool property."))
		}

		storagePool = rootDiskDevice["pool"]

		ps, err := storagePoolInit(d.State(), storagePool)
		if err != nil {
			return InternalError(err)
		}

		if ps.MigrationType() == MigrationFSType_RSYNC {
			c, err = containerCreateFromImage(d, args, req.Source.BaseImage)
			if err != nil {
				return InternalError(err)
			}
		} else {
			c, err = containerCreateAsEmpty(d, args)
			if err != nil {
				return InternalError(err)
			}
		}
	}

	var cert *x509.Certificate
	if req.Source.Certificate != "" {
		certBlock, _ := pem.Decode([]byte(req.Source.Certificate))
		if certBlock == nil {
			c.Delete()
			return InternalError(fmt.Errorf("Invalid certificate"))
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			c.Delete()
			return InternalError(err)
		}
	}

	config, err := shared.GetTLSConfig("", "", "", cert)
	if err != nil {
		c.Delete()
		return InternalError(err)
	}

	push := false
	if req.Source.Mode == "push" {
		push = true
	}

	migrationArgs := MigrationSinkArgs{
		Url: req.Source.Operation,
		Dialer: websocket.Dialer{
			TLSClientConfig: config,
			NetDial:         shared.RFC3493Dialer},
		Container:     c,
		Secrets:       req.Source.Websockets,
		Push:          push,
		Live:          req.Source.Live,
		ContainerOnly: req.Source.ContainerOnly,
	}

	sink, err := NewMigrationSink(&migrationArgs)
	if err != nil {
		c.Delete()
		return InternalError(err)
	}

	run := func(op *operation) error {
		// And finally run the migration.
		err = sink.Do(op)
		if err != nil {
			logger.Error("Error during migration sink", log.Ctx{"err": err})
			c.Delete()
			return fmt.Errorf("Error transferring container data: %s", err)
		}

		err = c.TemplateApply("copy")
		if err != nil {
			c.Delete()
			return err
		}

		if !migrationArgs.Live {
			if req.Config["volatile.last_state.power"] == "RUNNING" {
				return c.Start(false)
			}
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{req.Name}

	var op *operation
	if push {
		op, err = operationCreate(d.cluster, operationClassWebsocket, resources, sink.Metadata(), run, nil, sink.Connect)
		if err != nil {
			return InternalError(err)
		}
	} else {
		op, err = operationCreate(d.cluster, operationClassTask, resources, nil, run, nil, nil)
		if err != nil {
			return InternalError(err)
		}
	}

	return OperationResponse(op)
}

func createFromCopy(d *Daemon, req *api.ContainersPost) Response {
	if req.Source.Source == "" {
		return BadRequest(fmt.Errorf("must specify a source container"))
	}

	source, err := containerLoadByName(d.State(), req.Source.Source)
	if err != nil {
		return SmartError(err)
	}

	// Config override
	sourceConfig := source.LocalConfig()

	if req.Config == nil {
		req.Config = make(map[string]string)
	}

	for key, value := range sourceConfig {
		if len(key) > 8 && key[0:8] == "volatile" && !shared.StringInSlice(key[9:], []string{"base_image", "last_state.idmap"}) {
			logger.Debug("Skipping volatile key from copy source",
				log.Ctx{"key": key})
			continue
		}

		_, exists := req.Config[key]
		if exists {
			continue
		}

		req.Config[key] = value
	}

	// Devices override
	sourceDevices := source.LocalDevices()

	if req.Devices == nil {
		req.Devices = make(map[string]map[string]string)
	}

	for key, value := range sourceDevices {
		_, exists := req.Devices[key]
		if exists {
			continue
		}

		req.Devices[key] = value
	}

	// Profiles override
	if req.Profiles == nil {
		req.Profiles = source.Profiles()
	}

	if req.Stateful {
		sourceName, _, _ := containerGetParentAndSnapshotName(source.Name())
		if sourceName != req.Name {
			return BadRequest(fmt.Errorf(`Copying stateful `+
				`containers requires that source "%s" and `+
				`target "%s" name be identical`, sourceName,
				req.Name))
		}
	}

	args := db.ContainerArgs{
		Architecture: source.Architecture(),
		BaseImage:    req.Source.BaseImage,
		Config:       req.Config,
		Ctype:        db.CTypeRegular,
		Devices:      req.Devices,
		Ephemeral:    req.Ephemeral,
		Name:         req.Name,
		Profiles:     req.Profiles,
		Stateful:     req.Stateful,
	}

	run := func(op *operation) error {
		_, err := containerCreateAsCopy(d.State(), args, source, req.Source.ContainerOnly)
		if err != nil {
			return err
		}
		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{req.Name, req.Source.Source}

	op, err := operationCreate(d.cluster, operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func containersPost(d *Daemon, r *http.Request) Response {
	logger.Debugf("Responding to container create")

	req := api.ContainersPost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	targetNode := r.FormValue("targetNode")
	if targetNode != "" {
		address, err := cluster.ResolveTarget(d.cluster, targetNode)
		if err != nil {
			return SmartError(err)
		}
		if address != "" {
			cert := d.endpoints.NetworkCert()
			client, err := cluster.Connect(address, cert, false)
			if err != nil {
				return SmartError(err)
			}
			logger.Debugf("Forward container post request to %s", address)
			op, err := client.CreateContainer(req)
			if err != nil {
				return SmartError(err)
			}
			return ForwardedOperationResponse(&op.Operation)
		}
	}

	// If no storage pool is found, error out.
	pools, err := d.cluster.StoragePools()
	if err != nil || len(pools) == 0 {
		return BadRequest(fmt.Errorf("No storage pool found. Please create a new storage pool."))
	}

	if req.Name == "" {
		cs, err := d.cluster.ContainersList(db.CTypeRegular)
		if err != nil {
			return SmartError(err)
		}

		i := 0
		for {
			i++
			req.Name = strings.ToLower(petname.Generate(2, "-"))
			if !shared.StringInSlice(req.Name, cs) {
				break
			}

			if i > 100 {
				return InternalError(fmt.Errorf("couldn't generate a new unique name after 100 tries"))
			}
		}
		logger.Debugf("No name provided, creating %s", req.Name)
	}

	if req.Devices == nil {
		req.Devices = types.Devices{}
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	if req.InstanceType != "" {
		conf, err := instanceParseType(req.InstanceType)
		if err != nil {
			return BadRequest(err)
		}

		for k, v := range conf {
			if req.Config[k] == "" {
				req.Config[k] = v
			}
		}
	}

	if strings.Contains(req.Name, shared.SnapshotDelimiter) {
		return BadRequest(fmt.Errorf("Invalid container name: '%s' is reserved for snapshots", shared.SnapshotDelimiter))
	}

	switch req.Source.Type {
	case "image":
		return createFromImage(d, &req)
	case "none":
		return createFromNone(d, &req)
	case "migration":
		return createFromMigration(d, &req)
	case "copy":
		return createFromCopy(d, &req)
	default:
		return BadRequest(fmt.Errorf("unknown source type %s", req.Source.Type))
	}
}
