package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/shared/api"
)

// CertificateAction represents a lifecycle event action for Certificates.
type CertificateAction string

// All supported lifecycle events for Certificates.
const (
	CertificateCreated = CertificateAction("created")
	CertificateDeleted = CertificateAction("deleted")
	CertificateUpdated = CertificateAction("updated")
)

// Event creates the lifecycle event for an action on a Certificate.
func (a CertificateAction) Event(fingerprint string, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("certificate-%s", a)

	u := fmt.Sprintf("/1.0/certificates/%s", url.PathEscape(fingerprint))

	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
