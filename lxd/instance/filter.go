package instance

import (
	"github.com/lxc/lxd/lxd/filter"
	"github.com/lxc/lxd/shared/api"
)

// FilterFull returns a filtered list of full instances that match the given clauses.
func FilterFull(instances []*api.InstanceFull, clauses []filter.Clause) []*api.InstanceFull {
	filtered := []*api.InstanceFull{}
	for _, instance := range instances {
		if !filter.Match(*instance, clauses) {
			continue
		}

		filtered = append(filtered, instance)
	}

	return filtered
}
