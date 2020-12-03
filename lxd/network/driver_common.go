package network

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/pkg/errors"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
	"github.com/lxc/lxd/shared/validate"
)

// Info represents information about a network driver.
type Info struct {
	Projects           bool // Indicates if driver can be used in network enabled projects.
	NodeSpecificConfig bool // Whether driver has cluster node specific config as a prerequisite for creation.
}

// common represents a generic LXD network.
type common struct {
	logger      logger.Logger
	state       *state.State
	id          int64
	project     string
	name        string
	description string
	config      map[string]string
	status      string
	managed     bool
	nodes       map[int64]db.NetworkNode
}

// init initialise internal variables.
func (n *common) init(state *state.State, id int64, projectName string, netInfo *api.Network, netNodes map[int64]db.NetworkNode) {
	n.logger = logging.AddContext(logger.Log, log.Ctx{"project": projectName, "driver": netInfo.Type, "network": netInfo.Name})
	n.id = id
	n.project = projectName
	n.name = netInfo.Name
	n.config = netInfo.Config
	n.state = state
	n.description = netInfo.Description
	n.status = netInfo.Status
	n.managed = netInfo.Managed
	n.nodes = netNodes
}

// FillConfig fills requested config with any default values, by default this is a no-op.
func (n *common) FillConfig(config map[string]string) error {
	return nil
}

// validationRules returns a map of config rules common to all drivers.
func (n *common) validationRules() map[string]func(string) error {
	return map[string]func(string) error{}
}

