package main

import (
	"errors"
	"net/http"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

var api10StateCmd = APIEndpoint{
	Path:        "state",
	MetricsType: entity.TypeServer,

	Get: APIEndpointAction{Handler: api10StateGet, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanViewResources)},
}

// swagger:operation GET /1.0/state server state_get
//
//	Get server state
//
//	Gets the current system state of the LXD server.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Current server state
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/ServerState"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func api10StateGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	if s.ServerClustered {
		return response.BadRequest(errors.New("The server state is not available on clustered servers"))
	}

	memberState, err := cluster.MemberState(r.Context(), d.State())
	if err != nil {
		return response.SmartError(err)
	}

	serverState := api.ServerState(*memberState)

	return response.SyncResponse(true, serverState)
}
