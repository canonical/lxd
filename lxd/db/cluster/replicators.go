package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// ReplicatorRow represents a single row of the replicators table.
// db:model replicators
type ReplicatorRow struct {
	ID            int64        `db:"id"`
	Name          string       `db:"name"`
	ProjectID     int64        `db:"project_id"`
	Description   string       `db:"description"`
	LastRunDate   sql.NullTime `db:"last_run_date"`
	LastRunStatus string       `db:"last_run_status"`
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

// ToAPI converts the [Replicator] to an [api.Replicator].
func (r *Replicator) ToAPI(allConfigs map[int64]map[string]string) *api.Replicator {
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

	if r.Row.LastRunDate.Valid {
		replicator.LastRunAt = r.Row.LastRunDate.Time
	}

	if r.Row.LastRunStatus != "" {
		replicator.LastRunStatus = r.Row.LastRunStatus
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

// UpdateReplicatorLastRun updates the last_run_date and last_run_status fields of the replicator with the given ID.
func UpdateReplicatorLastRun(ctx context.Context, tx *sql.Tx, id int64, date time.Time, status string) error {
	_, err := tx.ExecContext(ctx, `UPDATE replicators SET last_run_date=?, last_run_status=? WHERE id=?`, date, status, id)
	return err
}

// UpdateReplicatorLastRunStatus updates only the last_run_status field of the replicator with the given ID.
func UpdateReplicatorLastRunStatus(ctx context.Context, tx *sql.Tx, id int64, status string) error {
	_, err := tx.ExecContext(ctx, `UPDATE replicators SET last_run_status=? WHERE id=?`, status, id)
	return err
}
