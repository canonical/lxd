package drivers

import (
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/lxd/storage/drivers/powerstoreclient"
	"github.com/canonical/lxd/lxd/storage/drivers/tokencache"
	"github.com/canonical/lxd/shared"
)

// powerStoreTokenCache stores shared PowerStore login sessions.
var powerStoreTokenCache = tokencache.New[powerstoreclient.LoginSession]("powerstore")

// newPowerStoreClient creates a new instance of the PowerStore HTTP API
// client.
func newPowerStoreClient(driver *powerstore) *powerstoreclient.Client {
	return &powerstoreclient.Client{
		Gateway:              driver.config["powerstore.gateway"],
		GatewaySkipTLSVerify: shared.IsFalse(driver.config["powerstore.gateway.verify"]),
		Username:             driver.config["powerstore.user.name"],
		Password:             driver.config["powerstore.user.password"],
		TokenCache:           powerStoreTokenCache,
	}
}

// client returns the PowerStore API client.
// A new client gets created if one does not exists.
func (d *powerstore) client() *powerstoreclient.Client {
	if d.apiClient == nil {
		d.apiClient = newPowerStoreClient(d)
	}

	return d.apiClient
}

// connector retrieves an initialized storage connector based on the configured
// PowerStore mode. The connector is cached in the driver struct.
func (d *powerstore) connector() (connectors.Connector, error) {
	if d.storageConnector == nil {
		mt, err := powerStoreSupportedModesAndTransports.Find(d.config["powerstore.mode"], d.config["powerstore.transport"])
		if err != nil {
			return nil, err
		}

		connector, err := connectors.NewConnector(mt.ConnectorType, d.state.OS.ServerUUID)
		if err != nil {
			return nil, err
		}

		d.storageConnector = connector
	}

	return d.storageConnector, nil
}
