//go:build linux && cgo && !agent

package drivers

import (
	"context"
	"errors"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/request/security"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
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

// emitAuthzFail builds and sends an authz_fail security event for a denial.
// No-op when the driver was loaded without a send callback (e.g. unit tests).
//
// can_edit denials on server and storage_pool are intentionally suppressed:
// GET handlers probe these to decide whether to render sensitive config and
// those probes are expected denials, not real authorization failures.
func (c *commonAuthorizer) emitAuthzFail(ctx context.Context, entityURL *api.URL, entitlement auth.Entitlement, entityType entity.Type) {
	if c.sendSecurity == nil {
		return
	}

	if entitlement == auth.EntitlementCanEdit && (entityType == entity.TypeServer || entityType == entity.TypeStoragePool) {
		return
	}

	evt := security.AuthzFail.WithSuffix(string(entitlement), entityURL.String()).UserEvent(ctx, security.LevelWarning, "Authorization denied")
	c.sendSecurity(evt)
}
