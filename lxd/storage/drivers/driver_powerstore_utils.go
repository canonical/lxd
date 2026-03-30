package drivers

import (
	"github.com/canonical/lxd/lxd/storage/connectors"
)

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
