package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/canonical/lxd/lxd/auth"
	clusterRequest "github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/network"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/version"
)

var networkAllocationsCmd = APIEndpoint{
	Path: "network-allocations",

	Get: APIEndpointAction{Handler: networkAllocationsGet, AccessHandler: allowAuthenticated},
}

// swagger:operation GET /1.0/network-allocations network-allocations network_allocations_get
//
//	Get the network allocations in use (`network`, `network-forward` and `load-balancer` and `instance`)
//
//	Returns a list of network allocations in use by a LXD deployment.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: all-projects
//	    description: Retrieve entities from all projects
//	    type: boolean
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
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
//	          type: array
//	          description: List of network allocations used by a consuming entity
//	          items:
//	            $ref: "#/definitions/NetworkAllocations"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkAllocationsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkProject(d.State().DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	allProjects := shared.IsTrue(request.QueryParam(r, "all-projects"))

	var projectNames []string
	err = d.db.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Figure out the projects to retrieve.
		if !allProjects {
			projectNames = []string{projectName}
		} else {
			// Get all project names if no specific project requested.
			projectNames, err = dbCluster.GetProjectNames(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed loading projects: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Helper function to get the CIDR address of an IP (/32 or /128 mask for ipv4 or ipv6 respectively).
	// Returns IP address in its canonical CIDR form and whether the network is using NAT for that IP family.
	ipToCIDR := func(addr string, netConf map[string]string) (string, bool, error) {
		ip := net.ParseIP(addr)
		if ip == nil {
			return "", false, fmt.Errorf("Invalid IP address %q", addr)
		}

		if ip.To4() != nil {
			return fmt.Sprintf("%s/32", ip.String()), shared.IsTrue(netConf["ipv4.nat"]), nil
		}

		return fmt.Sprintf("%s/128", ip.String()), shared.IsTrue(netConf["ipv6.nat"]), nil
	}

	result := make([]api.NetworkAllocations, 0)

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, entity.TypeNetwork)
	if err != nil {
		return response.SmartError(err)
	}

	// Then, get all the networks, their network forwards and their network load balancers.
	for _, projectName := range projectNames {
		var networkNames []string

		err := d.db.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error

			networkNames, err = tx.GetNetworks(ctx, projectName)

			return err
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading networks: %w", err))
		}

		// Get all the networks, their attached instances, their network forwards and their network load balancers.
		for _, networkName := range networkNames {
			if !userHasPermission(entity.TypeNetwork.URL(projectName, networkName)) {
				continue
			}

			n, err := network.LoadByName(d.State(), projectName, networkName)
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed loading network %q in project %q: %w", networkName, projectName, err))
			}

			netConf := n.Config()

			for _, keyPrefix := range []string{"ipv4", "ipv6"} {
				ipNet, _ := network.ParseIPCIDRToNet(netConf[fmt.Sprintf("%s.address", keyPrefix)])
				if ipNet == nil {
					continue
				}

				result = append(result, api.NetworkAllocations{
					Address: ipNet.String(),
					UsedBy:  api.NewURL().Path(version.APIVersion, "networks", networkName).Project(projectName).String(),
					Type:    "network",
					NAT:     shared.IsTrue(netConf[fmt.Sprintf("%s.nat", keyPrefix)]),
				})
			}

			leases, err := n.Leases(projectName, clusterRequest.ClientTypeNormal)
			if err != nil && !errors.Is(network.ErrNotImplemented, err) {
				return response.SmartError(fmt.Errorf("Failed getting leases for network %q in project %q: %w", networkName, projectName, err))
			}

			for _, lease := range leases {
				if shared.ValueInSlice(lease.Type, []string{"static", "dynamic"}) {
					cidrAddr, nat, err := ipToCIDR(lease.Address, netConf)
					if err != nil {
						return response.SmartError(err)
					}

					result = append(result, api.NetworkAllocations{
						Address: cidrAddr,
						UsedBy:  api.NewURL().Path(version.APIVersion, "instances", lease.Hostname).Project(projectName).String(),
						Type:    "instance",
						Hwaddr:  lease.Hwaddr,
						NAT:     nat,
					})
				}
			}

			var forwards map[int64]*api.NetworkForward

			err = d.db.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				forwards, err = tx.GetNetworkForwards(ctx, n.ID(), false)

				return err
			})
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed getting forwards for network %q in project %q: %w", networkName, projectName, err))
			}

			for _, forward := range forwards {
				cidrAddr, _, err := ipToCIDR(forward.ListenAddress, netConf)
				if err != nil {
					return response.SmartError(err)
				}

				result = append(
					result,
					api.NetworkAllocations{
						Address: cidrAddr,
						UsedBy:  api.NewURL().Path(version.APIVersion, "networks", networkName, "forwards", forward.ListenAddress).Project(projectName).String(),
						Type:    "network-forward",
						NAT:     false, // Network forwards are ingress and so aren't affected by SNAT.
					},
				)
			}

			var loadBalancers map[int64]*api.NetworkLoadBalancer

			err = d.db.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				loadBalancers, err = tx.GetNetworkLoadBalancers(ctx, n.ID(), false)

				return err
			})
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed getting load-balancers for network %q in project %q: %w", networkName, projectName, err))
			}

			for _, loadBalancer := range loadBalancers {
				cidrAddr, _, err := ipToCIDR(loadBalancer.ListenAddress, netConf)
				if err != nil {
					return response.SmartError(err)
				}

				result = append(
					result,
					api.NetworkAllocations{
						Address: cidrAddr,
						UsedBy:  api.NewURL().Path(version.APIVersion, "networks", networkName, "load-balancers", loadBalancer.ListenAddress).Project(projectName).String(),
						Type:    "network-load-balancer",
						NAT:     false, // Network load-balancers are ingress and so aren't affected by SNAT.
					},
				)
			}
		}
	}

	return response.SyncResponse(true, result)
}
