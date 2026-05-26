# `dbgen`

`dbgen` inspects Go struct definitions for expected comments and tags.
If it finds any, it generates functions for the type such that it implements the interfaces defined in [`generic.go`](../query/generic.go).
The type can then be used with the generic functions therein.

## Examples

### Simple table

For a simple table, just add a `db:model <table>` comment above the type and add a tag for each field representing the column name.

```sqlite
CREATE TABLE auth_groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    UNIQUE (name)
);
```
```go
package main

// AuthGroupsRow represents a single row of the auth_groups table.
// db:model auth_groups
type AuthGroupsRow struct {
	ID          int64  `db:"id"`
	Name        string `db:"name"`
	Description string `db:"description"`
}
```

In this example `dbgen` assumes that the `id` column is the primary key.

### Composite primary keys

Some tables don't have an auto-incrementing primary key. These are defined `WITHOUT ROWID`.
For these tables we can define the primary key as some unique column combination.
To specify the primary keys, add a `// db:primary` comment above those fields.

```sqlite
CREATE TABLE networks_load_balancer_pools_instances (
	network_load_balancer_pool_id INTEGER NOT NULL,
	instance_id INTEGER NOT NULL,
	target_port INTEGER NOT NULL,
	PRIMARY KEY (network_load_balancer_pool_id, instance_id),
	FOREIGN KEY (network_load_balancer_pool_id) REFERENCES networks_load_balancer_pools (id) ON DELETE CASCADE,
	FOREIGN KEY (instance_id) REFERENCES instances (id) ON DELETE CASCADE
) WITHOUT ROWID;
```
```go
package main

// NetworksLoadBalancerPoolsInstancesRow represents a row of the networks_load_balancer_pools_instances table.
// db:model networks_load_balancer_pools_instances
type NetworksLoadBalancerPoolsInstancesRow struct {
	// db:primary
	NetworkLoadBalancerPoolID int64 `db:"network_load_balancer_pool_id"`
	// db:primary
	InstanceID int64 `db:"instance_id"`
	TargetPort int64 `db:"target_port"`
}
```

### Joins

If a Go type is modelled via `dbgen` to represent a single row of a single table, then the interface implementations that
`dbgen` generates can be used to perform CRUD operations on that table. Structs like this can be referenced from other structs
to perform more complex joins, but these secondary structs are read-only.

To reference another struct, add it to the wrapper struct as the first field, with the field name `Row`.
To perform a join, add a `// db:join` comment above the field that requires a join.

```sqlite
CREATE TABLE "projects" (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    UNIQUE (name)
);
CREATE TABLE placement_groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    project_id INTEGER NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    UNIQUE (project_id, name)
);
```

```go
package main

// PlacementGroupsRow represents a single row of the placement_groups table.
// db:model placement_groups
type PlacementGroupsRow struct {
	ID          int64  `db:"id"`
	Name        string `db:"name"`
	Description string `db:"description"`
	ProjectID   int64  `db:"project_id"`
}

// PlacementGroup contains [PlacementGroupsRow] with additional joins.
// db:model placement_groups
type PlacementGroup struct {
	Row PlacementGroupsRow

	// db:join JOIN projects ON placement_groups.project_id = projects.id
	ProjectName string `db:"projects.name"`
}
```
