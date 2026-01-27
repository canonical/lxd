package zone

import (
	"context"
	"strings"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
)

// NetworkZone represents a Network zone.
type NetworkZone interface {
	// Initialise.
	init(state *state.State, id int64, projectName string, zoneInfo *api.NetworkZone)

	// Info.
	ID() int64
	Project() string
	Info() *api.NetworkZone
	Etag() []any
	UsedBy(ctx context.Context) ([]string, error)
	Content(ctx context.Context) (*strings.Builder, error)
	SOA() (*strings.Builder, error)

	// Records.
	AddRecord(ctx context.Context, req api.NetworkZoneRecordsPost) error
	GetRecords(ctx context.Context) ([]api.NetworkZoneRecord, error)
	GetRecord(ctx context.Context, name string) (*api.NetworkZoneRecord, error)
	UpdateRecord(ctx context.Context, name string, req api.NetworkZoneRecordPut) error
	DeleteRecord(ctx context.Context, name string) error

	// Internal validation.
	validateName(name string) error
	validateConfig(config *api.NetworkZonePut) error

	// Modifications.
	Update(config *api.NetworkZonePut, clientType request.ClientType) error
	Delete(ctx context.Context) error
}
