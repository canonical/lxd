package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	petname "github.com/dustinkirkland/golang-petname"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/archive"
	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	dbCluster "github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/operationtype"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/instance/operationlock"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/revert"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
)

func createFromImage(d *Daemon, r *http.Request, p api.Project, profiles []api.Profile, img *api.Image, imgAlias string, req *api.InstancesPost) response.Response {
	if d.db.Cluster.LocalNodeIsEvacuated() {
		return response.Forbidden(fmt.Errorf("Cluster member is evacuated"))
	}

	dbType, err := instancetype.New(string(req.Type))
	if err != nil {
		return response.BadRequest(err)
	}

	s := d.State()

	run := func(op *operations.Operation) error {
		args := db.InstanceArgs{
			Project:     p.Name,
			Config:      req.Config,
			Type:        dbType,
			Description: req.Description,
			Devices:     deviceConfig.NewDevices(req.Devices),
			Ephemeral:   req.Ephemeral,
			Name:        req.Name,
			Profiles:    profiles,
		}

		if req.Source.Server != "" {
			var autoUpdate bool
			if p.Config["images.auto_update_cached"] != "" {
				autoUpdate = shared.IsTrue(p.Config["images.auto_update_cached"])
			} else {
				autoUpdate = s.GlobalConfig.ImagesAutoUpdateCached()
			}

			var budget int64
			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				budget, err = project.GetImageSpaceBudget(tx, p.Name)
				return err
			})
			if err != nil {
				return err
			}

			img, err = ImageDownload(r, s, op, &ImageDownloadArgs{
				Server:       req.Source.Server,
				Protocol:     req.Source.Protocol,
				Certificate:  req.Source.Certificate,
				Secret:       req.Source.Secret,
				Alias:        imgAlias,
				SetCached:    true,
				Type:         string(req.Type),
				AutoUpdate:   autoUpdate,
				Public:       false,
				PreferCached: true,
				ProjectName:  p.Name,
				Budget:       budget,
			})
			if err != nil {
				return err
			}
		}

		if img == nil {
			return fmt.Errorf("Image not provided for instance creation")
		}

		args.Architecture, err = osarch.ArchitectureId(img.Architecture)
		if err != nil {
			return err
		}

		_, err = instanceCreateFromImage(d, r, img, args, op)
		return err
	}

	resources := map[string][]string{}
	resources["instances"] = []string{req.Name}

	if dbType == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(s, p.Name, operations.OperationClassTask, operationtype.InstanceCreate, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func createFromNone(d *Daemon, r *http.Request, projectName string, profiles []api.Profile, req *api.InstancesPost) response.Response {
	if d.db.Cluster.LocalNodeIsEvacuated() {
		return response.Forbidden(fmt.Errorf("Cluster member is evacuated"))
	}

	dbType, err := instancetype.New(string(req.Type))
	if err != nil {
		return response.BadRequest(err)
	}

	args := db.InstanceArgs{
		Project:     projectName,
		Config:      req.Config,
		Type:        dbType,
		Description: req.Description,
		Devices:     deviceConfig.NewDevices(req.Devices),
		Ephemeral:   req.Ephemeral,
		Name:        req.Name,
		Profiles:    profiles,
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

	if dbType == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask, operationtype.InstanceCreate, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func createFromMigration(d *Daemon, r *http.Request, projectName string, profiles []api.Profile, req *api.InstancesPost) response.Response {
	if d.db.Cluster.LocalNodeIsEvacuated() && r.Context().Value(request.CtxProtocol) != "cluster" {
		return response.Forbidden(fmt.Errorf("Cluster member is evacuated"))
	}

	// Validate migration mode.
	if req.Source.Mode != "pull" && req.Source.Mode != "push" {
		return response.NotImplemented(fmt.Errorf("Mode %q not implemented", req.Source.Mode))
	}

	// Parse the architecture name
	architecture, err := osarch.ArchitectureId(req.Architecture)
	if err != nil {
		return response.BadRequest(err)
	}

	dbType, err := instancetype.New(string(req.Type))
	if err != nil {
		return response.BadRequest(err)
	}

	if dbType != instancetype.Container && dbType != instancetype.VM {
		return response.BadRequest(fmt.Errorf("Instance type not supported %q", req.Type))
	}

	// Prepare the instance creation request.
	args := db.InstanceArgs{
		Project:      projectName,
		Architecture: architecture,
		BaseImage:    req.Source.BaseImage,
		Config:       req.Config,
		Type:         dbType,
		Devices:      deviceConfig.NewDevices(req.Devices),
		Description:  req.Description,
		Ephemeral:    req.Ephemeral,
		Name:         req.Name,
		Profiles:     profiles,
		Stateful:     req.Stateful,
	}

	storagePool, storagePoolProfile, localRootDiskDeviceKey, localRootDiskDevice, resp := instanceFindStoragePool(d, projectName, req)
	if resp != nil {
		return resp
	}

	if storagePool == "" {
		return response.BadRequest(fmt.Errorf("Can't find a storage pool for the instance to use"))
	}

	if localRootDiskDeviceKey == "" && storagePoolProfile == "" {
		// Give the container it's own local root disk device with a pool property.
		rootDev := map[string]string{}
		rootDev["type"] = "disk"
		rootDev["path"] = "/"
		rootDev["pool"] = storagePool
		if args.Devices == nil {
			args.Devices = deviceConfig.Devices{}
		}

		// Make sure that we do not overwrite a device the user is currently using under the
		// name "root".
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

	var inst instance.Instance
	var instOp *operationlock.InstanceOperation
	var cleanup revert.Hook

	// Early check for refresh.
	if req.Source.Refresh {
		// Check if the instance exists.
		inst, err = instance.LoadByProjectAndName(d.State(), projectName, req.Name)
		if err != nil {
			if response.IsNotFoundError(err) {
				req.Source.Refresh = false
			} else {
				return response.InternalError(err)
			}
		}
	}

	revert := revert.New()
	defer revert.Fail()

	instanceOnly := req.Source.InstanceOnly || req.Source.ContainerOnly

	if !req.Source.Refresh {
		_, err := storagePools.LoadByName(d.State(), storagePool)
		if err != nil {
			return response.InternalError(err)
		}

		// Create the instance and storage DB records for main instance.
		// Note: At this stage we do not yet know if snapshots are going to be received and so we cannot
		// create their DB records. This will be done if needed in the migrationSink.Do() function called
		// as part of the operation below.
		inst, instOp, cleanup, err = instance.CreateInternal(d.State(), args, true)
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed creating instance record: %w", err))
		}

		revert.Add(cleanup)
	} else {
		instOp, err = inst.LockExclusive()
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed getting exclusive access to instance: %w", err))
		}
	}

	defer instOp.Done(err)

	var cert *x509.Certificate
	if req.Source.Certificate != "" {
		certBlock, _ := pem.Decode([]byte(req.Source.Certificate))
		if certBlock == nil {
			return response.InternalError(fmt.Errorf("Invalid certificate"))
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return response.InternalError(err)
		}
	}

	config, err := shared.GetTLSConfig("", "", "", cert)
	if err != nil {
		return response.InternalError(err)
	}

	push := false
	if req.Source.Mode == "push" {
		push = true
	}

	migrationArgs := MigrationSinkArgs{
		URL: req.Source.Operation,
		Dialer: websocket.Dialer{
			TLSClientConfig:  config,
			NetDialContext:   shared.RFC3493Dialer,
			HandshakeTimeout: time.Second * 5,
		},
		Instance:     inst,
		Secrets:      req.Source.Websockets,
		Push:         push,
		Live:         req.Source.Live,
		InstanceOnly: instanceOnly,
		Refresh:      req.Source.Refresh,
	}

	sink, err := newMigrationSink(&migrationArgs)
	if err != nil {
		return response.InternalError(err)
	}

	// Copy reverter so far so we can use it inside run after this function has finished.
	runRevert := revert.Clone()

	run := func(op *operations.Operation) error {
		defer runRevert.Fail()

		// And finally run the migration.
		err = sink.Do(d.State(), runRevert, op)
		if err != nil {
			return fmt.Errorf("Error transferring instance data: %w", err)
		}

		err = inst.DeferTemplateApply(instance.TemplateTriggerCopy)
		if err != nil {
			return err
		}

		runRevert.Success()
		return nil
	}

	resources := map[string][]string{}
	resources["instances"] = []string{req.Name}

	if dbType == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	var op *operations.Operation
	if push {
		op, err = operations.OperationCreate(d.State(), projectName, operations.OperationClassWebsocket, operationtype.InstanceCreate, resources, sink.Metadata(), run, nil, sink.Connect, r)
		if err != nil {
			return response.InternalError(err)
		}
	} else {
		op, err = operations.OperationCreate(d.State(), projectName, operations.OperationClassTask, operationtype.InstanceCreate, resources, nil, run, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}
	}

	revert.Success()
	return operations.OperationResponse(op)
}

func createFromCopy(d *Daemon, r *http.Request, projectName string, profiles []api.Profile, req *api.InstancesPost) response.Response {
	if d.db.Cluster.LocalNodeIsEvacuated() {
		return response.Forbidden(fmt.Errorf("Cluster member is evacuated"))
	}

	if req.Source.Source == "" {
		return response.BadRequest(fmt.Errorf("Must specify a source instance"))
	}

	sourceProject := req.Source.Project
	if sourceProject == "" {
		sourceProject = projectName
	}

	targetProject := projectName

	source, err := instance.LoadByProjectAndName(d.State(), sourceProject, req.Source.Source)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if we need to redirect to migration
	clustered, err := cluster.Enabled(d.db.Node)
	if err != nil {
		return response.SmartError(err)
	}

	// When clustered, use the node name, otherwise use the hostname.
	if clustered {
		serverName := d.State().ServerName

		if serverName != source.Location() {
			// Check if we are copying from a ceph-based container.
			_, rootDevice, _ := shared.GetRootDiskDevice(source.ExpandedDevices().CloneNative())
			sourcePoolName := rootDevice["pool"]

			destPoolName, _, _, _, resp := instanceFindStoragePool(d, targetProject, req)
			if resp != nil {
				return resp
			}

			if sourcePoolName != destPoolName {
				// Redirect to migration
				return clusterCopyContainerInternal(d, r, source, projectName, profiles, req)
			}

			_, pool, _, err := d.db.Cluster.GetStoragePoolInAnyState(sourcePoolName)
			if err != nil {
				err = fmt.Errorf("Failed to fetch instance's pool info: %w", err)
				return response.SmartError(err)
			}

			if pool.Driver != "ceph" {
				// Redirect to migration
				return clusterCopyContainerInternal(d, r, source, projectName, profiles, req)
			}
		}
	}

	// Config override
	sourceConfig := source.LocalConfig()
	if req.Config == nil {
		req.Config = make(map[string]string)
	}

	for key, value := range sourceConfig {
		if !shared.InstanceIncludeWhenCopying(key, false) {
			logger.Debug("Skipping key from copy source", logger.Ctx{"key": key, "sourceProject": source.Project().Name, "sourceInstance": source.Name(), "project": targetProject, "instance": req.Name})
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

	if req.Stateful {
		sourceName, _, _ := api.GetParentAndSnapshotName(source.Name())
		if sourceName != req.Name {
			return response.BadRequest(fmt.Errorf("Instance name cannot be changed during stateful copy (%q to %q)", sourceName, req.Name))
		}
	}

	dbType, err := instancetype.New(string(req.Type))
	if err != nil {
		return response.BadRequest(err)
	}

	// If type isn't specified, match the source type.
	if req.Type == "" {
		dbType = source.Type()
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
		Profiles:     profiles,
		Stateful:     req.Stateful,
	}

	run := func(op *operations.Operation) error {
		_, err := instanceCreateAsCopy(d.State(), instanceCreateAsCopyOpts{
			sourceInstance:       source,
			targetInstance:       args,
			instanceOnly:         req.Source.InstanceOnly || req.Source.ContainerOnly,
			refresh:              req.Source.Refresh,
			applyTemplateTrigger: true,
			allowInconsistent:    req.Source.AllowInconsistent,
		}, op)
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["instances"] = []string{req.Name, req.Source.Source}

	if dbType == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(d.State(), targetProject, operations.OperationClassTask, operationtype.InstanceCreate, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func createFromBackup(d *Daemon, r *http.Request, projectName string, data io.Reader, pool string, instanceName string) response.Response {
	revert := revert.New()
	defer revert.Fail()

	// Create temporary file to store uploaded backup data.
	backupFile, err := os.CreateTemp(shared.VarPath("backups"), fmt.Sprintf("%s_", backup.WorkingDirPrefix))
	if err != nil {
		return response.InternalError(err)
	}

	defer func() { _ = os.Remove(backupFile.Name()) }()
	revert.Add(func() { _ = backupFile.Close() })

	// Stream uploaded backup data into temporary file.
	_, err = io.Copy(backupFile, data)
	if err != nil {
		return response.InternalError(err)
	}

	// Detect squashfs compression and convert to tarball.
	_, err = backupFile.Seek(0, 0)
	if err != nil {
		return response.InternalError(err)
	}

	_, algo, decomArgs, err := shared.DetectCompressionFile(backupFile)
	if err != nil {
		return response.InternalError(err)
	}

	if algo == ".squashfs" {
		// Pass the temporary file as program argument to the decompression command.
		decomArgs := append(decomArgs, backupFile.Name())

		// Create temporary file to store the decompressed tarball in.
		tarFile, err := os.CreateTemp(shared.VarPath("backups"), fmt.Sprintf("%s_decompress_", backup.WorkingDirPrefix))
		if err != nil {
			return response.InternalError(err)
		}

		defer func() { _ = os.Remove(tarFile.Name()) }()

		// Decompress to tarFile temporary file.
		err = archive.ExtractWithFds(decomArgs[0], decomArgs[1:], nil, nil, d.State().OS, tarFile)
		if err != nil {
			return response.InternalError(err)
		}

		// We don't need the original squashfs file anymore.
		_ = backupFile.Close()
		_ = os.Remove(backupFile.Name())

		// Replace the backup file handle with the handle to the tar file.
		backupFile = tarFile
	}

	// Parse the backup information.
	_, err = backupFile.Seek(0, 0)
	if err != nil {
		return response.InternalError(err)
	}

	logger.Debug("Reading backup file info")
	bInfo, err := backup.GetInfo(backupFile, d.State().OS, backupFile.Name())
	if err != nil {
		return response.BadRequest(err)
	}

	bInfo.Project = projectName

	// Override pool.
	if pool != "" {
		bInfo.Pool = pool
	}

	// Override instance name.
	if instanceName != "" {
		bInfo.Name = instanceName
	}

	logger.Debug("Backup file info loaded", logger.Ctx{
		"type":      bInfo.Type,
		"name":      bInfo.Name,
		"project":   bInfo.Project,
		"backend":   bInfo.Backend,
		"pool":      bInfo.Pool,
		"optimized": *bInfo.OptimizedStorage,
		"snapshots": bInfo.Snapshots,
	})

	// Check storage pool exists.
	_, _, _, err = d.State().DB.Cluster.GetStoragePoolInAnyState(bInfo.Pool)
	if response.IsNotFoundError(err) {
		// The storage pool doesn't exist. If backup is in binary format (so we cannot alter
		// the backup.yaml) or the pool has been specified directly from the user restoring
		// the backup then we cannot proceed so return an error.
		if *bInfo.OptimizedStorage || pool != "" {
			return response.InternalError(fmt.Errorf("Storage pool not found: %w", err))
		}

		// Otherwise try and restore to the project's default profile pool.
		_, profile, err := d.State().DB.Cluster.GetProfile(bInfo.Project, "default")
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed to get default profile: %w", err))
		}

		_, v, err := shared.GetRootDiskDevice(profile.Devices)
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed to get root disk device: %w", err))
		}

		// Use the default-profile's root pool.
		bInfo.Pool = v["pool"]
	} else if err != nil {
		return response.InternalError(err)
	}

	// Copy reverter so far so we can use it inside run after this function has finished.
	runRevert := revert.Clone()

	run := func(op *operations.Operation) error {
		defer func() { _ = backupFile.Close() }()
		defer runRevert.Fail()

		pool, err := storagePools.LoadByName(d.State(), bInfo.Pool)
		if err != nil {
			return err
		}

		// Check if the backup is optimized that the source pool driver matches the target pool driver.
		if *bInfo.OptimizedStorage && pool.Driver().Info().Name != bInfo.Backend {
			return fmt.Errorf("Optimized backup storage driver %q differs from the target storage pool driver %q", bInfo.Backend, pool.Driver().Info().Name)
		}

		// Dump tarball to storage. Because the backup file is unpacked and restored onto the storage
		// device before the instance is created in the database it is necessary to return two functions;
		// a post hook that can be run once the instance has been created in the database to run any
		// storage layer finalisations, and a revert hook that can be run if the instance database load
		// process fails that will remove anything created thus far.
		postHook, revertHook, err := pool.CreateInstanceFromBackup(*bInfo, backupFile, nil)
		if err != nil {
			return fmt.Errorf("Create instance from backup: %w", err)
		}

		runRevert.Add(revertHook)

		err = internalImportFromBackup(d, bInfo.Project, bInfo.Name, true, instanceName != "")
		if err != nil {
			return fmt.Errorf("Failed importing backup: %w", err)
		}

		inst, err := instance.LoadByProjectAndName(d.State(), bInfo.Project, bInfo.Name)
		if err != nil {
			return fmt.Errorf("Load instance: %w", err)
		}

		// Clean up created instance if the post hook fails below.
		runRevert.Add(func() { _ = inst.Delete(true) })

		// Run the storage post hook to perform any final actions now that the instance has been created
		// in the database (this normally includes unmounting volumes that were mounted).
		if postHook != nil {
			err = postHook(inst)
			if err != nil {
				return fmt.Errorf("Post hook failed: %w", err)
			}
		}

		runRevert.Success()
		return nil
	}

	resources := map[string][]string{}
	resources["instances"] = []string{bInfo.Name}
	resources["containers"] = resources["instances"]

	op, err := operations.OperationCreate(d.State(), bInfo.Project, operations.OperationClassTask, operationtype.BackupRestore, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	revert.Success()
	return operations.OperationResponse(op)
}

// swagger:operation POST /1.0/instances instances instances_post
//
// Create a new instance
//
// Creates a new instance on LXD.
// Depending on the source, this can create an instance from an existing
// local image, remote image, existing local instance or snapshot, remote
// migration stream or backup file.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member
//     type: string
//     example: default
//   - in: body
//     name: instance
//     description: Instance request
//     required: false
//     schema:
//       $ref: "#/definitions/InstancesPost"
//   - in: body
//     name: raw_backup
//     description: Raw backup file
//     required: false
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func instancesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	targetProjectName := projectParam(r)
	clusterNotification := isClusterNotification(r)

	logger.Debug("Responding to instance create")

	// If we're getting binary content, process separately
	if r.Header.Get("Content-Type") == "application/octet-stream" {
		return createFromBackup(d, r, targetProjectName, r.Body, r.Header.Get("X-LXD-pool"), r.Header.Get("X-LXD-name"))
	}

	// Parse the request
	req := api.InstancesPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Set type from URL if missing
	urlType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.InternalError(err)
	}

	if req.Type == "" && urlType != instancetype.Any {
		req.Type = api.InstanceType(urlType.String())
	}

	if req.Type == "" {
		req.Type = api.InstanceTypeContainer // Default to container if not specified.
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

	// Check if clustered.
	clustered, err := cluster.Enabled(d.db.Node)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to check for cluster state: %w", err))
	}

	var targetProject *api.Project
	var profiles []api.Profile
	var sourceInst *dbCluster.Instance
	var sourceImage *api.Image
	var sourceImageRef string
	var clusterGroupsAllowed []string
	var candidateMembers []db.NodeInfo
	var targetMemberInfo *db.NodeInfo

	err = d.db.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		target := queryParam(r, "target")
		if !clustered && target != "" {
			return api.StatusErrorf(http.StatusBadRequest, "Target only allowed when clustered")
		}

		var targetMember, targetGroup string
		if strings.HasPrefix(target, "@") {
			targetGroup = strings.TrimPrefix(target, "@")
		} else {
			targetMember = target
		}

		dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), targetProjectName)
		if err != nil {
			return fmt.Errorf("Failed loading project: %w", err)
		}

		targetProject, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		var allMembers []db.NodeInfo

		if clustered && !clusterNotification {
			clusterGroupsAllowed = shared.SplitNTrimSpace(targetProject.Config["restricted.cluster.groups"], ",", -1, true)

			// Check manual cluster member targeting restrictions.
			err = project.CheckClusterTargetRestriction(r, targetProject, target)
			if err != nil {
				return err
			}

			allMembers, err = tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			if targetMember != "" {
				// Find target member.
				for i := range allMembers {
					if allMembers[i].Name == targetMember {
						targetMemberInfo = &allMembers[i]
						break
					}
				}

				if targetMemberInfo == nil {
					return api.StatusErrorf(http.StatusNotFound, "Cluster member not found")
				}

				// If restricted groups are specified then check member is in at least one of them.
				if shared.IsTrue(targetProject.Config["restricted"]) && len(clusterGroupsAllowed) > 0 {
					found := false
					for _, memberGroupName := range targetMemberInfo.Groups {
						if shared.StringInSlice(memberGroupName, clusterGroupsAllowed) {
							found = true
							break
						}
					}

					if !found {
						return api.StatusErrorf(http.StatusForbidden, "Project isn't allowed to use this cluster member")
					}
				}
			} else if targetGroup != "" {
				// If restricted groups are specified then check the requested group is in the list.
				if shared.IsTrue(targetProject.Config["restricted"]) && len(clusterGroupsAllowed) > 0 && !shared.StringInSlice(targetGroup, clusterGroupsAllowed) {
					return api.StatusErrorf(http.StatusForbidden, "Project isn't allowed to use this cluster group")
				}

				// Check if the target group exists.
				targetGroupExists, err := tx.ClusterGroupExists(targetGroup)
				if err != nil {
					return err
				}

				if !targetGroupExists {
					return api.StatusErrorf(http.StatusBadRequest, "Cluster group %q doesn't exist", targetGroup)
				}
			}
		}

		profileProject := project.ProfileProjectFromRecord(targetProject)

		switch req.Source.Type {
		case "copy":
			if req.Source.Source == "" {
				return api.StatusErrorf(http.StatusBadRequest, "Must specify a source instance")
			}

			if req.Source.Project == "" {
				req.Source.Project = targetProjectName
			}

			sourceInst, err = instance.LoadInstanceDatabaseObject(ctx, tx, req.Source.Project, req.Source.Source)
			if err != nil {
				return err
			}

			req.Type = api.InstanceType(sourceInst.Type.String())

			// Use source instance's profiles if no profile override.
			if req.Profiles == nil {
				sourceInstArgs, err := tx.InstancesToInstanceArgs(ctx, true, *sourceInst)
				if err != nil {
					return err
				}

				req.Profiles = make([]string, 0, len(sourceInstArgs[sourceInst.ID].Profiles))
				for _, profile := range sourceInstArgs[sourceInst.ID].Profiles {
					req.Profiles = append(req.Profiles, profile.Name)
				}
			}

		case "image":
			// Resolve the image.
			sourceImageRef, err = instance.ResolveImage(ctx, tx, targetProject.Name, req.Source)
			if err != nil {
				return err
			}

			sourceImageHash := sourceImageRef

			// If a remote server is being used, check whether we have a cached image for the alias.
			// If so then use the cached image fingerprint for loading the cache image profiles.
			// As its possible for a remote cached image to have its profiles modified after download.
			if req.Source.Server != "" {
				for _, architecture := range d.os.Architectures {
					cachedFingerprint, err := tx.GetCachedImageSourceFingerprint(ctx, req.Source.Server, req.Source.Protocol, sourceImageRef, string(req.Type), architecture)
					if err == nil && cachedFingerprint != sourceImageHash {
						sourceImageHash = cachedFingerprint
						break
					}
				}
			}

			// Check if image has an entry in the database (but don't fail if not found).
			_, sourceImage, err = tx.GetImageByFingerprintPrefix(ctx, sourceImageHash, dbCluster.ImageFilter{Project: &targetProject.Name})
			if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
				return err
			}

			// If image has an entry in the database then use its profiles if no override provided.
			if sourceImage != nil && req.Profiles == nil {
				req.Architecture = sourceImage.Architecture
				req.Profiles = sourceImage.Profiles
			}
		}

		// Use default profile if no profile list specified (not even an empty list).
		// This mirrors the logic in instance.CreateInternal() that would occur anyway.
		if req.Profiles == nil {
			req.Profiles = []string{"default"}
		}

		// Initialise the profile info list (even if an empty list is provided so this isn't left as nil).
		// This way instances can still be created without any profiles by providing a non-nil empty list.
		profiles = make([]api.Profile, 0, len(req.Profiles))

		// Load profiles.
		if len(req.Profiles) > 0 {
			profileFilters := make([]dbCluster.ProfileFilter, 0, len(req.Profiles))
			for _, profileName := range req.Profiles {
				profileName := profileName
				profileFilters = append(profileFilters, dbCluster.ProfileFilter{
					Project: &profileProject,
					Name:    &profileName,
				})
			}

			dbProfiles, err := dbCluster.GetProfiles(ctx, tx.Tx(), profileFilters...)
			if err != nil {
				return err
			}

			profilesByName := make(map[string]dbCluster.Profile, len(dbProfiles))
			for _, dbProfile := range dbProfiles {
				profilesByName[dbProfile.Name] = dbProfile
			}

			for _, profileName := range req.Profiles {
				profile, found := profilesByName[profileName]
				if !found {
					return fmt.Errorf("Requested profile %q doesn't exist", profileName)
				}

				apiProfile, err := profile.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				profiles = append(profiles, *apiProfile)
			}
		}

		// Generate automatic instance name if not specified.
		if req.Name == "" {
			names, err := tx.GetInstanceNames(ctx, targetProjectName)
			if err != nil {
				return err
			}

			i := 0
			for {
				i++
				req.Name = strings.ToLower(petname.Generate(2, "-"))
				if !shared.StringInSlice(req.Name, names) {
					break
				}

				if i > 100 {
					return fmt.Errorf("Couldn't generate a new unique name after 100 tries")
				}
			}

			logger.Debug("No name provided for new instance, using auto-generated name", logger.Ctx{"project": targetProjectName, "instance": req.Name})
		}

		if clustered && !clusterNotification && targetMemberInfo == nil {
			architectures, err := instance.SuitableArchitectures(ctx, s, tx, targetProjectName, sourceInst, sourceImageRef, req)
			if err != nil {
				return err
			}

			// If no architectures have been ascertained from the source then use the default
			// architecture from project or global config if available.
			if len(architectures) < 1 {
				defaultArch := targetProject.Config["images.default_architecture"]
				if defaultArch == "" {
					defaultArch = s.GlobalConfig.ImagesDefaultArchitecture()
				}

				if defaultArch != "" {
					defaultArchID, err := osarch.ArchitectureId(defaultArch)
					if err != nil {
						return err
					}

					architectures = append(architectures, defaultArchID)
				} else {
					architectures = nil // Don't exclude candidate members based on architecture.
				}
			}

			candidateMembers, err = tx.GetCandidateMembers(ctx, allMembers, architectures, targetGroup, clusterGroupsAllowed, s.GlobalConfig.OfflineThreshold())
			if err != nil {
				return err
			}

			return nil
		}

		if !clusterNotification {
			// Check that the project's limits are not violated. Note this check is performed after
			// automatically generated config values (such as ones from an InstanceType) have been set.
			err = project.AllowInstanceCreation(tx, targetProjectName, req)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	err = instance.ValidName(req.Name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	if clustered && !clusterNotification && targetMemberInfo == nil {
		// If no target member was selected yet, pick the member with the least number of instances.
		if targetMemberInfo == nil {
			err = d.db.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				targetMemberInfo, err = tx.GetNodeWithLeastInstances(ctx, candidateMembers)
				if err != nil {
					return err
				}

				if targetMemberInfo == nil {
					return api.StatusErrorf(http.StatusBadRequest, "No suitable cluster member could be found")
				}

				return nil
			})
			if err != nil {
				return response.SmartError(err)
			}
		}
	}

	if targetMemberInfo != nil && targetMemberInfo.Address != "" && targetMemberInfo.Address != s.ServerName {
		client, err := cluster.Connect(targetMemberInfo.Address, d.endpoints.NetworkCert(), d.serverCert(), r, true)
		if err != nil {
			return response.SmartError(err)
		}

		client = client.UseProject(targetProjectName)
		client = client.UseTarget(targetMemberInfo.Name)

		logger.Debug("Forward instance post request", logger.Ctx{"member": targetMemberInfo.Address})
		op, err := client.CreateInstance(req)
		if err != nil {
			return response.SmartError(err)
		}

		opAPI := op.Get()
		return operations.ForwardedOperationResponse(targetProjectName, &opAPI)
	}

	switch req.Source.Type {
	case "image":
		return createFromImage(d, r, *targetProject, profiles, sourceImage, sourceImageRef, &req)
	case "none":
		return createFromNone(d, r, targetProjectName, profiles, &req)
	case "migration":
		return createFromMigration(d, r, targetProjectName, profiles, &req)
	case "copy":
		return createFromCopy(d, r, targetProjectName, profiles, &req)
	default:
		return response.BadRequest(fmt.Errorf("Unknown source type %s", req.Source.Type))
	}
}

func instanceFindStoragePool(d *Daemon, projectName string, req *api.InstancesPost) (string, string, string, map[string]string, response.Response) {
	// Grab the container's root device if one is specified
	storagePool := ""
	storagePoolProfile := ""

	localRootDiskDeviceKey, localRootDiskDevice, _ := shared.GetRootDiskDevice(req.Devices)
	if localRootDiskDeviceKey != "" {
		storagePool = localRootDiskDevice["pool"]
	}

	// Handle copying/moving between two storage-api LXD instances.
	if storagePool != "" {
		_, err := d.db.Cluster.GetStoragePoolID(storagePool)
		if response.IsNotFoundError(err) {
			storagePool = ""
			// Unset the local root disk device storage pool if not
			// found.
			localRootDiskDevice["pool"] = ""
		}
	}

	// If we don't have a valid pool yet, look through profiles
	if storagePool == "" {
		for _, pName := range req.Profiles {
			_, p, err := d.db.Cluster.GetProfile(projectName, pName)
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
		logger.Debug("No valid storage pool in the container's local root disk device and profiles found")
		pools, err := d.db.Cluster.GetStoragePoolNames()
		if err != nil {
			if response.IsNotFoundError(err) {
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

func clusterCopyContainerInternal(d *Daemon, r *http.Request, source instance.Instance, projectName string, profiles []api.Profile, req *api.InstancesPost) response.Response {
	name := req.Source.Source

	// Locate the source of the container
	var nodeAddress string
	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Load source node.
		nodeAddress, err = tx.GetNodeAddressOfInstance(ctx, projectName, name, source.Type())
		if err != nil {
			return fmt.Errorf("Failed to get address of instance's member: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if nodeAddress == "" {
		return response.BadRequest(fmt.Errorf("The source instance is currently offline"))
	}

	// Connect to the container source
	client, err := cluster.Connect(nodeAddress, d.endpoints.NetworkCert(), d.serverCert(), r, false)
	if err != nil {
		return response.SmartError(err)
	}

	client = client.UseProject(source.Project().Name)

	// Setup websockets
	var opAPI api.Operation
	if shared.IsSnapshot(req.Source.Source) {
		cName, sName, _ := api.GetParentAndSnapshotName(req.Source.Source)

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
	return createFromMigration(d, nil, projectName, profiles, req)
}
