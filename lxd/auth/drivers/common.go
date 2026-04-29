//go:build linux && cgo && !agent

package drivers

import (
	"errors"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

type commonAuthorizer struct {
	driverName   string
	logger       logger.Logger
	sendSecurity func(*api.EventSecurity)
}

func (c *commonAuthorizer) init(driverName string, l logger.Logger, sendSecurity func(*api.EventSecurity)) error {
	if l == nil {
		return errors.New("Cannot initialise authorizer: nil logger provided")
	}

	l = l.AddContext(logger.Ctx{"driver": driverName})

	c.driverName = driverName
	c.logger = l
	c.sendSecurity = sendSecurity
	return nil
}

// Driver returns the driver name.
func (c *commonAuthorizer) Driver() string {
	return c.driverName
}
