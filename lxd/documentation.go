package main

import (
	"embed"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
)

var documentationCmd = APIEndpoint{
	Path: "documentation",

	Get: APIEndpointAction{Handler: documentationGet, AllowUntrusted: true},
}

//go:embed gendocs/docs.yaml
var generatedDoc embed.FS

// swagger:operation GET /1.0/documentation documentation documentation_get
//
//	Get the documentation
//
//	Returns the generated LXD documentation in YAML format.
//
//	---
//	produces:
//	  - text/plain
//	responses:
//	  "200":
//	    description: API endpoints
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
//	          type: string
//	          description: The generated documentation
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func documentationGet(d *Daemon, r *http.Request) response.Response {
	file, err := generatedDoc.ReadFile("gendocs/docs.yaml")
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponsePlain(true, false, string(file))
}
