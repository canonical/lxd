package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// ReplicatorRunMode stores information about how a replicator run is initiated.
type ReplicatorRunMode string

const (
	// ReplicatorRunModeManual represents a user requested "manual" run.
	ReplicatorRunModeManual ReplicatorRunMode = "manual"

	// ReplicatorRunModeScheduled indicates that the replicator run was triggered by the replicator's cron schedule.
	ReplicatorRunModeScheduled ReplicatorRunMode = "scheduled"
)

const (
	replicatorRunModeCodeManual    int64 = 0
	replicatorRunModeCodeScheduled int64 = 1
)

// Scan implements [sql.Scanner] for [ReplicatorRunMode].
func (r *ReplicatorRunMode) Scan(value any) error {
	return query.ScanValue(value, r, false)
}

// ScanInteger implements [query.IntegerScanner] for [ReplicatorRunMode] which reduces duplication for [sql.Scanner] implementations.
func (r *ReplicatorRunMode) ScanInteger(in int64) error {
	switch in {
	case replicatorRunModeCodeManual:
		*r = ReplicatorRunModeManual
	case replicatorRunModeCodeScheduled:
		*r = ReplicatorRunModeScheduled
	default:
		return fmt.Errorf(`Unknown replicator mode code "%d"`, in)
	}

	return nil
}

// Value implements [driver.Valuer] for [ReplicatorRunMode].
func (r ReplicatorRunMode) Value() (driver.Value, error) {
	switch r {
	case ReplicatorRunModeManual:
		return replicatorRunModeCodeManual, nil
	case ReplicatorRunModeScheduled:
		return replicatorRunModeCodeScheduled, nil
	default:
		return nil, fmt.Errorf("Unknown replicator run mode %q", r)
	}
}

// ReplicatorRow represents a single row of the replicators table.
// db:model replicators
type ReplicatorRow struct {
	ID          int64  `db:"id"`
	Name        string `db:"name"`
	ProjectID   int64  `db:"project_id"`
	Description string `db:"description"`
}

// APIName implements [query.APINamer] for API friendly error messages.
func (ReplicatorRow) APIName() string {
	return "Replicator"
}

// Replicator contains [ReplicatorRow] with additional joins.
// db:model replicators
type Replicator struct {
	Row ReplicatorRow

	// db:join JOIN projects ON replicators.project_id = projects.id
	ProjectName string `db:"projects.name"`
}

// ReplicatorsConfigStore returns a [query.EntityConfigStore] for replicators.
func ReplicatorsConfigStore() *query.EntityConfigStore {
	return &query.EntityConfigStore{
		EntityTable:               "replicators",
		ConfigTable:               "replicators_config",
		ConfigTableEntityIDColumn: "replicator_id",
	}
}

// ReplicatorsStatusRow represents a row of the replicators_status table.
// db:model replicators_status
type ReplicatorsStatusRow struct {
	ID int64 `db:"id"`

	// db:omit update
	Mode   ReplicatorRunMode `db:"mode"`
	Status string            `db:"status"`

	// db:omit update
	StartedDate          time.Time    `db:"started_date"`
	FinishedDate         sql.NullTime `db:"finished_date"`
	SnapshotStartedDate  sql.NullTime `db:"snapshot_started_date"`
	SnapshotFinishedDate sql.NullTime `db:"snapshot_finished_date"`

	// db:omit update
	ReplicatorID int64 `db:"replicator_id"`
}

// APIName implements [query.APINamer] for API friendly error messages.
func (ReplicatorsStatusRow) APIName() string {
	return "Replicator status"
}

// APIPluralName implements [query.APIPluralNamer] for API friendly error messages (to avoid misspelling the plural of the [APIName] by appending an "s").
func (ReplicatorsStatusRow) APIPluralName() string {
	return "Replicator statuses"
}

// ToAPI converts the [Replicator] to an [api.Replicator].
func (r *Replicator) ToAPI(allConfigs map[int64]map[string]string, lastStatuses map[int64]ReplicatorsStatusRow) *api.Replicator {
	config := allConfigs[r.Row.ID]
	if config == nil {
		config = map[string]string{}
	}

	replicator := &api.Replicator{
		Name:          r.Row.Name,
		Description:   r.Row.Description,
		Project:       r.ProjectName,
		Config:        config,
		LastRunStatus: api.ReplicatorStatusPending,
	}

	lastStatus, ok := lastStatuses[r.Row.ID]
	if ok {
		replicator.LastRunAt = lastStatus.StartedDate
		replicator.LastRunStatus = lastStatus.Status
	}

	return replicator
}

// GetReplicator returns the replicator with the given name and project.
func GetReplicator(ctx context.Context, tx *sql.Tx, name string, projectName string) (*Replicator, error) {
	replicator, err := query.SelectOne[Replicator](ctx, tx, "WHERE replicators.name = ? AND projects.name = ?", name, projectName)
	if err != nil {
		return nil, fmt.Errorf("Failed loading replicator: %w", err)
	}

	return replicator, nil
}

// CreateReplicator adds a new replicator to the database.
func CreateReplicator(ctx context.Context, tx *sql.Tx, object ReplicatorRow) (int64, error) {
	return query.Create(ctx, tx, object)
}

// UpdateReplicator updates the replicator by its ID.
func UpdateReplicator(ctx context.Context, tx *sql.Tx, object ReplicatorRow) error {
	return query.UpdateByPrimaryKey(ctx, tx, object)
}

