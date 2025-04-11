package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetNetworkPeerNames returns a list of network peer names.
func (r *ProtocolLXD) GetNetworkPeerNames(networkName string) ([]string, error) {
	err := r.CheckExtension("network_peer")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("networks", networkName, "peers")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetNetworkPeers returns a list of network peer structs.
func (r *ProtocolLXD) GetNetworkPeers(networkName string) ([]api.NetworkPeer, error) {
	err := r.CheckExtension("network_peer")
	if err != nil {
		return nil, err
	}

	peers := []api.NetworkPeer{}

	// Fetch the raw value.
	u := api.NewURL().Path("networks", networkName, "peers").WithQuery("recursion", "1")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &peers)
	if err != nil {
		return nil, err
	}

	return peers, nil
}

// GetNetworkPeer returns a network peer entry for the provided network and peer name.
func (r *ProtocolLXD) GetNetworkPeer(networkName string, peerName string) (*api.NetworkPeer, string, error) {
	err := r.CheckExtension("network_peer")
	if err != nil {
		return nil, "", err
	}

	peer := api.NetworkPeer{}

	// Fetch the raw value.
	u := api.NewURL().Path("networks", networkName, "peers", peerName)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &peer)
	if err != nil {
		return nil, "", err
	}

	return &peer, etag, nil
}

// CreateNetworkPeer defines a new network peer using the provided struct.
// Returns true if the peer connection has been mutually created. Returns false if peering has been only initiated.
func (r *ProtocolLXD) CreateNetworkPeer(networkName string, peer api.NetworkPeersPost) error {
	err := r.CheckExtension("network_peer")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "peers")
	_, _, err = r.query(http.MethodPost, u.String(), peer, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkPeer updates the network peer to match the provided struct.
func (r *ProtocolLXD) UpdateNetworkPeer(networkName string, peerName string, peer api.NetworkPeerPut, ETag string) error {
	err := r.CheckExtension("network_peer")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "peers", peerName)
	_, _, err = r.query(http.MethodPut, u.String(), peer, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkPeer deletes an existing network peer.
func (r *ProtocolLXD) DeleteNetworkPeer(networkName string, peerName string) error {
	err := r.CheckExtension("network_peer")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "peers", peerName)
	_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}
