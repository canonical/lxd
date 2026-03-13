package cluster

// Certificate represents a row of the certificates table.
// db:model certificates
type Certificate struct {
	ID          int64  `db:"id"`
	Certificate string `db:"certificate"`
}
