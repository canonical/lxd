package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
)

// NetworksLoadBalancerPoolRow represents a single row of the networks_load_balancer_pools table.
// db:model networks_load_balancer_pools
type NetworksLoadBalancerPoolRow struct {
	ID          int64  `db:"id"`
	NetworkID   int64  `db:"network_id"`
	Name        string `db:"name"`
	Description string `db:"description"`
}

// APIName implements [query.APINamer] for API friendly error messages.
func (NetworksLoadBalancerPoolRow) APIName() string {
	return "Network load balancer pool"
}

// NetworksLoadBalancerPool represents a single row of the networks_load_balancer_pools table joined with the projects and networks tables.
// db:model networks_load_balancer_pools
type NetworksLoadBalancerPool struct {
	Row NetworksLoadBalancerPoolRow

	// db:join JOIN networks ON networks.id = networks_load_balancer_pools.network_id
	NetworkName string `db:"networks.name"`
	// db:join JOIN projects ON projects.id = networks.project_id
	ProjectID   int64  `db:"projects.id"`
	ProjectName string `db:"projects.name"`
}

// NetworksLoadBalancerPoolInstanceRow represents a single row of the networks_load_balancer_pools_instances table.
// db:model networks_load_balancer_pools_instances
type NetworksLoadBalancerPoolInstanceRow struct {
	ID         int64 `db:"id"`
	PoolID     int64 `db:"network_load_balancer_pool_id"`
	InstanceID int64 `db:"instance_id"`
	TargetPort int64 `db:"target_port"`
}

// APIName implements [query.APINamer] for API friendly error messages.
func (NetworksLoadBalancerPoolInstanceRow) APIName() string {
	return "Network load balancer pool instance"
}

// NetworksLoadBalancerPoolInstance represents a single row of the networks_load_balancer_pools_instances table joined with the instances table.
// db:model networks_load_balancer_pools_instances
type NetworksLoadBalancerPoolInstance struct {
	Row NetworksLoadBalancerPoolInstanceRow

	// db:join JOIN instances ON instances.id = networks_load_balancer_pools_instances.instance_id
	InstanceName string `db:"instances.name"`
}

// ToAPI converts the [NetworksLoadBalancerPool] to an [api.NetworkLoadBalancerPool].
func (p *NetworksLoadBalancerPool) ToAPI(allConfigs map[int64]map[string]string, allInstances map[int64][]NetworksLoadBalancerPoolInstance) (*api.NetworkLoadBalancerPool, error) {
	config := allConfigs[p.Row.ID]
	if config == nil {
		config = map[string]string{}
	}

	instances := allInstances[p.Row.ID]
	if instances == nil {
		instances = make([]NetworksLoadBalancerPoolInstance, 0)
	}

	apiInstances := make([]api.NetworkLoadBalancerPoolInstance, 0, len(instances))
	for _, instance := range instances {
		apiInstances = append(apiInstances, *instance.ToAPI())
	}

	return &api.NetworkLoadBalancerPool{
		Name:        p.Row.Name,
		Description: p.Row.Description,
		Config:      config,
		Instances:   apiInstances,
	}, nil
}

// ToAPI converts the [NetworksLoadBalancerPoolInstance] to an [api.NetworkLoadBalancerPoolInstance], querying for extra data as necessary.
func (p *NetworksLoadBalancerPoolInstance) ToAPI() *api.NetworkLoadBalancerPoolInstance {
	// Unset the target port in case it's 0 (not defined).
	targetPort := strconv.Itoa(int(p.Row.TargetPort))
	if targetPort == "0" {
		targetPort = ""
	}

	return &api.NetworkLoadBalancerPoolInstance{
		Name:       p.InstanceName,
		TargetPort: targetPort,
	}
}

// GetNetworksLoadBalancerPool gets a single NetworksLoadBalancerPool by name.
func GetNetworksLoadBalancerPool(ctx context.Context, tx *sql.Tx, networkID int64, poolName string) (*NetworksLoadBalancerPool, error) {
	pool, err := query.SelectOne[NetworksLoadBalancerPool](ctx, tx, "WHERE networks_load_balancer_pools.network_id = ? AND networks_load_balancer_pools.name = ?", networkID, poolName)
	if err != nil {
		return nil, err
	}

	return pool, nil
}

