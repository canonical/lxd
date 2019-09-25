package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/daemon"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/operation"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

func containerBackupsGet(d *Daemon, r *http.Request) daemon.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return SmartError(err)
	}

	project := projectParam(r)
	cname := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, cname, instanceType)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	recursion := util.IsRecursionRequest(r)

	c, err := instance.InstanceLoadByProjectAndName(d.State(), project, cname)
	if err != nil {
		return SmartError(err)
	}

	backups, err := c.Backups()
	if err != nil {
		return SmartError(err)
	}

	resultString := []string{}
	resultMap := []*api.InstanceBackup{}

	for _, backup := range backups {
		if !recursion {
			url := fmt.Sprintf("/%s/containers/%s/backups/%s",
				version.APIVersion, cname, strings.Split(backup.Name, "/")[1])
			resultString = append(resultString, url)
		} else {
			render := backup.Render()
			resultMap = append(resultMap, render)
		}
	}

	if !recursion {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

func containerBackupsPost(d *Daemon, r *http.Request) daemon.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	c, err := instance.InstanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

	rj := shared.Jmap{}
	err = json.NewDecoder(r.Body).Decode(&rj)
	if err != nil {
		return InternalError(err)
	}

	expiry, _ := rj.GetString("expiry")
	if expiry == "" {
		// Disable expiration by setting it to zero time
		rj["expiry"] = time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)
	}

	// Create body with correct expiry
	body, err := json.Marshal(rj)
	if err != nil {
		return InternalError(err)
	}

	req := api.InstanceBackupsPost{}

	err = json.Unmarshal(body, &req)
	if err != nil {
		return BadRequest(err)
	}

	if req.Name == "" {
		// come up with a name
		backups, err := c.Backups()
		if err != nil {
			return BadRequest(err)
		}

		base := name + shared.SnapshotDelimiter + "backup"
		length := len(base)
		max := 0

		for _, backup := range backups {
			// Ignore backups not containing base
			if !strings.HasPrefix(backup.Name, base) {
				continue
			}

			substr := backup.Name[length:]
			var num int
			count, err := fmt.Sscanf(substr, "%d", &num)
			if err != nil || count != 1 {
				continue
			}
			if num >= max {
				max = num + 1
			}
		}

		req.Name = fmt.Sprintf("backup%d", max)
	}

	// Validate the name
	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Backup names may not contain slashes"))
	}

	fullName := name + shared.SnapshotDelimiter + req.Name
	instanceOnly := req.InstanceOnly || req.ContainerOnly

	backup := func(op *operation.Operation) error {
		args := db.ContainerBackupArgs{
			Name:             fullName,
			ContainerID:      c.Id(),
			CreationDate:     time.Now(),
			ExpiryDate:       req.ExpiresAt,
			InstanceOnly:     instanceOnly,
			OptimizedStorage: req.OptimizedStorage,
		}

		err := backupCreate(d.State(), args, c)
		if err != nil {
			return errors.Wrap(err, "Create backup")
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}
	resources["backups"] = []string{req.Name}

	op, err := operation.OperationCreate(d.cluster, project, operation.OperationClassTask,
		db.OperationBackupCreate, resources, nil, backup, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func containerBackupGet(d *Daemon, r *http.Request) daemon.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]
	backupName := mux.Vars(r)["backupName"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	fullName := name + shared.SnapshotDelimiter + backupName
	backup, err := instance.BackupLoadByName(d.State(), project, fullName)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, backup.Render())
}

func containerBackupPost(d *Daemon, r *http.Request) daemon.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]
	backupName := mux.Vars(r)["backupName"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	req := api.InstanceBackupPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Validate the name
	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Backup names may not contain slashes"))
	}

	oldName := name + shared.SnapshotDelimiter + backupName
	backup, err := instance.BackupLoadByName(d.State(), project, oldName)
	if err != nil {
		return SmartError(err)
	}

	newName := name + shared.SnapshotDelimiter + req.Name

	rename := func(op *operation.Operation) error {
		err := backup.Rename(newName)
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operation.OperationCreate(d.cluster, project, operation.OperationClassTask,
		db.OperationBackupRename, resources, nil, rename, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func containerBackupDelete(d *Daemon, r *http.Request) daemon.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]
	backupName := mux.Vars(r)["backupName"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	fullName := name + shared.SnapshotDelimiter + backupName
	backup, err := instance.BackupLoadByName(d.State(), project, fullName)
	if err != nil {
		return SmartError(err)
	}

	remove := func(op *operation.Operation) error {
		err := backup.Delete()
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["container"] = []string{name}

	op, err := operation.OperationCreate(d.cluster, project, operation.OperationClassTask,
		db.OperationBackupRemove, resources, nil, remove, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func containerBackupExportGet(d *Daemon, r *http.Request) daemon.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]
	backupName := mux.Vars(r)["backupName"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	fullName := name + shared.SnapshotDelimiter + backupName
	backup, err := instance.BackupLoadByName(d.State(), project, fullName)
	if err != nil {
		return SmartError(err)
	}

	ent := fileResponseEntry{
		path: shared.VarPath("backups", backup.Name),
	}

	return FileResponse(r, []fileResponseEntry{ent}, nil, false)
}
