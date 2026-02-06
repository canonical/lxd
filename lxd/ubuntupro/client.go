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

// isHostAttached returns true if the host is attached to a pro subscription with a valid contract.
func (proCLI) isHostAttached() (bool, error) {
	// Run pro status command.
	response, err := shared.RunCommand("pro", "api", "u.pro.status.is_attached.v1")
	if err != nil {
		return false, fmt.Errorf("Ubuntu Pro client command unsuccessful: %w", err)
	}

	return parseProAPIIsAttachedV1(response)
}

// proAPIIsAttachedV1 represents the expected format of calls to `pro api u.pro.status.is_attached.v1`.
type proAPIIsAttachedV1 struct {
	Data *struct {
		Attributes *struct {
			Attached *bool `json:"is_attached_and_contract_valid"`
		} `json:"attributes"`
	} `json:"data"`
}

func parseProAPIIsAttachedV1(response string) (bool, error) {
	var statusResponse proAPIIsAttachedV1

	err := json.Unmarshal([]byte(response), &statusResponse)
	if err != nil {
		return false, fmt.Errorf("Received unexpected response from Ubuntu Pro client: %w", err)
	}

	if statusResponse.Data == nil || statusResponse.Data.Attributes == nil || statusResponse.Data.Attributes.Attached == nil {
		return false, errors.New("Received unexpected response from Ubuntu Pro client: missing attached field")
	}

	return *statusResponse.Data.Attributes.Attached, nil
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