// GetNetworksLoadBalancerPoolRowByID gets a single NetworksLoadBalancerPoolRow by ID.
func GetNetworksLoadBalancerPoolRowByID(ctx context.Context, tx *sql.Tx, poolID int64) (*NetworksLoadBalancerPoolRow, error) {
	return query.SelectOne[NetworksLoadBalancerPoolRow](ctx, tx, "WHERE networks_load_balancer_pools.id = ?", poolID)
}

// GetNetworksLoadBalancers returns the names of all load balancers in the given network,
// or only the load balancers for the pool with the given name if provided.
// The returned map contains the addresses of the load balancers referencing the pool keyed by pool name.
func GetNetworksLoadBalancers(ctx context.Context, tx *sql.Tx, networkID int64, poolName *string) (map[string][]string, error) {
	var b strings.Builder
	var args []any

	b.WriteString(`
		SELECT DISTINCT
			networks_load_balancers.listen_address,
			json_extract(value, "$.target_pool") AS pool_name
		FROM networks_load_balancers
		CROSS JOIN json_each(networks_load_balancers.ports)
			WHERE networks_load_balancers.network_id = ? 
			AND json_valid(networks_load_balancers.ports) 
	`)
	args = append(args, networkID)

	if poolName != nil {
		b.WriteString(`
			AND pool_name = ? 
		`)
		args = append(args, poolName)
	}

	allLoadBalancers := map[string][]string{}
	err := query.Scan(ctx, tx, b.String(), func(scan func(dest ...any) error) error {
		var listenAddress string
		var poolName string

		err := scan(&listenAddress, &poolName)
		if err != nil {
			return err
		}

		if allLoadBalancers[poolName] == nil {
			allLoadBalancers[poolName] = []string{}
		}

		allLoadBalancers[poolName] = append(allLoadBalancers[poolName], listenAddress)

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	return allLoadBalancers, nil
}

// UpdateNetworksLoadBalancerPool updates a single NetworksLoadBalancerPool, replacing its config with the given config.
func UpdateNetworksLoadBalancerPool(ctx context.Context, tx *sql.Tx, pool *NetworksLoadBalancerPoolRow, config map[string]string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM networks_load_balancer_pools_config WHERE network_load_balancer_pool_id=?", pool.ID)
	if err != nil {
		return err
	}

	err = CreateNetworksLoadBalancerPoolConfig(ctx, tx, pool.ID, config)
	if err != nil {
		return err
	}

	return query.UpdateByPrimaryKey(ctx, tx, pool)
}

// GetNetworksLoadBalancerPools queries for all networks load balancers pools and then applies the given filter to the result.
func GetNetworksLoadBalancerPools(ctx context.Context, tx *sql.Tx, networkID int64, filter func(pool NetworksLoadBalancerPool) bool) ([]NetworksLoadBalancerPool, error) {
	var b strings.Builder
	b.WriteString("WHERE network_id = ? ORDER BY ")

	b.WriteString("networks_load_balancer_pools.name")
	clause := b.String()

	var loadBalancerPools []NetworksLoadBalancerPool
	err := query.SelectFunc[NetworksLoadBalancerPool](ctx, tx, clause, func(pool NetworksLoadBalancerPool) error {
		if filter != nil && !filter(pool) {
			return nil
		}

		loadBalancerPools = append(loadBalancerPools, pool)
		return nil
	}, networkID)
	if err != nil {
		return nil, err
	}

	return loadBalancerPools, nil
}

// DeleteNetworksLoadBalancerPool deletes the networks load balancer pool with the given name and network ID.
func DeleteNetworksLoadBalancerPool(ctx context.Context, tx *sql.Tx, networkID int64, poolName string) error {
	return query.DeleteOne[NetworksLoadBalancerPoolRow](ctx, tx, "WHERE network_id = ? AND name = ?", networkID, poolName)
}

// CreateNetworksLoadBalancerPoolConfig creates config for a new networks load balancer pool with the given ID.
func CreateNetworksLoadBalancerPoolConfig(ctx context.Context, tx *sql.Tx, poolID int64, config map[string]string) error {
	return createEntityConfig(ctx, tx, "networks_load_balancer_pools_config", "network_load_balancer_pool_id", poolID, config)
}

// GetNetworksLoadBalancerPoolConfig returns the config for all network load balancer pools,
// or only the config for the pool with the given ID if provided.
func GetNetworksLoadBalancerPoolConfig(ctx context.Context, tx *sql.Tx, poolID *int64) (map[int64]map[string]string, error) {
	var q string
	var args []any

	if poolID != nil {
		q = `SELECT network_load_balancer_pool_id, key, value FROM networks_load_balancer_pools_config WHERE network_load_balancer_pool_id=?`
		args = []any{*poolID}
	} else {
		q = `SELECT network_load_balancer_pool_id, key, value FROM networks_load_balancer_pools_config`
	}

	allConfigs := map[int64]map[string]string{}
	return allConfigs, query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
		var id int64
		var key, value string

		err := scan(&id, &key, &value)
		if err != nil {
			return err
		}

		if allConfigs[id] == nil {
			allConfigs[id] = map[string]string{}
		}

		_, found := allConfigs[id][key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for network load balancer pool ID %d", key, id)
		}

		allConfigs[id][key] = value

		return nil
	}, args...)
}

