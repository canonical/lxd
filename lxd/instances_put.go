package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/version"
)

// swagger:operation PUT /1.0/instances instances instances_put
//
//	Bulk instance state update
//
//	Changes the running state of all instances.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: state
//	    description: State
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstancesPut"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instancesPut(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)

	// Don't mess with instances while in setup mode.
	<-d.waitReady.Done()

	s := d.State()

	// Get all instances in the project.
	var members map[string]*db.NodeInfo
	var instances []instance.Instance
	err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := dbCluster.InstanceFilter{
			Project: &projectName,
		}

		err := tx.InstanceList(ctx, func(dbInst db.InstanceArgs, p api.Project) error {
			inst, err := instance.Load(s, dbInst, p)
			if err != nil {
				return fmt.Errorf("Failed loading instance %q in project %q: %w", dbInst.Name, dbInst.Project, err)
			}

			instances = append(instances, inst)

			return nil
		}, filter)
		if err != nil {
			return err
		}

		membersList, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		// Convert members list to map for easier lookup later.
		members = make(map[string]*db.NodeInfo, len(membersList))
		for _, member := range membersList {
			members[member.Name] = &member
		}

		return nil
	})
	if err != nil {
		return response.BadRequest(err)
	}

	req := api.InstancesPut{}
	req.State = &api.InstanceStatePut{}
	req.State.Timeout = -1
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	action := instancetype.InstanceAction(req.State.Action)

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanUpdateState, entity.TypeInstance)
	if err != nil {
		return response.SmartError(err)
	}

	for _, inst := range instances {
		// Check permission for all instances so that we apply the state change to all or none.
		if !userHasPermission(entity.InstanceURL(inst.Project().Name, inst.Name())) {
			return response.Forbidden(nil)
		}
	}

	// Batch the changes.
	childRunHookDo := func(ctx context.Context, op *operations.Operation, inst instance.Instance) error {
		// Get node member where the instance is located.
		member, ok := members[inst.Location()]
		if !ok {
			return fmt.Errorf("No cluster member found for instance %q location %q", inst.Name(), inst.Location())
		}

		// Get local cluster address.
		localClusterAddress := s.LocalConfig.ClusterAddress()

		// Run the action locally if not clustered, or if the instance is located on the local member.
		if !s.ServerClustered || member.Address == localClusterAddress {
			if !instanceActionNeeded(inst, action) {
				return nil
			}

			// Ideally we should call inst.SetOperation() here to the instance to send events and progress updates.
			// However, the operation is not available on other members handling instance updates. So, we don't set
			// the operation on instance here to keep the same behavior on all members.

			return doInstanceStatePut(inst, *req.State)
		}

		// Record the results.
		networkCert := s.Endpoints.NetworkCert()

		// Connect to the remote server.
		client, err := cluster.ConnectNotification(ctx, member.Address, networkCert, s.ServerCert(), request.ClientTypeOperationNotifier)
		if err != nil {
			return err
		}

		action := req.State.Action

		req := api.InstanceStatePut{
			Action:   action,
			Timeout:  req.State.Timeout,
			Force:    req.State.Force,
			Stateful: req.State.Stateful,
		}

		url := api.NewURL().Path(version.APIVersion, "instances", inst.Name(), "state").Project(projectName)
		_, _, err = client.RawQuery(http.MethodPut, url.String(), req, "")
		return err
	}

	// Set the child operations for each instance under a single parent operation on the project.
	opType, err := instanceActionToOptype(string(action))
	if err != nil {
		return response.BadRequest(err)
	}

	childArgs := make([]*operations.OperationArgs, 0, len(instances))
	for _, inst := range instances {
		// Create a run hook function for the child operations that captures the instance in its closure.
		childRunHook := func(inst instance.Instance) func(ctx context.Context, op *operations.Operation) error {
			return func(ctx context.Context, op *operations.Operation) error {
				return childRunHookDo(ctx, op, inst)
			}
		}

		instURL := api.NewURL().Path(version.APIVersion, "instances", inst.Name()).Project(projectName)
		args := operations.OperationArgs{
			ProjectName: projectName,
			EntityURL:   instURL,
			Type:        opType,
			Class:       operations.OperationClassTask,
			RunHook:     childRunHook(inst),
		}

		childArgs = append(childArgs, &args)
	}

	// Create a parent operation for the bulk state change.
	// There's no run hook, as the parent operation doesn't need to do anything.
	projectURL := api.NewURL().Path(version.APIVersion, "projects", projectName)
	args := operations.OperationArgs{
		ProjectName: projectName,
		EntityURL:   projectURL,
		Type:        operationtype.InstanceStateUpdateBulk,
		Class:       operations.OperationClassTask,
		Children:    childArgs,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
