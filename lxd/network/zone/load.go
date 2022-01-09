package zone

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// LoadByName loads and initialises a Network zone from the database by name.
func LoadByName(s *state.State, name string) (NetworkZone, error) {
	id, projectName, zoneInfo, err := s.Cluster.GetNetworkZone(name)
	if err != nil {
		return nil, err
	}

	var zone NetworkZone = &zone{}
	zone.init(s, id, projectName, zoneInfo)

	return zone, nil
}

// LoadByNameAndProject loads and initialises a Network zone from the database by project and name.
func LoadByNameAndProject(s *state.State, projectName string, name string) (NetworkZone, error) {
	id, zoneInfo, err := s.Cluster.GetNetworkZoneByProject(projectName, name)
	if err != nil {
		return nil, err
	}

	var zone NetworkZone = &zone{}
	zone.init(s, id, projectName, zoneInfo)

	return zone, nil
}

// Create validates supplied record and creates new Network zone record in the database.
func Create(s *state.State, projectName string, zoneInfo *api.NetworkZonesPost) error {
	var zone NetworkZone = &zone{}
	zone.init(s, -1, projectName, nil)

	err := zone.validateName(zoneInfo.Name)
	if err != nil {
		return err
	}

	err = zone.validateConfig(&zoneInfo.NetworkZonePut)
	if err != nil {
		return err
	}

	// Load the project.
	var p *db.Project
	var config map[string]string
	err = s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		p, err = tx.GetProject(projectName)
		if err != nil {
			return err
		}

		config, err = tx.GetProjectConfig(p.ID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Validate restrictions.
	if shared.IsTrue(config["restricted"]) {
		found := false
		for _, entry := range strings.Split(config["restricted.networks.zones"], ",") {
			entry = strings.TrimSpace(entry)

			if zoneInfo.Name == entry || strings.HasSuffix(zoneInfo.Name, "."+entry) {
				found = true
				break
			}
		}

		if !found {
			return api.StatusErrorf(http.StatusForbidden, "Project isn't allowed to use this DNS zone")
		}
	}

	// Insert DB record.
	_, err = s.Cluster.CreateNetworkZone(projectName, zoneInfo)
	if err != nil {
		return err
	}

	// Trigger a refresh of the TSIG entries.
	err = s.DNS.UpdateTSIG()
	if err != nil {
		return err
	}

	return nil
}

// Exists checks the zone name(s) provided exists.
// If multiple names are provided, also checks that duplicate names aren't specified in the list.
func Exists(s *state.State, name ...string) error {
	checkedzoneNames := make(map[string]struct{}, len(name))
	for _, zoneName := range name {
		_, _, _, err := s.Cluster.GetNetworkZone(zoneName)
		if err != nil {
			return fmt.Errorf("Network zone %q does not exist", zoneName)
		}

		_, found := checkedzoneNames[zoneName]
		if found {
			return fmt.Errorf("Network zone %q specified multiple times", zoneName)
		}

		checkedzoneNames[zoneName] = struct{}{}
	}

	return nil
}