// RenameReplicator renames the replicator with the given name in the given project.
func RenameReplicator(ctx context.Context, tx *sql.Tx, name string, projectName string, newName string) error {
	replicator, err := GetReplicator(ctx, tx, name, projectName)
	if err != nil {
		return err
	}

	replicator.Row.Name = newName
	return query.UpdateByPrimaryKey(ctx, tx, replicator.Row)
}

// DeleteReplicator deletes the replicator with the given name and project.
func DeleteReplicator(ctx context.Context, tx *sql.Tx, name string, projectName string) error {
	return query.DeleteOne[ReplicatorRow, *ReplicatorRow](ctx, tx, "WHERE replicators.name = ? AND replicators.project_id = (SELECT id FROM projects WHERE name = ?)", name, projectName)
}

// GetReplicatorsAndURLs returns all replicators that pass the given filter, along with their entity URLs.
func GetReplicatorsAndURLs(ctx context.Context, tx *sql.Tx, projectName *string, filter func(replicator Replicator) bool) ([]Replicator, []string, error) {
	var args []any
	var b strings.Builder
	if projectName == nil {
		b.WriteString("ORDER BY projects.name, ")
	} else {
		b.WriteString("WHERE projects.name = ? ORDER BY ")
		args = append(args, *projectName)
	}

	b.WriteString("replicators.name")
	clause := b.String()

	var replicators []Replicator
	var replicatorURLs []string
	err := query.SelectFunc[Replicator](ctx, tx, clause, func(replicator Replicator) error {
		if filter != nil && !filter(replicator) {
			return nil
		}

		u := entity.ReplicatorURL(replicator.ProjectName, replicator.Row.Name)
		replicators = append(replicators, replicator)
		replicatorURLs = append(replicatorURLs, u.String())
		return nil
	}, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed loading replicators: %w", err)
	}

	return replicators, replicatorURLs, nil
}

// CreateNewReplicatorStatus updates the last_run_date and last_run_status fields of the replicator with the given ID.
func CreateNewReplicatorStatus(ctx context.Context, tx *sql.Tx, replicatorID int64, date time.Time, status string, mode ReplicatorRunMode) error {
	_, err := query.Create(ctx, tx, ReplicatorsStatusRow{
		Mode:         mode,
		Status:       status,
		StartedDate:  date,
		ReplicatorID: replicatorID,
	})
	return err
}

// FinalizeReplicatorStatus updates only the last_run_status field of the replicator with the given ID.
func FinalizeReplicatorStatus(ctx context.Context, tx *sql.Tx, replicatorID int64, status string, date time.Time) error {
	var b strings.Builder
	args := []any{status}
	b.WriteString("UPDATE replicators_status SET status = ?")
	if status == api.ReplicatorStatusCompleted {
		args = append(args, date.UTC())
		b.WriteString(", finished_date = ?")
	}

	args = append(args, replicatorID)
	b.WriteString(" WHERE id = (SELECT MAX(id) FROM replicators_status WHERE replicator_id = ?)")
	_, err := tx.ExecContext(ctx, b.String(), args...)

	if err != nil {
		return fmt.Errorf("Failed finalizing replicator run status: %w", err)
	}

	return err
}

// GetLastReplicatorStatuses gets the most recent [ReplicatorsStatusRow] for all [Replicator] entries in the given project,
// or for all projects if no project name is provided.
func GetLastReplicatorStatuses(ctx context.Context, tx *sql.Tx, projectName *string) (map[int64]ReplicatorsStatusRow, error) {
	var b strings.Builder
	r := ReplicatorsStatusRow{}
	cols := r.SelectColumns()
	idx := slices.Index(cols, "replicators_status.id")
	cols[idx] = "MAX(replicators_status.id)"
	b.WriteString(`SELECT `)
	b.WriteString(cols[0])
	for _, col := range cols[1:] {
		b.WriteString(", ")
		b.WriteString(col)
	}

	b.WriteString(" FROM ")
	b.WriteString(r.TableName())

	var args []any
	if projectName != nil {
		b.WriteString(" JOIN replicators ON ")
		b.WriteString(r.TableName())
		b.WriteString(".replicator_id = replicators.id JOIN projects ON replicators.project_id = projects.id WHERE projects.name = ?")
		args = append(args, *projectName)
	}

	b.WriteString(" GROUP BY replicators_status.replicator_id")

	result := make(map[int64]ReplicatorsStatusRow)
	err := query.Scan(ctx, tx, b.String(), func(scan func(dest ...any) error) error {
		status := &ReplicatorsStatusRow{}
		err := scan(status.ScanArgs()...)
		if err != nil {
			return fmt.Errorf("Failed scanning replicator status row: %w", err)
		}

		result[status.ReplicatorID] = *status
		return nil
	}, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed querying for replicator statuses: %w", err)
	}

	return result, nil
}

// GetLastReplicatorStatus gets the most recent [ReplicatorsStatusRow] for the [Replicator] with the given ID.
func GetLastReplicatorStatus(ctx context.Context, tx *sql.Tx, replicatorID int64) (*ReplicatorsStatusRow, error) {
	return query.SelectOne[ReplicatorsStatusRow](ctx, tx, "WHERE replicators_status.replicator_id = ? ORDER BY replicators_status.id DESC LIMIT 1", replicatorID)
}
