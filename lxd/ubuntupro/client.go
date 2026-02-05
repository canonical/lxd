package ubuntupro

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

// Client is our wrapper for the Ubuntu Pro CLI.
type Client struct {
	pro pro
}

// pro is an internal interface that is used for mocking calls to the pro CLI.
type pro interface {
	isHostAttached() (bool, error)
}

// proCLI calls the actual Ubuntu Pro CLI to implement the interface.
type proCLI struct{}

// isHostAttached returns true if the host is attached to a pro subscription.
func (proCLI) isHostAttached() (bool, error) {
	// Run pro status command.
	response, err := shared.RunCommand("pro", "status", "--format", "json")
	if err != nil {
		return false, fmt.Errorf("Ubuntu Pro client command unsuccessful: %w", err)
	}

	// Parse response.
	var statusResponse struct {
		Attached *bool `json:"attached"`
	}

	err = json.Unmarshal([]byte(response), &statusResponse)
	if err != nil {
		return false, fmt.Errorf("Received unexpected response from Ubuntu Pro client: %w", err)
	}

	if statusResponse.Attached == nil {
		return false, errors.New("Received unexpected response from Ubuntu Pro client: missing attached field")
	}

	return *statusResponse.Attached, nil
}

// New returns a new Client that checks (once) if the host is attached to a pro subscription.
// If the host is not Ubuntu, it returns nil.
func New(osName string) *Client {
	if osName != "Ubuntu" {
		return nil
	}

	s := &Client{}
	s.init(proCLI{})
	return s
}

func (s *Client) init(proShim pro) {
	s.pro = proShim

	// Determine if the host is attached to Ubuntu Pro and update the user agent accordingly.
	isAttached, err := s.pro.isHostAttached()
	if err != nil {
		logger.Debug("Failed to check if host is Ubuntu Pro attached", logger.Ctx{"err": err})
	} else if isAttached {
		err = version.UserAgentFeatures([]string{"pro"})
		if err != nil {
			logger.Warn("Failed to configure LXD user agent for Ubuntu Pro", logger.Ctx{"err": err})
		}
	}
}
