package lxd

import (
	"fmt"
	"net"
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetNetworkLoadBalancerAddresses returns a list of network load balancer listen addresses.
func (r *ProtocolLXD) GetNetworkLoadBalancerAddresses(networkName string) ([]string, error) {
	err := r.CheckExtension("network_load_balancer")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("networks", networkName, "load-balancers")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetNetworkLoadBalancers returns a list of Network load balancer structs.
func (r *ProtocolLXD) GetNetworkLoadBalancers(networkName string) ([]api.NetworkLoadBalancer, error) {
	err := r.CheckExtension("network_load_balancer")
	if err != nil {
		return nil, err
	}

	loadBalancers := []api.NetworkLoadBalancer{}

	// Fetch the raw value.
	u := api.NewURL().Path("networks", networkName, "load-balancers").WithQuery("recursion", "1")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &loadBalancers)
	if err != nil {
		return nil, err
	}

	return loadBalancers, nil
}

// GetNetworkLoadBalancer returns a Network load balancer entry for the provided network and listen address.
func (r *ProtocolLXD) GetNetworkLoadBalancer(networkName string, listenAddress string) (*api.NetworkLoadBalancer, string, error) {
	err := r.CheckExtension("network_load_balancer")
	if err != nil {
		return nil, "", err
	}

	loadBalancer := api.NetworkLoadBalancer{}

	// Fetch the raw value.
	u := api.NewURL().Path("networks", networkName, "load-balancers", listenAddress)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &loadBalancer)
	if err != nil {
		return nil, "", err
	}

	return &loadBalancer, etag, nil
}

// CreateNetworkLoadBalancer defines a new network load balancer using the provided struct.
func (r *ProtocolLXD) CreateNetworkLoadBalancer(networkName string, loadBalancer api.NetworkLoadBalancersPost) (Operation, error) {
	err := r.CheckExtension("network_load_balancer")
	if err != nil {
		return nil, err
	}

	listenAddressIP := net.ParseIP(loadBalancer.ListenAddress)
	if listenAddressIP == nil {
		return nil, fmt.Errorf("Invalid network load balancer listen address: %s", loadBalancer.ListenAddress)
	}

	if listenAddressIP.IsUnspecified() {
		err := r.CheckExtension("network_allocate_external_ips")
		if err != nil {
			return nil, err
		}
	}

	u := api.NewURL().Path("networks", networkName, "load-balancers")

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil || r.isClusterOperationNotification() {
		// Use a synchronous request when the server lacks async endpoint support
		// or when handling a cluster operation notification.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPost, u.String(), loadBalancer, "")
	} else {
		op, _, err = r.queryOperation(http.MethodPost, u.String(), loadBalancer, "", true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateNetworkLoadBalancer updates the network load balancer to match the provided struct.
func (r *ProtocolLXD) UpdateNetworkLoadBalancer(networkName string, listenAddress string, loadBalancer api.NetworkLoadBalancerPut, ETag string) (Operation, error) {
	err := r.CheckExtension("network_load_balancer")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("networks", networkName, "load-balancers", listenAddress)

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil || r.isClusterOperationNotification() {
		// Use a synchronous request when the server lacks async endpoint support
		// or when handling a cluster operation notification.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPut, u.String(), loadBalancer, ETag)
	} else {
		op, _, err = r.queryOperation(http.MethodPut, u.String(), loadBalancer, ETag, true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteNetworkLoadBalancer deletes an existing network load balancer.
func (r *ProtocolLXD) DeleteNetworkLoadBalancer(networkName string, listenAddress string) (Operation, error) {
	err := r.CheckExtension("network_load_balancer")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("networks", networkName, "load-balancers", listenAddress)

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil || r.isClusterOperationNotification() {
		// Use a synchronous request when the server lacks async endpoint support
		// or when handling a cluster operation notification.
		op = noopOperation{}
		_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	} else {
		op, _, err = r.queryOperation(http.MethodDelete, u.String(), nil, "", true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// CreateNetworkLoadBalancerPool defines a new network load balancer pool using the provided struct.
func (r *ProtocolLXD) CreateNetworkLoadBalancerPool(networkName string, pool api.NetworkLoadBalancerPoolsPost) error {
	err := r.CheckExtension("network_load_balancer_pool")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "load-balancer-pools")
	_, _, err = r.query(http.MethodPost, u.String(), pool, "")
	if err != nil {
		return err
	}

	return nil
}

// GetNetworkLoadBalancerPools returns all network load balancer pool for provided network name.
func (r *ProtocolLXD) GetNetworkLoadBalancerPools(networkName string) ([]api.NetworkLoadBalancerPool, error) {
	err := r.CheckExtension("network_load_balancer_pool")
	if err != nil {
		return nil, err
	}

	var loadBalancerPools []api.NetworkLoadBalancerPool

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "load-balancer-pools")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &loadBalancerPools)
	if err != nil {
		return nil, err
	}

	return loadBalancerPools, nil
}

// GetNetworkLoadBalancerPool returns a network load balancer pool by name in the provided network.
func (r *ProtocolLXD) GetNetworkLoadBalancerPool(networkName string, poolName string) (*api.NetworkLoadBalancerPool, string, error) {
	err := r.CheckExtension("network_load_balancer_pool")
	if err != nil {
		return nil, "", err
	}

	var loadBalancerPool api.NetworkLoadBalancerPool

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "load-balancer-pools", poolName)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &loadBalancerPool)
	if err != nil {
		return nil, "", err
	}

	return &loadBalancerPool, etag, nil
}

// GetNetworkLoadBalancerPoolState returns the state of a network load balancer pool by name in the provided network.
func (r *ProtocolLXD) GetNetworkLoadBalancerPoolState(networkName string, poolName string) (*api.NetworkLoadBalancerPoolState, error) {
	err := r.CheckExtension("network_load_balancer_pool")
	if err != nil {
		return nil, err
	}

	var loadBalancerPoolState api.NetworkLoadBalancerPoolState

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "load-balancer-pools", poolName, "state")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &loadBalancerPoolState)
	if err != nil {
		return nil, err
	}

	return &loadBalancerPoolState, nil
}

// DeleteNetworkLoadBalancerPool deletes an existing network load balancer pool.
func (r *ProtocolLXD) DeleteNetworkLoadBalancerPool(networkName string, poolName string) error {
	err := r.CheckExtension("network_load_balancer_pool")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "load-balancer-pools", poolName)
	_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	return err
}

// UpdateNetworkLoadBalancerPool updates the network load balancer pool to match the provided struct.
func (r *ProtocolLXD) UpdateNetworkLoadBalancerPool(networkName string, poolName string, pool api.NetworkLoadBalancerPoolPut, ETag string) (Operation, error) {
	err := r.CheckExtension("network_load_balancer_pool")
	if err != nil {
		return nil, err
	}

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "load-balancer-pools", poolName)
	op, _, err := r.queryOperation(http.MethodPut, u.String(), pool, ETag, true)
	return op, err
}
