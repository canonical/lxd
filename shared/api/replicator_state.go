package api

const (
	// ReplicatorStatusPending represents a replicator that has never been run.
	ReplicatorStatusPending = "Pending"

	// ReplicatorStatusRunning represents a replicator that is currently running.
	ReplicatorStatusRunning = "Running"

	// ReplicatorStatusCompleted represents a successfully completed replicator run.
	ReplicatorStatusCompleted = "Completed"

	// ReplicatorStatusFailed represents a failed replicator run.
	ReplicatorStatusFailed = "Failed"
)

// ReplicatorState represents the state of a replicator job.
//
// swagger:model
//
// API extension: replicators.
type ReplicatorState struct {
	// Status of the replicator job.
	// Example: Pending
	Status string `json:"status" yaml:"status"`
}

// ReplicatorStatePut represents the fields available to change the state of a replicator.
//
// swagger:model
//
// API extension: replicators.
type ReplicatorStatePut struct {
	// Action to perform on the replicator (start, restore).
	// Example: start
	Action string `json:"action" yaml:"action"`
}
