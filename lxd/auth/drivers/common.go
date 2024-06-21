//go:build linux && cgo && !agent

package drivers

import (
	"fmt"

	"github.com/canonical/lxd/shared/logger"
)

type commonAuthorizer struct {
	driverName string
	logger     logger.Logger
}

func (c *commonAuthorizer) init(driverName string, l logger.Logger) error {
	if l == nil {
		return fmt.Errorf("Cannot initialise authorizer: nil logger provided")
	}

	l = l.AddContext(logger.Ctx{"driver": driverName})

	c.driverName = driverName
	c.logger = l
	return nil
}

// Driver returns the driver name.
func (c *commonAuthorizer) Driver() string {
	return c.driverName
}