// validate a network config against common rules and optional driver specific rules.
func (n *common) validate(config map[string]string, driverRules map[string]func(value string) error) error {
	checkedFields := map[string]struct{}{}

	// Get rules common for all drivers.
	rules := n.validationRules()

	// Merge driver specific rules into common rules.
	for field, validator := range driverRules {
		rules[field] = validator
	}

	// Run the validator against each field.
	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(config[k])
		if err != nil {
			return errors.Wrapf(err, "Invalid value for network %q option %q", n.name, k)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range config {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// User keys are not validated.
		if strings.HasPrefix(k, "user.") {
			continue
		}

		return fmt.Errorf("Invalid option for network %q option %q", n.name, k)
	}

	return nil
}

// ValidateName validates network name.
func (n *common) ValidateName(name string) error {
	err := validate.IsURLSegmentSafe(name)
	if err != nil {
		return err
	}

	if strings.Contains(name, ":") {
		return fmt.Errorf("Cannot contain %q", ":")
	}

	return nil
}

// ID returns the network ID.
func (n *common) ID() int64 {
	return n.id
}

// Name returns the network name.
func (n *common) Name() string {
	return n.name
}

// Description returns the network description.
func (n *common) Description() string {
	return n.description
}

// Status returns the network status.
func (n *common) Status() string {
	return n.status
}

// LocalStatus returns network status of the local cluster member.
func (n *common) LocalStatus() string {
	node, exists := n.nodes[n.state.Cluster.GetNodeID()]
	if !exists {
		return api.NetworkStatusUnknown
	}

	return db.NetworkStateToAPIStatus(node.State)
}

// Config returns the network config.
func (n *common) Config() map[string]string {
	return n.config
}

func (n *common) IsManaged() bool {
	return n.managed
}

// Config returns the common network driver info.
func (n *common) Info() Info {
	return Info{
		Projects:           false,
		NodeSpecificConfig: true,
	}
}

// IsUsed returns whether the network is used by any instances or profiles.
func (n *common) IsUsed() (bool, error) {
	usedBy, err := UsedBy(n.state, n.project, n.name, true)
	if err != nil {
		return false, err
	}

	return len(usedBy) > 0, nil
}

// DHCPv4Subnet returns nil always.
func (n *common) DHCPv4Subnet() *net.IPNet {
	return nil
}

// DHCPv6Subnet returns nil always.
func (n *common) DHCPv6Subnet() *net.IPNet {
	return nil
}

// DHCPv4Ranges returns a parsed set of DHCPv4 ranges for this network.
func (n *common) DHCPv4Ranges() []shared.IPRange {
	dhcpRanges := make([]shared.IPRange, 0)
	if n.config["ipv4.dhcp.ranges"] != "" {
		for _, r := range strings.Split(n.config["ipv4.dhcp.ranges"], ",") {
			parts := strings.SplitN(strings.TrimSpace(r), "-", 2)
			if len(parts) == 2 {
				startIP := net.ParseIP(parts[0])
				endIP := net.ParseIP(parts[1])
				dhcpRanges = append(dhcpRanges, shared.IPRange{
					Start: startIP.To4(),
					End:   endIP.To4(),
				})
			}
		}
	}

	return dhcpRanges
}

// DHCPv6Ranges returns a parsed set of DHCPv6 ranges for this network.
func (n *common) DHCPv6Ranges() []shared.IPRange {
	dhcpRanges := make([]shared.IPRange, 0)
	if n.config["ipv6.dhcp.ranges"] != "" {
		for _, r := range strings.Split(n.config["ipv6.dhcp.ranges"], ",") {
			parts := strings.SplitN(strings.TrimSpace(r), "-", 2)
			if len(parts) == 2 {
				startIP := net.ParseIP(parts[0])
				endIP := net.ParseIP(parts[1])
				dhcpRanges = append(dhcpRanges, shared.IPRange{
					Start: startIP.To16(),
					End:   endIP.To16(),
				})
			}
		}
	}

	return dhcpRanges
}

// update the internal config variables, and if not cluster notification, notifies all nodes and updates database.
func (n *common) update(applyNetwork api.NetworkPut, targetNode string, clientType cluster.ClientType) error {
	// Update internal config before database has been updated (so that if update is a notification we apply
	// the config being supplied and not that in the database).
	n.description = applyNetwork.Description
	n.config = applyNetwork.Config

	// If this update isn't coming via a cluster notification itself, then notify all nodes of change and then
	// update the database.
	if clientType != cluster.ClientTypeNotifier {
		if targetNode == "" {
			// Notify all other nodes to update the network if no target specified.
			notifier, err := cluster.NewNotifier(n.state, n.state.Endpoints.NetworkCert(), cluster.NotifyAll)
			if err != nil {
				return err
			}

			sendNetwork := applyNetwork
			sendNetwork.Config = make(map[string]string)
			for k, v := range applyNetwork.Config {
				// Don't forward node specific keys (these will be merged in on recipient node).
				if shared.StringInSlice(k, db.NodeSpecificNetworkConfig) {
					continue
				}

				sendNetwork.Config[k] = v
			}

			err = notifier(func(client lxd.InstanceServer) error {
				return client.UseProject(n.project).UpdateNetwork(n.name, sendNetwork, "")
			})
			if err != nil {
				return err
			}
		}

		// Update the database.
		err := n.state.Cluster.UpdateNetwork(n.project, n.name, applyNetwork.Description, applyNetwork.Config)
		if err != nil {
			return err
		}
	}

	return nil
}

// configChanged compares supplied new config with existing config. Returns a boolean indicating if differences in
// the config or description were found (and the database record needs updating), and a list of non-user config
// keys that have changed, and a copy of the current internal network config that can be used to revert if needed.
func (n *common) configChanged(newNetwork api.NetworkPut) (bool, []string, api.NetworkPut, error) {
	// Backup the current state.
	oldNetwork := api.NetworkPut{
		Description: n.description,
		Config:      map[string]string{},
	}

	err := shared.DeepCopy(&n.config, &oldNetwork.Config)
	if err != nil {
		return false, nil, oldNetwork, err
	}

	// Diff the configurations.
	changedKeys := []string{}
	dbUpdateNeeded := false

	if newNetwork.Description != n.description {
		dbUpdateNeeded = true
	}

	for k, v := range oldNetwork.Config {
		if v != newNetwork.Config[k] {
			dbUpdateNeeded = true

			// Add non-user changed key to list of changed keys.
			if !strings.HasPrefix(k, "user.") && !shared.StringInSlice(k, changedKeys) {
				changedKeys = append(changedKeys, k)
			}
		}
	}

	for k, v := range newNetwork.Config {
		if v != oldNetwork.Config[k] {
			dbUpdateNeeded = true

			// Add non-user changed key to list of changed keys.
			if !strings.HasPrefix(k, "user.") && !shared.StringInSlice(k, changedKeys) {
				changedKeys = append(changedKeys, k)
			}
		}
	}

	return dbUpdateNeeded, changedKeys, oldNetwork, nil
}

// rename the network directory, update database record and update internal variables.
func (n *common) rename(newName string) error {
	// Clear new directory if exists.
	if shared.PathExists(shared.VarPath("networks", newName)) {
		os.RemoveAll(shared.VarPath("networks", newName))
	}

	// Rename directory to new name.
	if shared.PathExists(shared.VarPath("networks", n.name)) {
		err := os.Rename(shared.VarPath("networks", n.name), shared.VarPath("networks", newName))
		if err != nil {
			return err
		}
	}

	// Rename the database entry.
	err := n.state.Cluster.RenameNetwork(n.project, n.name, newName)
	if err != nil {
		return err
	}

	// Reinitialise internal name variable and logger context with new name.
	n.name = newName

	return nil
}

// delete the network from the database if clusterNotification is false.
func (n *common) delete(clientType cluster.ClientType) error {
	// Only delete database record if not cluster notification.
	if clientType != cluster.ClientTypeNotifier {
		// Notify all other nodes. If any node is down, an error will be returned.
		notifier, err := cluster.NewNotifier(n.state, n.state.Endpoints.NetworkCert(), cluster.NotifyAll)
		if err != nil {
			return err
		}
		err = notifier(func(client lxd.InstanceServer) error {
			return client.UseProject(n.project).DeleteNetwork(n.name)
		})
		if err != nil {
			return err
		}

		// Remove the network from the database.
		err = n.state.Cluster.DeleteNetwork(n.project, n.name)
		if err != nil {
			return err
		}
	}

	// Cleanup storage.
	if shared.PathExists(shared.VarPath("networks", n.name)) {
		os.RemoveAll(shared.VarPath("networks", n.name))
	}

	return nil
}

// Create is a no-op.
func (n *common) Create(clientType cluster.ClientType) error {
	n.logger.Debug("Create", log.Ctx{"clientType": clientType, "config": n.config})

	return nil
}

// HandleHeartbeat is a no-op.
func (n *common) HandleHeartbeat(heartbeatData *cluster.APIHeartbeat) error {
	return nil
}

// lifecycle sends a lifecycle event for the network.
func (n *common) lifecycle(action string, ctx map[string]interface{}) error {
	prefix := "network"
	u := fmt.Sprintf("/1.0/networks/%s", url.PathEscape(n.name))

	if n.project != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(n.project))
	}

	return n.state.Events.SendLifecycle(n.project, fmt.Sprintf("%s-%s", prefix, action), u, ctx)
}
