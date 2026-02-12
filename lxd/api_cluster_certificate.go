package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/acme"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/warningtype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/lxd/warnings"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

var clusterCertificateCmd = APIEndpoint{
	Path:        "cluster/certificate",
	MetricsType: entity.TypeClusterMember,

	Put: APIEndpointAction{Handler: clusterCertificatePut, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

// swagger:operation PUT /1.0/cluster/certificate cluster clustering_update_cert
//
//	Update the certificate for the cluster
//
//	Replaces existing cluster certificate and reloads LXD on each cluster
//	member.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster certificate replace request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterCertificatePut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterCertificatePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := api.ClusterCertificatePut{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	certBytes := []byte(req.ClusterCertificate)
	keyBytes := []byte(req.ClusterCertificateKey)

	certBlock, _ := pem.Decode(certBytes)
	if certBlock == nil {
		return response.BadRequest(fmt.Errorf("Certificate must be base64 encoded PEM certificate: %w", err))
	}

	keyBlock, _ := pem.Decode(keyBytes)
	if keyBlock == nil {
		return response.BadRequest(fmt.Errorf("Private key must be base64 encoded PEM key: %w", err))
	}

	err = updateClusterCertificate(r.Context(), s, d.gateway, r, req)
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterCertificateUpdated.Event("certificate", requestor, nil))

	return response.EmptySyncResponse
}

func updateClusterCertificate(ctx context.Context, s *state.State, gateway *cluster.Gateway, r *http.Request, req api.ClusterCertificatePut) error {
	revert := revert.New()
	defer revert.Fail()

	newClusterCertFilename := shared.VarPath(acme.ClusterCertFilename)

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return err
	}

	// First node forwards request to all other cluster nodes
	if r == nil || !requestor.IsClusterNotification() {
		var err error

		revert.Add(func() {
			_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.UpsertWarningLocalNode(ctx, "", "", -1, warningtype.UnableToUpdateClusterCertificate, err.Error())
			})
		})

		oldCertBytes, err := os.ReadFile(shared.VarPath("cluster.crt"))
		if err != nil {
			return err
		}

		keyBytes, err := os.ReadFile(shared.VarPath("cluster.key"))
		if err != nil {
			return err
		}

		oldReq := api.ClusterCertificatePut{
			ClusterCertificate:    string(oldCertBytes),
			ClusterCertificateKey: string(keyBytes),
		}

		// Get all members in cluster.
		var members []db.NodeInfo
		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			members, err = tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			return nil
		})
		if err != nil {
			return err
		}

		localClusterAddress := s.LocalConfig.ClusterAddress()

		revert.Add(func() {
			// If distributing the new certificate fails, store the certificate. This new file will
			// be considered when running the auto renewal again.
			err := os.WriteFile(newClusterCertFilename, []byte(req.ClusterCertificate), 0600)
			if err != nil {
				logger.Error("Failed storing new certificate", logger.Ctx{"err": err})
			}
		})

		newCertInfo, err := shared.KeyPairFromRaw([]byte(req.ClusterCertificate), []byte(req.ClusterCertificateKey))
		if err != nil {
			return err
		}

		var client lxd.InstanceServer

		for i := range members {
			member := members[i]

			if member.Address == localClusterAddress {
				continue
			}

			client, err = cluster.Connect(r.Context(), member.Address, s.Endpoints.NetworkCert(), s.ServerCert(), true)
			if err != nil {
				return err
			}

			err = client.UpdateClusterCertificate(req, "")
			if err != nil {
				return err
			}

			// When reverting the certificate, we need to connect to the cluster members using the
			// new certificate otherwise we'll get a bad certificate error.
			revert.Add(func() {
				client, err := cluster.Connect(r.Context(), member.Address, newCertInfo, s.ServerCert(), true)
				if err != nil {
					logger.Error("Failed connecting to cluster member", logger.Ctx{"address": member.Address, "err": err})
					return
				}

				err = client.UpdateClusterCertificate(oldReq, "")
				if err != nil {
					logger.Error("Failed updating cluster certificate on cluster member", logger.Ctx{"address": member.Address, "err": err})
				}
			})
		}
	}

	err = util.WriteCert(s.OS.VarDir, "cluster", []byte(req.ClusterCertificate), []byte(req.ClusterCertificateKey), nil)
	if err != nil {
		return err
	}

	if shared.PathExists(newClusterCertFilename) {
		err := os.Remove(newClusterCertFilename)
		if err != nil {
			return fmt.Errorf("Failed removing cluster certificate: %w", err)
		}
	}

	// Get the new cluster certificate struct
	cert, err := util.LoadClusterCert(s.OS.VarDir)
	if err != nil {
		return err
	}

	// Update the certificate on the network endpoint and gateway
	s.Endpoints.NetworkUpdateCert(cert)
	gateway.NetworkUpdateCert(cert)

	// Resolve warning of this type
	_ = warnings.ResolveWarningsByLocalNodeAndType(s.DB.Cluster, warningtype.UnableToUpdateClusterCertificate)

	revert.Success()

	return nil
}
