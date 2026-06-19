package main

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/lxd/network"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
)

// mockNetwork satisfies network.Network for unit tests.
type mockNetwork struct {
	network.Network
	managed     bool
	info        network.Info
	networkType string
}

func (s *mockNetwork) IsManaged() bool {
	return s.managed
}

func (s *mockNetwork) Info() network.Info {
	return s.info
}

func (s *mockNetwork) Type() string {
	return s.networkType
}

// supportedNetwork is the ideal supported network.
var supportedNetwork = &mockNetwork{
	managed: true,
	info: network.Info{
		LoadBalancers: true,
	},
	networkType: "ovn",
}

// supportedProject is the ideal supported project.
var supportedProject = networkDetails{
	requestProject: api.Project{
		Config: map[string]string{
			"features.networks": "true",
		},
	},
	networkName: "default",
}

func Test_networkLoadBalancerPoolCheckAccess(t *testing.T) {
	tests := []struct {
		TestName string
		network  network.Network
		details  networkDetails
		response response.Response
	}{
		{
			TestName: "Check pool creation is allowed if project has network features enabled and supports load balancers",
			network:  supportedNetwork,
			details:  supportedProject,
			response: nil,
		},
		{
			TestName: "Check pool creation is forbidden if project has network features enabled but does not support load balancers",
			network: &mockNetwork{
				managed: true,
				info: network.Info{
					LoadBalancers: false,
				},
				networkType: "foo",
			},
			details:  supportedProject,
			response: response.BadRequest(errors.New(`Network driver "foo" does not support load balancers`)),
		},
		{
			TestName: "Check pool creation is forbidden if project does not have network features enabled but does support load balancers",
			network:  supportedNetwork,
			details: networkDetails{
				requestProject: api.Project{
					Name: "default",
					Config: map[string]string{
						"features.networks": "false",
					},
				},
				networkName: "default",
			},
			response: response.BadRequest(errors.New(`Project "default" requires features.networks=true`)),
		},
		{
			TestName: "Check pool creation is forbidden if project does not have network features specifically set but does support load balancers",
			network:  supportedNetwork,
			details: networkDetails{
				requestProject: api.Project{
					Name:   "default",
					Config: map[string]string{},
				},
				networkName: "default",
			},
			response: response.BadRequest(errors.New(`Project "default" requires features.networks=true`)),
		},
		{
			TestName: "Check pool creation is forbidden if network is not in the list of allowed networks",
			network:  supportedNetwork,
			details: networkDetails{
				requestProject: api.Project{
					Config: map[string]string{
						"restricted":                 "true",
						"restricted.networks.access": "foo",
					},
				},
				networkName: "bar",
			},
			response: response.NotFound(api.NewStatusError(http.StatusNotFound, "Network not found")),
		},
	}

	for _, test := range tests {
		t.Run(test.TestName, func(t *testing.T) {
			resp := networkLoadBalancerPoolCheckAccess(test.network, test.details)
			assert.Equal(t, test.response, resp)
		})
	}
}