// GetNetworksLoadBalancerPoolInstanceRowsByID returns all rows of the networks_load_balancer_pools_instances table for a given instance ID.
// It returns all memberships of the instance in any load balancer pool.
func GetNetworksLoadBalancerPoolInstanceRowsByID(ctx context.Context, tx *sql.Tx, instanceID int64) ([]NetworksLoadBalancerPoolInstanceRow, error) {
	return query.Select[NetworksLoadBalancerPoolInstanceRow](ctx, tx, "WHERE instance_id = ?", instanceID)
}

// UpdateNetworkLoadBalancerPoolInstanceRow updates a single row of the networks_load_balancer_pools_instances table.
func UpdateNetworkLoadBalancerPoolInstanceRow(ctx context.Context, tx *sql.Tx, instance *NetworksLoadBalancerPoolInstanceRow) error {
	return query.UpdateOne(ctx, tx, instance, "WHERE network_load_balancer_pool_id = ? AND instance_id = ?", instance.PoolID, instance.InstanceID)
}

// DeleteNetworksLoadBalancerPoolInstanceRow deletes the networks load balancer pool instance row with the given pool and instance IDs.
func DeleteNetworksLoadBalancerPoolInstanceRow(ctx context.Context, tx *sql.Tx, poolID int64, instanceID int64) error {
	return query.DeleteOne[NetworksLoadBalancerPoolInstanceRow](ctx, tx, "WHERE network_load_balancer_pool_id = ? AND instance_id = ?", poolID, instanceID)
}

// GetNetworksLoadBalancerPoolInstances returns all instances of the networks_load_balancer_pools_instances table,
// or only the config for the pool with the given ID if provided.
func GetNetworksLoadBalancerPoolInstances(ctx context.Context, tx *sql.Tx, poolID *int64) (map[int64][]NetworksLoadBalancerPoolInstance, error) {
	var q string
	var args []any

	if poolID != nil {
		q = "WHERE network_load_balancer_pool_id = ?"
		args = append(args, poolID)
	}

	instances, err := query.Select[NetworksLoadBalancerPoolInstance](ctx, tx, q, args...)
	if err != nil {
		return nil, err
	}

	allInstances := map[int64][]NetworksLoadBalancerPoolInstance{}
	for _, instance := range instances {
		if allInstances[instance.Row.PoolID] == nil {
			allInstances[instance.Row.PoolID] = []NetworksLoadBalancerPoolInstance{}
		}

		allInstances[instance.Row.PoolID] = append(allInstances[instance.Row.PoolID], instance)
	}

	return allInstances, nil
}
