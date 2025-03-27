package cluster

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

type entityTypeCommon struct{}

// allURLsQuery returns an empty string because there are no Server entities in the database.
func (e entityTypeCommon) allURLsQuery() string {
	return ""
}

func (e entityTypeCommon) urlsByProjectQuery() string {
	return ""
}

func (e entityTypeCommon) urlByIDQuery() string {
	return ""
}

// idFromURLQuery returns an empty string because there are no Server entities in the database.
func (e entityTypeCommon) idFromURLQuery() string {
	return ""
}

func (e entityTypeCommon) onDeleteTriggerSQL() (name string, sql string) {
	return "", ""
}

// runSelector is not implemented for entityTypeServer.
func (e entityTypeCommon) runSelector(_ context.Context, _ *sql.Tx, _ Selector) ([]int, error) {
	return nil, api.NewGenericStatusError(http.StatusBadRequest)
}
