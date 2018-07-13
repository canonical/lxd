package cluster

import (
	"fmt"
	"net/http"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/pkg/errors"
)

// NotifyUpgradeCompleted sends a notification to all other nodes in the
// cluster that any possible pending database update has been applied, and any
// nodes which was waiting for this node to be upgraded should re-check if it's
// okay to move forward.
func NotifyUpgradeCompleted(state *state.State, cert *shared.CertInfo) error {
	notifier, err := NewNotifier(state, cert, NotifyAll)
	if err != nil {
		return err
	}
	return notifier(func(client lxd.ContainerServer) error {
		info, err := client.GetConnectionInfo()
		if err != nil {
			return errors.Wrap(err, "failed to get connection info")
		}

		url := fmt.Sprintf("%s%s", info.Addresses[0], databaseEndpoint)
		request, err := http.NewRequest("PATCH", url, nil)
		if err != nil {
			return errors.Wrap(err, "failed to create database notify upgrade request")
		}

		httpClient, err := client.GetHTTPClient()
		if err != nil {
			return errors.Wrap(err, "failed to get HTTP client")
		}

		response, err := httpClient.Do(request)
		if err != nil {
			return errors.Wrap(err, "failed to notify node about completed upgrade")
		}

		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("database upgrade notification failed: %s", response.Status)
		}

		return nil
	})
}
