package main

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/dustinkirkland/golang-petname"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
)

func createFromImage(d *Daemon, project string, req *api.InstancesPost) response.Response {
	var hash string
	var err error

	if req.Source.Fingerprint != "" {
		hash = req.Source.Fingerprint
	} else if req.Source.Alias != "" {
		if req.Source.Server != "" {
			hash = req.Source.Alias
		} else {
			_, alias, err := d.cluster.ImageAliasGet(project, req.Source.Alias, true)
			if err != nil {
				return response.SmartError(err)
			}

			hash = alias.Target
		}
	} else if req.Source.Properties != nil {
		if req.Source.Server != "" {
			return response.BadRequest(fmt.Errorf("Property match is only supported for local images"))
		}

		hashes, err := d.cluster.ImagesGet(project, false)
		if err != nil {
			return response.SmartError(err)
		}

		var image *api.Image

		for _, imageHash := range hashes {
			_, img, err := d.cluster.ImageGet(project, imageHash, false, true)
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
			return response.BadRequest(fmt.Errorf("No matching image could be found"))
		}

		hash = image.Fingerprint
	} else {
		return response.BadRequest(fmt.Errorf("Must specify one of alias, fingerprint or properties for init from image"))
	}

	dbType, err := instancetype.New(string(req.Type))
	if err != nil {
		return response.BadRequest(err)
	}

	run := func(op *operations.Operation) error {
		args := db.InstanceArgs{
			Project:     project,
			Config:      req.Config,
			Type:        dbType,
			Description: req.Description,
			Devices:     deviceConfig.NewDevices(req.Devices),
			Ephemeral:   req.Ephemeral,
			Name:        req.Name,
			Profiles:    req.Profiles,
		}

		var info *api.Image
		if req.Source.Server != "" {
			autoUpdate, err := cluster.ConfigGetBool(d.cluster, "images.auto_update_cached")
			if err != nil {
				return err
			}

			// Detect image type based on instance type requested.
			imgType := "container"
			if req.Type == "virtual-machine" {
				imgType = "virtual-machine"
			}

			info, err = d.ImageDownload(
				op, req.Source.Server, req.Source.Protocol, req.Source.Certificate,
				req.Source.Secret, hash, imgType, true, autoUpdate, "", true, project)
			if err != nil {
				return err
			}
		} else {
			_, info, err = d.cluster.ImageGet(project, hash, false, false)
			if err != nil {
				return err
			}
		}

		args.Architecture, err = osarch.ArchitectureId(info.Architecture)
		if err != nil {
			return err
		}

		_, err = instanceCreateFromImage(d, args, info.Fingerprint, op)
		return err
	}

	resources := map[string][]string{}
	resources["instances"] = []string{req.Name}
	resources["containers"] = resources["instances"] // Populate old field name.

	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, db.OperationContainerCreate, resources, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func createFromNone(d *Daemon, project string, req *api.InstancesPost) response.Response {
	dbType, err := instancetype.New(string(req.Type))
	if err != nil {
		return response.BadRequest(err)
	}

	args := db.InstanceArgs{
		Project:     project,
		Config:      req.Config,
		Type:        dbType,
		Description: req.Description,
		Devices:     deviceConfig.NewDevices(req.Devices),
		Ephemeral:   req.Ephemeral,
		Name:        req.Name,
		Profiles:    req.Profiles,
	}

	if req.Architecture != "" {
		architecture, err := osarch.ArchitectureId(req.Architecture)
		if err != nil {
			return response.InternalError(err)
		}
		args.Architecture = architecture
	}

	run := func(op *operations.Operation) error {
		_, err := instanceCreateAsEmpty(d, args)
		return err
	}

	resources := map[string][]string{}
	resources["instances"] = []string{req.Name}
	resources["containers"] = resources["instances"] // Populate old field name.

	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, db.OperationContainerCreate, resources, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func createFromMigration(d *Daemon, project string, req *api.InstancesPost) response.Response {
	// Validate migration mode
	if req.Source.Mode != "pull" && req.Source.Mode != "push" {
		return response.NotImplemented(fmt.Errorf("Mode '%s' not implemented", req.Source.Mode))
	}

	var c Instance

	// Parse the architecture name
	architecture, err := osarch.ArchitectureId(req.Architecture)
	if err != nil {
		return response.BadRequest(err)
	}

	// Pre-fill default profile
	if req.Profiles == nil {
		req.Profiles = []string{"default"}
	}

	dbType, err := instancetype.New(string(req.Type))
	if err != nil {
		return response.BadRequest(err)
	}

	// Prepare the container creation request
	args := db.InstanceArgs{
		Project:      project,
		Architecture: architecture,
		BaseImage:    req.Source.BaseImage,
		Config:       req.Config,
		Type:         dbType,
		Devices:      deviceConfig.NewDevices(req.Devices),
		Description:  req.Description,
		Ephemeral:    req.Ephemeral,
		Name:         req.Name,
		Profiles:     req.Profiles,
		Stateful:     req.Stateful,
	}

	// Early profile validation
	profiles, err := d.cluster.Profiles(project)
	if err != nil {
		return response.InternalError(err)
	}

	for _, profile := range args.Profiles {
		if !shared.StringInSlice(profile, profiles) {
			return response.BadRequest(fmt.Errorf("Requested profile '%s' doesn't exist", profile))
		}
	}

	storagePool, storagePoolProfile, localRootDiskDeviceKey, localRootDiskDevice, resp := containerFindStoragePool(d, project, req)
	if resp != nil {
		return resp
	}

	if storagePool == "" {
		return response.BadRequest(fmt.Errorf("Can't find a storage pool for the container to use"))
	}

	if localRootDiskDeviceKey == "" && storagePoolProfile == "" {
		// Give the container it's own local root disk device with a
		// pool property.
		rootDev := map[string]string{}
		rootDev["type"] = "disk"
		rootDev["path"] = "/"
		rootDev["pool"] = storagePool
		if args.Devices == nil {
			args.Devices = deviceConfig.Devices{}
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

		args.Devices[rootDevName] = rootDev
	} else if localRootDiskDeviceKey != "" && localRootDiskDevice["pool"] == "" {
		args.Devices[localRootDiskDeviceKey]["pool"] = storagePool
	}

	// Early check for refresh
	if req.Source.Refresh {
		// Check if the container exists
		inst, err := instanceLoadByProjectAndName(d.State(), project, req.Name)
		if err != nil {
			req.Source.Refresh = false
		} else if inst.IsRunning() {
			return response.BadRequest(fmt.Errorf("Cannot refresh a running container"))
		}

		if inst.Type() != instancetype.Container {
			return response.BadRequest(fmt.Errorf("Instance type not container"))
		}

		c = inst
	}

	if !req.Source.Refresh {
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
		_, _, err = d.cluster.ImageGet(args.Project, req.Source.BaseImage, false, true)
		if err != nil {
			c, err = instanceCreateAsEmpty(d, args)
			if err != nil {
				return response.InternalError(err)
			}
		} else {
			// Retrieve the future storage pool
			inst, err := instanceLoad(d.State(), args, nil)
			if err != nil {
				return response.InternalError(err)
			}

			_, rootDiskDevice, err := shared.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
			if err != nil {
				return response.InternalError(err)
			}

			if rootDiskDevice["pool"] == "" {
				return response.BadRequest(fmt.Errorf("The container's root device is missing the pool property"))
			}

			storagePool = rootDiskDevice["pool"]

			ps, err := storagePoolInit(d.State(), storagePool)
			if err != nil {
				return response.InternalError(err)
			}

			if ps.MigrationType() == migration.MigrationFSType_RSYNC {
				c, err = instanceCreateFromImage(d, args, req.Source.BaseImage, nil)
				if err != nil {
					return response.InternalError(err)
				}
			} else {
				c, err = instanceCreateAsEmpty(d, args)
				if err != nil {
					return response.InternalError(err)
				}
			}
		}
	}

	var cert *x509.Certificate
	if req.Source.Certificate != "" {
		certBlock, _ := pem.Decode([]byte(req.Source.Certificate))
		if certBlock == nil {
			if !req.Source.Refresh {
				c.Delete()
			}
			return response.InternalError(fmt.Errorf("Invalid certificate"))
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			if !req.Source.Refresh {
				c.Delete()
			}
			return response.InternalError(err)
		}
	}

	config, err := shared.GetTLSConfig("", "", "", cert)
	if err != nil {
		if !req.Source.Refresh {
			c.Delete()
		}
		return response.InternalError(err)
	}

	push := false
	if req.Source.Mode == "push" {
		push = true
	}

	instanceOnly := req.Source.InstanceOnly || req.Source.ContainerOnly
	migrationArgs := MigrationSinkArgs{
		Url: req.Source.Operation,
		Dialer: websocket.Dialer{
			TLSClientConfig: config,
			NetDial:         shared.RFC3493Dialer},
		Instance:     c,
		Secrets:      req.Source.Websockets,
		Push:         push,
		Live:         req.Source.Live,
		InstanceOnly: instanceOnly,
		Refresh:      req.Source.Refresh,
	}

	sink, err := NewMigrationSink(&migrationArgs)
	if err != nil {
		c.Delete()
		return response.InternalError(err)
	}

	run := func(op *operations.Operation) error {
		// And finally run the migration.
		err = sink.Do(op)
		if err != nil {
			logger.Error("Error during migration sink", log.Ctx{"err": err})
			if !req.Source.Refresh {
				c.Delete()
			}
			return fmt.Errorf("Error transferring container data: %s", err)
		}

		err = c.DeferTemplateApply("copy")
		if err != nil {
			if !req.Source.Refresh {
				c.Delete()
			}
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{req.Name}

	var op *operations.Operation
	if push {
		op, err = operations.OperationCreate(d.State(), project, operations.OperationClassWebsocket, db.OperationContainerCreate, resources, sink.Metadata(), run, nil, sink.Connect)
		if err != nil {
			return response.InternalError(err)
		}
	} else {
		op, err = operations.OperationCreate(d.State(), project, operations.OperationClassTask, db.OperationContainerCreate, resources, nil, run, nil, nil)
		if err != nil {
			return response.InternalError(err)
		}
	}

	return operations.OperationResponse(op)
}

func createFromCopy(d *Daemon, project string, req *api.InstancesPost) response.Response {
	if req.Source.Source == "" {
		return response.BadRequest(fmt.Errorf("must specify a source container"))
	}

	sourceProject := req.Source.Project
	if sourceProject == "" {
		sourceProject = project
	}
	targetProject := project

	source, err := instanceLoadByProjectAndName(d.State(), sourceProject, req.Source.Source)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if we need to redirect to migration
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	// When clustered, use the node name, otherwise use the hostname.
	if clustered {
		var serverName string
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			serverName, err = tx.NodeName()
			return err
		})
		if err != nil {
			return response.SmartError(err)
		}

		if serverName != source.Location() {
			// Check if we are copying from a ceph-based container.
			_, rootDevice, _ := shared.GetRootDiskDevice(source.ExpandedDevices().CloneNative())
			sourcePoolName := rootDevice["pool"]

			destPoolName, _, _, _, resp := containerFindStoragePool(d, targetProject, req)
			if resp != nil {
				return resp
			}

			if sourcePoolName != destPoolName {
				// Redirect to migration
				return clusterCopyContainerInternal(d, source, project, req)
			}

			_, pool, err := d.cluster.StoragePoolGet(sourcePoolName)
			if err != nil {
				err = errors.Wrap(err, "Failed to fetch container's pool info")
				return response.SmartError(err)
			}

			if pool.Driver != "ceph" {
				// Redirect to migration
				return clusterCopyContainerInternal(d, source, project, req)
			}
		}
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
		sourceName, _, _ := shared.ContainerGetParentAndSnapshotName(source.Name())
		if sourceName != req.Name {
			return response.BadRequest(fmt.Errorf(`Copying stateful `+
				`containers requires that source "%s" and `+
				`target "%s" name be identical`, sourceName,
				req.Name))
		}
	}

	// Early check for refresh
	if req.Source.Refresh {
		// Check if the container exists
		c, err := instanceLoadByProjectAndName(d.State(), targetProject, req.Name)
		if err != nil {
			req.Source.Refresh = false
		} else if c.IsRunning() {
			return response.BadRequest(fmt.Errorf("Cannot refresh a running container"))
		}
	}

	dbType, err := instancetype.New(string(req.Type))
	if err != nil {
		return response.BadRequest(err)
	}

	if dbType != instancetype.Any && dbType != source.Type() {
		return response.BadRequest(fmt.Errorf("Instance type should not be specified or should match source type"))
	}

	args := db.InstanceArgs{
		Project:      targetProject,
		Architecture: source.Architecture(),
		BaseImage:    req.Source.BaseImage,
		Config:       req.Config,
		Type:         source.Type(),
		Description:  req.Description,
		Devices:      deviceConfig.NewDevices(req.Devices),
		Ephemeral:    req.Ephemeral,
		Name:         req.Name,
		Profiles:     req.Profiles,
		Stateful:     req.Stateful,
	}

	run := func(op *operations.Operation) error {
		instanceOnly := req.Source.InstanceOnly || req.Source.ContainerOnly
		_, err := containerCreateAsCopy(d.State(), args, source, instanceOnly, req.Source.Refresh)
		if err != nil {
			return err
		}
		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{req.Name, req.Source.Source}

	op, err := operations.OperationCreate(d.State(), targetProject, operations.OperationClassTask, db.OperationContainerCreate, resources, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func createFromBackup(d *Daemon, project string, data io.Reader, pool string) response.Response {
	// Write the data to a temp file
	f, err := ioutil.TempFile("", "lxd_backup_")
	if err != nil {
		return response.InternalError(err)
	}
	defer os.Remove(f.Name())

	_, err = io.Copy(f, data)
	if err != nil {
		f.Close()
		return response.InternalError(err)
	}

	// Parse the backup information
	f.Seek(0, 0)
	bInfo, err := backup.GetInfo(f)
	if err != nil {
		f.Close()
		return response.BadRequest(err)
	}
	bInfo.Project = project

	// Override pool
	if pool != "" {
		bInfo.Pool = pool
	}

	run := func(op *operations.Operation) error {
		defer f.Close()

		// Dump tarball to storage
		f.Seek(0, 0)
		cPool, err := containerCreateFromBackup(d.State(), *bInfo, f, pool != "")
		if err != nil {
			return errors.Wrap(err, "Create container from backup")
		}

		body, err := json.Marshal(&internalImportPost{
			Name:  bInfo.Name,
			Force: true,
		})
		if err != nil {
			cPool.ContainerDelete(&containerLXC{name: bInfo.Name, project: project})
			return errors.Wrap(err, "Marshal internal import request")
		}

		req := &http.Request{
			Body: ioutil.NopCloser(bytes.NewReader(body)),
		}
		req.URL = &url.URL{
			RawQuery: fmt.Sprintf("project=%s", project),
		}
		resp := internalImport(d, req)

		if resp.String() != "success" {
			cPool.ContainerDelete(&containerLXC{name: bInfo.Name, project: project})
			return fmt.Errorf("Internal import request: %v", resp.String())
		}

		c, err := instanceLoadByProjectAndName(d.State(), project, bInfo.Name)
		if err != nil {
			return errors.Wrap(err, "Load container")
		}

		_, err = c.StorageStop()
		if err != nil {
			return errors.Wrap(err, "Stop storage pool")
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{bInfo.Name}

	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, db.OperationBackupRestore,
		resources, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func containersPost(d *Daemon, r *http.Request) response.Response {
	project := projectParam(r)
	logger.Debugf("Responding to container create")

	// If we're getting binary content, process separately
	if r.Header.Get("Content-Type") == "application/octet-stream" {
		return createFromBackup(d, project, r.Body, r.Header.Get("X-LXD-pool"))
	}

	// Parse the request
	req := api.InstancesPost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	targetNode := queryParam(r, "target")
	if targetNode == "" {
		// If no target node was specified, pick the node with the
		// least number of containers. If there's just one node, or if
		// the selected node is the local one, this is effectively a
		// no-op, since NodeWithLeastContainers() will return an empty
		// string.
		err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
			var err error
			targetNode, err = tx.NodeWithLeastContainers()
			return err
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	if targetNode != "" {
		address, err := cluster.ResolveTarget(d.cluster, targetNode)
		if err != nil {
			return response.SmartError(err)
		}
		if address != "" {
			cert := d.endpoints.NetworkCert()
			client, err := cluster.Connect(address, cert, false)
			if err != nil {
				return response.SmartError(err)
			}

			client = client.UseProject(project)
			client = client.UseTarget(targetNode)

			logger.Debugf("Forward instance post request to %s", address)
			op, err := client.CreateInstance(req)
			if err != nil {
				return response.SmartError(err)
			}

			opAPI := op.Get()
			return operations.ForwardedOperationResponse(project, &opAPI)
		}
	}

	// If no storage pool is found, error out.
	pools, err := d.cluster.StoragePools()
	if err != nil || len(pools) == 0 {
		return response.BadRequest(fmt.Errorf("No storage pool found. Please create a new storage pool"))
	}

	if req.Name == "" {
		var names []string
		err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
			var err error
			names, err = tx.ContainerNames(project)
			return err
		})
		if err != nil {
			return response.SmartError(err)
		}

		i := 0
		for {
			i++
			req.Name = strings.ToLower(petname.Generate(2, "-"))
			if !shared.StringInSlice(req.Name, names) {
				break
			}

			if i > 100 {
				return response.InternalError(fmt.Errorf("couldn't generate a new unique name after 100 tries"))
			}
		}
		logger.Debugf("No name provided, creating %s", req.Name)
	}

	if req.Devices == nil {
		req.Devices = map[string]map[string]string{}
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	if req.InstanceType != "" {
		conf, err := instanceParseType(req.InstanceType)
		if err != nil {
			return response.BadRequest(err)
		}

		for k, v := range conf {
			if req.Config[k] == "" {
				req.Config[k] = v
			}
		}
	}

	if strings.Contains(req.Name, shared.SnapshotDelimiter) {
		return response.BadRequest(fmt.Errorf("Invalid container name: '%s' is reserved for snapshots", shared.SnapshotDelimiter))
	}

	switch req.Source.Type {
	case "image":
		return createFromImage(d, project, &req)
	case "none":
		return createFromNone(d, project, &req)
	case "migration":
		return createFromMigration(d, project, &req)
	case "copy":
		return createFromCopy(d, project, &req)
	default:
		return response.BadRequest(fmt.Errorf("unknown source type %s", req.Source.Type))
	}
}

func containerFindStoragePool(d *Daemon, project string, req *api.InstancesPost) (string, string, string, map[string]string, response.Response) {
	// Grab the container's root device if one is specified
	storagePool := ""
	storagePoolProfile := ""

	localRootDiskDeviceKey, localRootDiskDevice, _ := shared.GetRootDiskDevice(req.Devices)
	if localRootDiskDeviceKey != "" {
		storagePool = localRootDiskDevice["pool"]
	}

	// Handle copying/moving between two storage-api LXD instances.
	if storagePool != "" {
		_, err := d.cluster.StoragePoolGetID(storagePool)
		if err == db.ErrNoSuchObject {
			storagePool = ""
			// Unset the local root disk device storage pool if not
			// found.
			localRootDiskDevice["pool"] = ""
		}
	}

	// If we don't have a valid pool yet, look through profiles
	if storagePool == "" {
		for _, pName := range req.Profiles {
			_, p, err := d.cluster.ProfileGet(project, pName)
			if err != nil {
				return "", "", "", nil, response.SmartError(err)
			}

			k, v, _ := shared.GetRootDiskDevice(p.Devices)
			if k != "" && v["pool"] != "" {
				// Keep going as we want the last one in the profile chain
				storagePool = v["pool"]
				storagePoolProfile = pName
			}
		}
	}

	// If there is just a single pool in the database, use that
	if storagePool == "" {
		logger.Debugf("No valid storage pool in the container's local root disk device and profiles found")
		pools, err := d.cluster.StoragePools()
		if err != nil {
			if err == db.ErrNoSuchObject {
				return "", "", "", nil, response.BadRequest(fmt.Errorf("This LXD instance does not have any storage pools configured"))
			}
			return "", "", "", nil, response.SmartError(err)
		}

		if len(pools) == 1 {
			storagePool = pools[0]
		}
	}

	return storagePool, storagePoolProfile, localRootDiskDeviceKey, localRootDiskDevice, nil
}

func clusterCopyContainerInternal(d *Daemon, source Instance, project string, req *api.InstancesPost) response.Response {
	name := req.Source.Source

	// Locate the source of the container
	var nodeAddress string
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		// Load source node.
		nodeAddress, err = tx.ContainerNodeAddress(project, name, source.Type())
		if err != nil {
			return errors.Wrap(err, "Failed to get address of container's node")
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if nodeAddress == "" {
		return response.BadRequest(fmt.Errorf("The container source is currently offline"))
	}

	// Connect to the container source
	client, err := cluster.Connect(nodeAddress, d.endpoints.NetworkCert(), false)
	if err != nil {
		return response.SmartError(err)
	}

	client = client.UseProject(source.Project())

	// Setup websockets
	var opAPI api.Operation
	if shared.IsSnapshot(req.Source.Source) {
		cName, sName, _ := shared.ContainerGetParentAndSnapshotName(req.Source.Source)

		pullReq := api.InstanceSnapshotPost{
			Migration: true,
			Live:      req.Source.Live,
			Name:      req.Name,
		}

		op, err := client.MigrateInstanceSnapshot(cName, sName, pullReq)
		if err != nil {
			return response.SmartError(err)
		}

		opAPI = op.Get()
	} else {
		instanceOnly := req.Source.InstanceOnly || req.Source.ContainerOnly
		pullReq := api.InstancePost{
			Migration:     true,
			Live:          req.Source.Live,
			ContainerOnly: instanceOnly,
			InstanceOnly:  instanceOnly,
			Name:          req.Name,
		}

		op, err := client.MigrateInstance(req.Source.Source, pullReq)
		if err != nil {
			return response.SmartError(err)
		}

		opAPI = op.Get()
	}

	websockets := map[string]string{}
	for k, v := range opAPI.Metadata {
		websockets[k] = v.(string)
	}

	// Reset the source for a migration
	req.Source.Type = "migration"
	req.Source.Certificate = string(d.endpoints.NetworkCert().PublicKey())
	req.Source.Mode = "pull"
	req.Source.Operation = fmt.Sprintf("https://%s/1.0/operations/%s", nodeAddress, opAPI.ID)
	req.Source.Websockets = websockets
	req.Source.Source = ""
	req.Source.Project = ""

	// Run the migration
	return createFromMigration(d, project, req)
}
