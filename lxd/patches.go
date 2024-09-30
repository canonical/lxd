package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	liblxc "github.com/lxc/go-lxc"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/network"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

type patchStage int

// Define the stages that patches can run at.
const (
	patchNoStageSet patchStage = iota
	patchPreLoadClusterConfig
	patchPreDaemonStorage
	patchPostDaemonStorage
	patchPostNetworks
)

/*
Patches are one-time actions that are sometimes needed to update

	existing container configuration or move things around on the
	filesystem.

	Those patches are applied at startup time after the database schema
	has been fully updated. Patches can therefore assume a working database.

	At the time the patches are applied, the containers aren't started
	yet and the daemon isn't listening to requests.

	DO NOT use this mechanism for database update. Schema updates must be
	done through the separate schema update mechanism.


	Only append to the patches list, never remove entries and never re-order them.
*/
var patches = []patch{
	{name: "storage_lvm_skipactivation", stage: patchPostDaemonStorage, run: patchGenericStorage},
	{name: "clustering_drop_database_role", stage: patchPostDaemonStorage, run: patchClusteringDropDatabaseRole},
	{name: "network_clear_bridge_volatile_hwaddr", stage: patchPostDaemonStorage, run: patchGenericNetwork(patchNetworkClearBridgeVolatileHwaddr)},
	{name: "move_backups_instances", stage: patchPostDaemonStorage, run: patchMoveBackupsInstances},
	{name: "network_ovn_enable_nat", stage: patchPostDaemonStorage, run: patchGenericNetwork(patchNetworkOVNEnableNAT)},
	{name: "network_ovn_remove_routes", stage: patchPostDaemonStorage, run: patchGenericNetwork(patchNetworkOVNRemoveRoutes)},
	{name: "network_fan_enable_nat", stage: patchPostDaemonStorage, run: patchGenericNetwork(patchNetworkFANEnableNAT)},
	{name: "thinpool_typo_fix", stage: patchPostDaemonStorage, run: patchThinpoolTypoFix},
	{name: "vm_rename_uuid_key", stage: patchPostDaemonStorage, run: patchVMRenameUUIDKey},
	{name: "db_nodes_autoinc", stage: patchPreDaemonStorage, run: patchDBNodesAutoInc},
	{name: "network_acl_remove_defaults", stage: patchPostDaemonStorage, run: patchGenericNetwork(patchNetworkACLRemoveDefaults)},
	{name: "clustering_server_cert_trust", stage: patchPreDaemonStorage, run: patchClusteringServerCertTrust},
	{name: "warnings_remove_empty_node", stage: patchPostDaemonStorage, run: patchRemoveWarningsWithEmptyNode},
	{name: "dnsmasq_entries_include_device_name", stage: patchPostDaemonStorage, run: patchDnsmasqEntriesIncludeDeviceName},
	{name: "storage_missing_snapshot_records", stage: patchPostDaemonStorage, run: patchGenericStorage},
	{name: "storage_delete_old_snapshot_records", stage: patchPostDaemonStorage, run: patchGenericStorage},
	{name: "storage_zfs_drop_block_volume_filesystem_extension", stage: patchPostDaemonStorage, run: patchGenericStorage},
	{name: "storage_prefix_bucket_names_with_project", stage: patchPostDaemonStorage, run: patchGenericStorage},
	{name: "storage_move_custom_iso_block_volumes", stage: patchPostDaemonStorage, run: patchStorageRenameCustomISOBlockVolumes},
	{name: "zfs_set_content_type_user_property", stage: patchPostDaemonStorage, run: patchZfsSetContentTypeUserProperty},
	{name: "storage_zfs_unset_invalid_block_settings", stage: patchPostDaemonStorage, run: patchStorageZfsUnsetInvalidBlockSettings},
	{name: "storage_zfs_unset_invalid_block_settings_v2", stage: patchPostDaemonStorage, run: patchStorageZfsUnsetInvalidBlockSettingsV2},
	{name: "storage_unset_invalid_block_settings", stage: patchPostDaemonStorage, run: patchStorageUnsetInvalidBlockSettings},
	{name: "candid_rbac_remove_config_keys", stage: patchPreDaemonStorage, run: patchRemoveCandidRBACConfigKeys},
	{name: "storage_set_volume_uuid", stage: patchPostDaemonStorage, run: patchStorageSetVolumeUUID},
	{name: "storage_set_volume_uuid_v2", stage: patchPostDaemonStorage, run: patchStorageSetVolumeUUIDV2},
	{name: "storage_move_custom_iso_block_volumes_v2", stage: patchPostDaemonStorage, run: patchStorageRenameCustomISOBlockVolumesV2},
	{name: "storage_unset_invalid_block_settings_v2", stage: patchPostDaemonStorage, run: patchStorageUnsetInvalidBlockSettingsV2},
	{name: "config_remove_core_trust_password", stage: patchPreLoadClusterConfig, run: patchRemoveCoreTrustPassword},
	{name: "entity_type_instance_snapshot_on_delete_trigger_typo_fix", stage: patchPreLoadClusterConfig, run: patchEntityTypeInstanceSnapshotOnDeleteTriggerTypoFix},
	{name: "instance_remove_volatile_last_state_ip_addresses", stage: patchPostDaemonStorage, run: patchInstanceRemoveVolatileLastStateIPAddresses},
	{name: "container_update_stop_hook", stage: patchPostDaemonStorage, run: patchContainerUpdateStopHook},
}

type patch struct {
	name  string
	stage patchStage
	run   func(name string, d *Daemon) error
}

func (p *patch) apply(d *Daemon) error {
	err := p.run(p.name, d)
	if err != nil {
		return fmt.Errorf("Failed applying patch %q: %w", p.name, err)
	}

	err = d.db.Node.MarkPatchAsApplied(p.name)
	if err != nil {
		return fmt.Errorf("Failed marking patch applied %q: %w", p.name, err)
	}

	return nil
}

// Return the names of all available patches.
func patchesGetNames() []string {
	names := make([]string, len(patches))
	for i, patch := range patches {
		if patch.stage == patchNoStageSet {
			continue // Ignore any patch without explicitly set stage (it is defined incorrectly).
		}

		names[i] = patch.name
	}

	return names
}

// patchesApply applies the patches for the specified stage.
func patchesApply(d *Daemon, stage patchStage) error {
	logger.Debug("Checking for patches", logger.Ctx{"stage": stage})
	appliedPatches, err := d.db.Node.GetAppliedPatches()
	if err != nil {
		return err
	}

	for _, patch := range patches {
		if patch.stage == patchNoStageSet {
			return fmt.Errorf("Patch %q has no stage set: %d", patch.name, patch.stage)
		}

		if patch.stage != stage {
			continue
		}

		if shared.ValueInSlice(patch.name, appliedPatches) {
			continue
		}

		logger.Info("Applying patch", logger.Ctx{"name": patch.name, "stage": stage})
		err := patch.apply(d)
		if err != nil {
			return err
		}
	}

	return nil
}

// selectedPatchClusterMember returns true if the current node is eligible to execute a patch.
// Use this function to deterministically coordinate the execution of patches on a single cluster member.
// The member selection isn't based on the raft leader election which allows getting the same
// results even if the raft cluster is currently running any kind of election.
func selectedPatchClusterMember(s *state.State) (bool, error) {
	// If not clustered indicate to apply the patch.
	if !s.ServerClustered {
		return true, nil
	}

	// Get a list of all cluster members.
	var clusterMembers []string
	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		nodeInfos, err := tx.GetNodes(ctx)
		if err != nil {
			return err
		}

		for _, nodeInfo := range nodeInfos {
			clusterMembers = append(clusterMembers, nodeInfo.Name)
		}

		return nil
	})
	if err != nil {
		return false, err
	}

	if len(clusterMembers) == 0 {
		return false, fmt.Errorf("Clustered but no member found")
	}

	// Sort the cluster members by name.
	sort.Strings(clusterMembers)

	// If the first cluster member in the sorted list matches the current node indicate to apply the patch.
	return clusterMembers[0] == s.ServerName, nil
}

// Patches begin here

func patchDnsmasqEntriesIncludeDeviceName(name string, d *Daemon) error {
	err := network.UpdateDNSMasqStatic(d.State(), "")
	if err != nil {
		return err
	}

	return nil
}

func patchRemoveWarningsWithEmptyNode(name string, d *Daemon) error {
	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		warnings, err := dbCluster.GetWarnings(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, w := range warnings {
			if w.Node == "" {
				err = dbCluster.DeleteWarning(ctx, tx.Tx(), w.UUID)
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func patchClusteringServerCertTrust(name string, d *Daemon) error {
	if !d.serverClustered {
		return nil
	}

	var serverName string
	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		serverName, err = tx.GetLocalNodeName(ctx)
		return err
	})
	if err != nil {
		return err
	}

	// Add our server cert to DB trust store.
	serverCert, err := util.LoadServerCert(d.os.VarDir)
	if err != nil {
		return err
	}
	// Update our own entry in the nodes table.
	logger.Infof("Adding local server certificate to global trust store for %q patch", name)
	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.EnsureServerCertificateTrusted(serverName, serverCert, tx)
	})
	if err != nil {
		return err
	}

	logger.Infof("Added local server certificate to global trust store for %q patch", name)

	// Check all other members have done the same.
	for {
		var err error
		var dbCerts []dbCluster.Certificate
		err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			dbCerts, err = dbCluster.GetCertificates(ctx, tx.Tx())
			return err
		})
		if err != nil {
			return err
		}

		trustedServerCerts := make(map[string]dbCluster.Certificate)

		for _, c := range dbCerts {
			if c.Type == certificate.TypeServer {
				trustedServerCerts[c.Name] = c
			}
		}

		var members []db.NodeInfo
		err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			members, err = tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			return nil
		})
		if err != nil {
			return err
		}

		missingCerts := false
		for _, member := range members {
			_, found := trustedServerCerts[member.Name]
			if !found {
				logger.Warnf("Missing trusted server certificate for cluster member %q", member.Name)
				missingCerts = true
				break
			}
		}

		if missingCerts {
			logger.Warnf("Waiting for %q patch to be applied on all cluster members", name)
			time.Sleep(time.Second)
			continue
		}

		logger.Infof("Trusted server certificates found in trust store for all cluster members")
		break
	}

	// Now switch to using our server certificate for intra-cluster communication and load the trusted server
	// certificates for the other members into the in-memory trusted cache.
	logger.Infof("Set client certificate to server certificate %v", serverCert.Fingerprint())
	d.serverCertInt = serverCert
	updateIdentityCache(d)

	return nil
}

// patchNetworkACLRemoveDefaults removes the "default.action" and "default.logged" settings from network ACLs.
// It was decided that the user experience of having the default actions at the ACL level was confusing when using
// multiple ACLs, and that the interplay between conflicting default actions on multiple ACLs was difficult to
// understand. Instead it will be replace with a network and NIC level defaults settings.
func patchNetworkACLRemoveDefaults(name string, d *Daemon) error {
	var err error
	var projectNames []string

	// Get projects.
	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNames, err = dbCluster.GetProjectNames(ctx, tx.Tx())
		return err
	})
	if err != nil {
		return err
	}

	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get ACLs in projects.
		for _, projectName := range projectNames {
			aclNames, err := tx.GetNetworkACLs(ctx, projectName)
			if err != nil {
				return err
			}

			for _, aclName := range aclNames {
				aclID, acl, err := tx.GetNetworkACL(ctx, projectName, aclName)
				if err != nil {
					return err
				}

				modified := false

				// Remove the offending keys if found.
				_, found := acl.Config["default.action"]
				if found {
					delete(acl.Config, "default.action")
					modified = true
				}

				_, found = acl.Config["default.logged"]
				if found {
					delete(acl.Config, "default.logged")
					modified = true
				}

				// Write back modified config if needed.
				if modified {
					err = tx.UpdateNetworkACL(ctx, aclID, acl.Writable())
					if err != nil {
						return fmt.Errorf("Failed updating network ACL %d: %w", aclID, err)
					}
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// patchDBNodesAutoInc re-creates the nodes table id column as AUTOINCREMENT.
// Its done as a patch rather than a schema update so we can use PRAGMA foreign_keys = OFF without a transaction.
func patchDBNodesAutoInc(name string, d *Daemon) error {
	for {
		// Only apply patch if schema needs it.
		var schemaSQL string
		row := d.State().DB.Cluster.DB().QueryRow("SELECT sql FROM sqlite_master WHERE name = 'nodes'")
		err := row.Scan(&schemaSQL)
		if err != nil {
			return err
		}

		if strings.Contains(schemaSQL, "id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL") {
			logger.Debugf(`Skipping %q patch as "nodes" table id column already AUTOINCREMENT`, name)
			return nil // Nothing to do.
		}

		// Only apply patch on leader, otherwise wait for it to be applied.
		var localConfig *node.Config
		err = d.db.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
			localConfig, err = node.ConfigLoad(ctx, tx)
			return err
		})
		if err != nil {
			return err
		}

		leaderAddress, err := d.gateway.LeaderAddress()
		if err != nil {
			if errors.Is(err, cluster.ErrNodeIsNotClustered) {
				break // Apply change on standalone node.
			}

			return err
		}

		if localConfig.ClusterAddress() == leaderAddress {
			break // Apply change on leader node.
		}

		logger.Warnf("Waiting for %q patch to be applied on leader cluster member", name)
		time.Sleep(time.Second)
	}

	// Apply patch.
	_, err := d.State().DB.Cluster.DB().Exec(`
PRAGMA foreign_keys=OFF; -- So that integrity doesn't get in the way for now.
PRAGMA legacy_alter_table = ON; -- So that views referencing this table don't block change.

CREATE TABLE nodes_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	name TEXT NOT NULL,
	description TEXT DEFAULT '',
	address TEXT NOT NULL,
	schema INTEGER NOT NULL,
	api_extensions INTEGER NOT NULL,
	heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP,
	state INTEGER NOT NULL DEFAULT 0,
	arch INTEGER NOT NULL DEFAULT 0 CHECK (arch > 0),
	failure_domain_id INTEGER DEFAULT NULL REFERENCES nodes_failure_domains (id) ON DELETE SET NULL,
	UNIQUE (name),
	UNIQUE (address)
);

INSERT INTO nodes_new (id, name, description, address, schema, api_extensions, heartbeat, state, arch, failure_domain_id)
	SELECT id, name, description, address, schema, api_extensions, heartbeat, state, arch, failure_domain_id FROM nodes;

DROP TABLE nodes;
ALTER TABLE nodes_new RENAME TO nodes;

PRAGMA foreign_keys=ON; -- Make sure we turn integrity checks back on.
PRAGMA legacy_alter_table = OFF; -- So views check integrity again.
`)

	return err
}

// patchVMRenameUUIDKey renames the volatile.vm.uuid key to volatile.uuid in instance and snapshot configs.
func patchVMRenameUUIDKey(name string, d *Daemon) error {
	oldUUIDKey := "volatile.vm.uuid"
	newUUIDKey := "volatile.uuid"

	s := d.State()

	return s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(inst db.InstanceArgs, p api.Project) error {
			if inst.Type != instancetype.VM {
				return nil
			}

			uuid := inst.Config[oldUUIDKey]
			if uuid != "" {
				changes := map[string]string{
					oldUUIDKey: "",
					newUUIDKey: uuid,
				}

				logger.Debugf("Renaming config key %q to %q for VM %q (Project %q)", oldUUIDKey, newUUIDKey, inst.Name, inst.Project)
				err := tx.UpdateInstanceConfig(inst.ID, changes)
				if err != nil {
					return fmt.Errorf("Failed renaming config key %q to %q for VM %q (Project %q): %w", oldUUIDKey, newUUIDKey, inst.Name, inst.Project, err)
				}
			}

			snaps, err := tx.GetInstanceSnapshotsWithName(ctx, inst.Project, inst.Name)
			if err != nil {
				return err
			}

			for _, snap := range snaps {
				config, err := dbCluster.GetInstanceSnapshotConfig(ctx, tx.Tx(), snap.ID)
				if err != nil {
					return err
				}

				uuid := config[oldUUIDKey]
				if uuid != "" {
					changes := map[string]string{
						oldUUIDKey: "",
						newUUIDKey: uuid,
					}

					logger.Debugf("Renaming config key %q to %q for VM %q (Project %q)", oldUUIDKey, newUUIDKey, snap.Name, snap.Project)
					err = tx.UpdateInstanceSnapshotConfig(snap.ID, changes)
					if err != nil {
						return fmt.Errorf("Failed renaming config key %q to %q for VM %q (Project %q): %w", oldUUIDKey, newUUIDKey, snap.Name, snap.Project, err)
					}
				}
			}

			return nil
		})
	})
}

// patchThinpoolTypoFix renames any config incorrectly set config file entries due to the lvm.thinpool_name typo.
func patchThinpoolTypoFix(name string, d *Daemon) error {
	revert := revert.New()
	defer revert.Fail()

	// Setup a transaction.
	tx, err := d.db.Cluster.Begin()
	if err != nil {
		return fmt.Errorf("Failed to begin transaction: %w", err)
	}

	revert.Add(func() { _ = tx.Rollback() })

	// Fetch the IDs of all existing nodes.
	nodeIDs, err := query.SelectIntegers(context.TODO(), tx, "SELECT id FROM nodes")
	if err != nil {
		return fmt.Errorf("Failed to get IDs of current nodes: %w", err)
	}

	// Fetch the IDs of all existing lvm pools.
	poolIDs, err := query.SelectIntegers(context.TODO(), tx, "SELECT id FROM storage_pools WHERE driver='lvm'")
	if err != nil {
		return fmt.Errorf("Failed to get IDs of current lvm pools: %w", err)
	}

	for _, poolID := range poolIDs {
		// Fetch the config for this lvm pool and check if it has the lvm.thinpool_name.
		config, err := query.SelectConfig(context.TODO(), tx, "storage_pools_config", "storage_pool_id=? AND node_id IS NULL", poolID)
		if err != nil {
			return fmt.Errorf("Failed to fetch of lvm pool config: %w", err)
		}

		value, ok := config["lvm.thinpool_name"]
		if !ok {
			continue
		}

		// Delete the current key
		_, err = tx.Exec(`
DELETE FROM storage_pools_config WHERE key='lvm.thinpool_name' AND storage_pool_id=? AND node_id IS NULL
`, poolID)
		if err != nil {
			return fmt.Errorf("Failed to delete lvm.thinpool_name config: %w", err)
		}

		// Add the config entry for each node
		for _, nodeID := range nodeIDs {
			_, err := tx.Exec(`
INSERT INTO storage_pools_config(storage_pool_id, node_id, key, value)
  VALUES(?, ?, 'lvm.thinpool_name', ?)
`, poolID, nodeID, value)
			if err != nil {
				return fmt.Errorf("Failed to create lvm.thinpool_name node config: %w", err)
			}
		}
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("Failed to commit transaction: %w", err)
	}

	revert.Success()
	return nil
}

// patchNetworkFANEnableNAT sets "ipv4.nat=true" on fan bridges that are missing the "ipv4.nat" setting.
// This prevents outbound connectivity breaking on existing fan networks now that the default behaviour of not
// having "ipv4.nat" set is to disable NAT (bringing in line with the non-fan bridge behavior and docs).
func patchNetworkFANEnableNAT(name string, d *Daemon) error {
	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNetworks, err := tx.GetCreatedNetworks(ctx)
		if err != nil {
			return err
		}

		for projectName, networks := range projectNetworks {
			for networkID, network := range networks {
				if network.Type != "bridge" {
					continue
				}

				if network.Config["bridge.mode"] != "fan" {
					continue
				}

				modified := false

				// Enable ipv4.nat if setting not specified.
				_, found := network.Config["ipv4.nat"]
				if !found {
					modified = true
					network.Config["ipv4.nat"] = "true"
				}

				if modified {
					err = tx.UpdateNetwork(ctx, projectName, network.Name, network.Description, network.Config)
					if err != nil {
						return fmt.Errorf("Failed setting ipv4.nat=true for fan network %q (%d): %w", network.Name, networkID, err)
					}

					logger.Debugf("Set ipv4.nat=true for fan network %q (%d)", network.Name, networkID)
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// patchNetworkOVNRemoveRoutes removes the "ipv4.routes.external" and "ipv6.routes.external" settings from OVN
// networks. It was decided that the OVN NIC level equivalent settings were sufficient.
func patchNetworkOVNRemoveRoutes(name string, d *Daemon) error {
	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNetworks, err := tx.GetCreatedNetworks(ctx)
		if err != nil {
			return err
		}

		for projectName, networks := range projectNetworks {
			for networkID, network := range networks {
				if network.Type != "ovn" {
					continue
				}

				modified := false

				// Ensure existing behaviour of having NAT enabled if IP address was set.
				_, found := network.Config["ipv4.routes.external"]
				if found {
					modified = true
					delete(network.Config, "ipv4.routes.external")
				}

				_, found = network.Config["ipv6.routes.external"]
				if found {
					modified = true
					delete(network.Config, "ipv6.routes.external")
				}

				if modified {
					err = tx.UpdateNetwork(ctx, projectName, network.Name, network.Description, network.Config)
					if err != nil {
						return fmt.Errorf("Failed removing OVN external route settings for %q (%d): %w", network.Name, networkID, err)
					}

					logger.Debugf("Removing external route settings for OVN network %q (%d)", network.Name, networkID)
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// patchNetworkOVNEnableNAT adds "ipv4.nat" and "ipv6.nat" keys set to "true" to OVN networks if not present.
// This is to ensure existing networks retain the old behaviour of always having NAT enabled as we introduce
// the new NAT settings which default to disabled if not specified.
// patchNetworkCearBridgeVolatileHwaddr removes the unsupported `volatile.bridge.hwaddr` config key from networks.
func patchNetworkOVNEnableNAT(name string, d *Daemon) error {
	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNetworks, err := tx.GetCreatedNetworks(ctx)
		if err != nil {
			return err
		}

		for projectName, networks := range projectNetworks {
			for networkID, network := range networks {
				if network.Type != "ovn" {
					continue
				}

				modified := false

				// Ensure existing behaviour of having NAT enabled if IP address was set.
				if network.Config["ipv4.address"] != "" && network.Config["ipv4.nat"] == "" {
					modified = true
					network.Config["ipv4.nat"] = "true"
				}

				if network.Config["ipv6.address"] != "" && network.Config["ipv6.nat"] == "" {
					modified = true
					network.Config["ipv6.nat"] = "true"
				}

				if modified {
					err = tx.UpdateNetwork(ctx, projectName, network.Name, network.Description, network.Config)
					if err != nil {
						return fmt.Errorf("Failed saving OVN NAT settings for %q (%d): %w", network.Name, networkID, err)
					}

					logger.Debugf("Enabling NAT for OVN network %q (%d)", network.Name, networkID)
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// Moves backups from shared.VarPath("backups") to shared.VarPath("backups", "instances").
func patchMoveBackupsInstances(name string, d *Daemon) error {
	if !shared.PathExists(shared.VarPath("backups")) {
		return nil // Nothing to do, no backups directory.
	}

	backupsPath := shared.VarPath("backups", "instances")

	err := os.MkdirAll(backupsPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed creating instances backup directory %q: %w", backupsPath, err)
	}

	backups, err := os.ReadDir(shared.VarPath("backups"))
	if err != nil {
		return fmt.Errorf("Failed listing existing backup directory %q: %w", shared.VarPath("backups"), err)
	}

	for _, backupDir := range backups {
		if backupDir.Name() == "instances" || strings.HasPrefix(backupDir.Name(), backup.WorkingDirPrefix) {
			continue // Don't try and move our new instances directory or temporary directories.
		}

		oldPath := shared.VarPath("backups", backupDir.Name())
		newPath := filepath.Join(backupsPath, backupDir.Name())
		logger.Debugf("Moving backup from %q to %q", oldPath, newPath)
		err = os.Rename(oldPath, newPath)
		if err != nil {
			return fmt.Errorf("Failed moving backup from %q to %q: %w", oldPath, newPath, err)
		}
	}

	return nil
}

func patchGenericStorage(name string, d *Daemon) error {
	return storagePools.Patch(d.State(), name)
}

func patchGenericNetwork(f func(name string, d *Daemon) error) func(name string, d *Daemon) error {
	return func(name string, d *Daemon) error {
		err := network.PatchPreCheck()
		if err != nil {
			return err
		}

		return f(name, d)
	}
}

func patchClusteringDropDatabaseRole(name string, d *Daemon) error {
	return d.State().DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		for _, member := range members {
			err := tx.UpdateNodeRoles(member.ID, nil)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// patchNetworkClearBridgeVolatileHwaddr removes the unsupported `volatile.bridge.hwaddr` config key from networks.
func patchNetworkClearBridgeVolatileHwaddr(name string, d *Daemon) error {
	// Use api.ProjectDefaultName, as bridge networks don't support projects.
	projectName := api.ProjectDefaultName

	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the list of networks.
		networks, err := tx.GetNetworks(ctx, projectName)
		if err != nil {
			return fmt.Errorf("Failed loading networks for network_clear_bridge_volatile_hwaddr patch: %w", err)
		}

		for _, networkName := range networks {
			_, net, _, err := tx.GetNetworkInAnyState(ctx, projectName, networkName)
			if err != nil {
				return fmt.Errorf("Failed loading network %q for network_clear_bridge_volatile_hwaddr patch: %w", networkName, err)
			}

			if net.Config["volatile.bridge.hwaddr"] != "" {
				delete(net.Config, "volatile.bridge.hwaddr")
				err = tx.UpdateNetwork(ctx, projectName, net.Name, net.Description, net.Config)
				if err != nil {
					return fmt.Errorf("Failed updating network %q for network_clear_bridge_volatile_hwaddr patch: %w", networkName, err)
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// patchStorageRenameCustomISOBlockVolumes renames existing custom ISO volumes by adding the ".iso" suffix so they can be distinguished from regular custom block volumes.
// This patch doesn't use the patchGenericStorage function because the storage drivers themselves aren't aware of custom ISO volumes.
func patchStorageRenameCustomISOBlockVolumes(name string, d *Daemon) error {
	// Superseded by patchStorageRenameCustomISOBlockVolumesV2.
	return nil
}

// patchZfsSetContentTypeUserProperty adds the `lxd:content_type` user property to custom storage volumes. In case of recovery, this allows for proper detection of block-mode enabled volumes.
func patchZfsSetContentTypeUserProperty(name string, d *Daemon) error {
	s := d.State()

	var pools []string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get all storage pool names.
		pools, err = tx.GetStoragePoolNames(ctx)

		return err
	})
	if err != nil {
		// Skip the rest of the patch if no storage pools were found.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}

		return fmt.Errorf("Failed getting storage pool names: %w", err)
	}

	volTypeCustom := dbCluster.StoragePoolVolumeTypeCustom
	customPoolVolumes := make(map[string][]*db.StorageVolume, 0)

	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, pool := range pools {
			// Get storage pool ID.
			poolID, err := tx.GetStoragePoolID(ctx, pool)
			if err != nil {
				return fmt.Errorf("Failed getting storage pool ID of pool %q: %w", pool, err)
			}

			// Get the pool's custom storage volumes.
			customVolumes, err := tx.GetStorageVolumes(ctx, false, db.StorageVolumeFilter{Type: &volTypeCustom, PoolID: &poolID})
			if err != nil {
				return fmt.Errorf("Failed getting custom storage volumes of pool %q: %w", pool, err)
			}

			if customPoolVolumes[pool] == nil {
				customPoolVolumes[pool] = []*db.StorageVolume{}
			}

			customPoolVolumes[pool] = append(customPoolVolumes[pool], customVolumes...)
		}

		return nil
	})
	if err != nil {
		return err
	}

	for poolName, volumes := range customPoolVolumes {
		// Load storage pool.
		p, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return fmt.Errorf("Failed loading pool %q: %w", poolName, err)
		}

		if p.Driver().Info().Name != "zfs" {
			continue
		}

		for _, vol := range volumes {
			// Only consider volumes local to this server.
			if s.ServerClustered && vol.Location != s.ServerName {
				continue
			}

			zfsPoolName := p.Driver().Config()["zfs.pool_name"]
			if zfsPoolName != "" {
				poolName = zfsPoolName
			}

			zfsVolName := fmt.Sprintf("%s/%s/%s", poolName, storageDrivers.VolumeTypeCustom, project.StorageVolume(vol.Project, vol.Name))

			_, err = shared.RunCommand("zfs", "set", fmt.Sprintf("lxd:content_type=%s", vol.ContentType), zfsVolName)
			if err != nil {
				logger.Debug("Failed setting lxd:content_type property", logger.Ctx{"name": zfsVolName, "err": err})
			}
		}
	}

	return nil
}

// patchStorageZfsUnsetInvalidBlockSettings removes invalid block settings from volumes.
func patchStorageZfsUnsetInvalidBlockSettings(_ string, d *Daemon) error {
	// Superseded by patchStorageZfsUnsetInvalidBlockSettingsV2.
	return nil
}

// patchStorageZfsUnsetInvalidBlockSettingsV2 removes invalid block settings from volumes.
// This patch fixes the previous one.
// - Handle non-clusted environments correctly.
// - Always remove block.* settings from VMs.
func patchStorageZfsUnsetInvalidBlockSettingsV2(_ string, d *Daemon) error {
	s := d.State()

	var pools []string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get all storage pool names.
		pools, err = tx.GetStoragePoolNames(ctx)

		return err
	})
	if err != nil {
		// Skip the rest of the patch if no storage pools were found.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}

		return fmt.Errorf("Failed getting storage pool names: %w", err)
	}

	volTypeCustom := dbCluster.StoragePoolVolumeTypeCustom
	volTypeVM := dbCluster.StoragePoolVolumeTypeVM

	poolIDNameMap := make(map[int64]string, 0)
	poolVolumes := make(map[int64][]*db.StorageVolume, 0)

	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, pool := range pools {
			// Get storage pool ID.
			poolID, err := tx.GetStoragePoolID(ctx, pool)
			if err != nil {
				return fmt.Errorf("Failed getting storage pool ID of pool %q: %w", pool, err)
			}

			driverName, err := tx.GetStoragePoolDriver(ctx, poolID)
			if err != nil {
				return fmt.Errorf("Failed getting storage pool driver of pool %q: %w", pool, err)
			}

			if driverName != "zfs" {
				continue
			}

			// Get the pool's custom storage volumes.
			volumes, err := tx.GetStorageVolumes(ctx, false, db.StorageVolumeFilter{Type: &volTypeCustom, PoolID: &poolID}, db.StorageVolumeFilter{Type: &volTypeVM, PoolID: &poolID})
			if err != nil {
				return fmt.Errorf("Failed getting custom storage volumes of pool %q: %w", pool, err)
			}

			if poolVolumes[poolID] == nil {
				poolVolumes[poolID] = []*db.StorageVolume{}
			}

			poolIDNameMap[poolID] = pool
			poolVolumes[poolID] = append(poolVolumes[poolID], volumes...)
		}

		return nil
	})
	if err != nil {
		return err
	}

	var volType int

	for pool, volumes := range poolVolumes {
		for _, vol := range volumes {
			// Only consider volumes local to this server.
			if s.ServerClustered && vol.Location != s.ServerName {
				continue
			}

			config := vol.Config

			// Only check zfs.block_mode for custom volumes. VMs should never have any block.* settings
			// regardless of the zfs.block_mode setting.
			if shared.IsTrue(config["zfs.block_mode"]) && vol.Type == dbCluster.StoragePoolVolumeTypeNameCustom {
				continue
			}

			update := false
			for _, k := range []string{"block.filesystem", "block.mount_options"} {
				_, found := config[k]
				if found {
					delete(config, k)
					update = true
				}
			}

			if !update {
				continue
			}

			if vol.Type == dbCluster.StoragePoolVolumeTypeNameVM {
				volType = volTypeVM
			} else if vol.Type == dbCluster.StoragePoolVolumeTypeNameCustom {
				volType = volTypeCustom
			} else {
				// Should not happen.
				continue
			}

			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.UpdateStoragePoolVolume(ctx, vol.Project, vol.Name, volType, pool, vol.Description, config)
			})
			if err != nil {
				return fmt.Errorf("Failed updating volume %q in project %q on pool %q: %w", vol.Name, vol.Project, poolIDNameMap[pool], err)
			}
		}
	}

	return nil
}

// patchStorageUnsetInvalidBlockSettings removes invalid block settings from LVM and Ceph RBD volumes.
func patchStorageUnsetInvalidBlockSettings(_ string, d *Daemon) error {
	// This patch is superseded by patchStorageUnsetInvalidBlockSettingsV2.
	// In its earlier version the patch might not have been applied due to leader election.
	return nil
}

// patchRemoveCandidRBACConfigKeys removes all Candid and RBAC related configuration from the database.
func patchRemoveCandidRBACConfigKeys(_ string, d *Daemon) error {
	s := d.State()
	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateClusterConfig(map[string]string{
			"candid.api.url":         "",
			"candid.api.key":         "",
			"candid.expiry":          "",
			"candid.domains":         "",
			"rbac.api.url":           "",
			"rbac.api.key":           "",
			"rbac.expiry":            "",
			"rbac.agent.url":         "",
			"rbac.agent.username":    "",
			"rbac.agent.private_key": "",
			"rbac.agent.public_key":  "",
		})
	})
	if err != nil {
		return fmt.Errorf("Failed to remove RBAC and Candid configuration keys: %w", err)
	}

	return nil
}

// patchStorageSetVolumeUUID sets a unique volatile.uuid field for each volume and its snapshots.
func patchStorageSetVolumeUUID(_ string, d *Daemon) error {
	// This patch is superseded by patchStorageSetVolumeUUIDV2.
	// In its earlier version the patch might not have been applied due to leader election.
	return nil
}

// patchStorageSetVolumeUUIDV2 sets a unique volatile.uuid field for each volume and its snapshots using an idempotent SQL query.
func patchStorageSetVolumeUUIDV2(_ string, d *Daemon) error {
	type volumeConfigEntry struct {
		id    string
		value *string
	}

	// Gets a list of `volatile.uuid` settings for all storage volumes.
	getVolumeUUIDs := func(volumesTable string, volumesConfigTable string, volumesConfigTableID string) ([]volumeConfigEntry, error) {
		rows, err := d.State().DB.Cluster.DB().QueryContext(d.shutdownCtx, fmt.Sprintf(`
SELECT %[1]s.id, %[2]s.value
FROM %[1]s
	LEFT JOIN %[2]s
		ON %[2]s.%[3]s = %[1]s.id
		AND %[2]s.key = "volatile.uuid"
		`, volumesTable, volumesConfigTable, volumesConfigTableID))
		if err != nil {
			return nil, err
		}

		defer func() { _ = rows.Close() }()

		var volumeUUIDs []volumeConfigEntry

		for rows.Next() {
			var r volumeConfigEntry
			err = rows.Scan(&r.id, &r.value)
			if err != nil {
				return nil, fmt.Errorf("Failed to scan row into struct: %w", err)
			}

			volumeUUIDs = append(volumeUUIDs, r)
		}

		return volumeUUIDs, nil
	}

	// Sets a volume's `volatile.uuid` config setting.
	setVolumeUUID := func(volumeID string, volumesConfigTable string, volumesConfigTableID string) error {
		_, err := d.State().DB.Cluster.DB().ExecContext(d.shutdownCtx, fmt.Sprintf(`
INSERT OR IGNORE INTO %s (%s, key, value) VALUES (?, "volatile.uuid", ?)
	`, volumesConfigTable, volumesConfigTableID), volumeID, uuid.New().String())
		return err
	}

	// Get the "volatile.uuid" setting of all storage volumes.
	volumeUUIDs, err := getVolumeUUIDs("storage_volumes", "storage_volumes_config", "storage_volume_id")
	if err != nil {
		return err
	}

	// Set a new "volatile.uuid" for all volumes which are missing the config key.
	for _, volumeUUID := range volumeUUIDs {
		if volumeUUID.value == nil {
			err := setVolumeUUID(volumeUUID.id, "storage_volumes_config", "storage_volume_id")
			if err != nil {
				return err
			}
		}
	}

	// Get the "volatile.uuid" setting of all storage volume snapshots.
	volumeSnapshotsUUIDs, err := getVolumeUUIDs("storage_volumes_snapshots", "storage_volumes_snapshots_config", "storage_volume_snapshot_id")
	if err != nil {
		return err
	}

	// Set a new "volatile.uuid" for all volumes which are missing the config key.
	for _, volumeSnapshotUUID := range volumeSnapshotsUUIDs {
		if volumeSnapshotUUID.value == nil {
			err := setVolumeUUID(volumeSnapshotUUID.id, "storage_volumes_snapshots_config", "storage_volume_snapshot_id")
			if err != nil {
				return err
			}
		}
	}

	// Get the "volatile.uuid" setting of all storage buckets.
	bucketUUIDs, err := getVolumeUUIDs("storage_buckets", "storage_buckets_config", "storage_bucket_id")
	if err != nil {
		return err
	}

	// Set a new "volatile.uuid" for all buckets which are missing the config key.
	for _, bucketUUID := range bucketUUIDs {
		if bucketUUID.value == nil {
			err := setVolumeUUID(bucketUUID.id, "storage_buckets_config", "storage_bucket_id")
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// patchStorageRenameCustomISOBlockVolumesV2 renames existing custom ISO volumes by adding the ".iso" suffix so they can be distinguished from regular custom block volumes.
// This patch doesn't use the patchGenericStorage function because the storage drivers themselves aren't aware of custom ISO volumes.
func patchStorageRenameCustomISOBlockVolumesV2(name string, d *Daemon) error {
	s := d.State()

	var pools []string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get all storage pool names.
		pools, err = tx.GetStoragePoolNames(ctx)

		return err
	})
	if err != nil {
		// Skip the rest of the patch if no storage pools were found.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}

		return fmt.Errorf("Failed getting storage pool names: %w", err)
	}

	volTypeCustom := dbCluster.StoragePoolVolumeTypeCustom
	customPoolVolumes := make(map[string][]*db.StorageVolume, 0)

	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, pool := range pools {
			// Get storage pool ID.
			poolID, err := tx.GetStoragePoolID(ctx, pool)
			if err != nil {
				return fmt.Errorf("Failed getting storage pool ID of pool %q: %w", pool, err)
			}

			// Get the pool's custom storage volumes.
			customVolumes, err := tx.GetStorageVolumes(ctx, false, db.StorageVolumeFilter{Type: &volTypeCustom, PoolID: &poolID})
			if err != nil {
				return fmt.Errorf("Failed getting custom storage volumes of pool %q: %w", pool, err)
			}

			if customPoolVolumes[pool] == nil {
				customPoolVolumes[pool] = []*db.StorageVolume{}
			}

			customPoolVolumes[pool] = append(customPoolVolumes[pool], customVolumes...)
		}

		return nil
	})
	if err != nil {
		return err
	}

	isSelectedPatchMember, err := selectedPatchClusterMember(s)
	if err != nil {
		return err
	}

	for poolName, volumes := range customPoolVolumes {
		// Load storage pool.
		p, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return fmt.Errorf("Failed loading pool %q: %w", poolName, err)
		}

		isRemotePool := p.Driver().Info().Remote

		// Ensure the renaming is done only on the selected patch cluster member for remote storage pools.
		if isRemotePool && !isSelectedPatchMember {
			continue
		}

		for _, vol := range volumes {
			// Skip volumes on local pools that are on other servers.
			if !isRemotePool && s.ServerClustered && vol.Location != s.ServerName {
				continue
			}

			// Exclude non-ISO custom volumes.
			if vol.ContentType != dbCluster.StoragePoolVolumeContentTypeNameISO {
				continue
			}

			// The existing volume using the actual *.iso suffix has ContentTypeISO.
			existingVol := storageDrivers.NewVolume(p.Driver(), p.Name(), storageDrivers.VolumeTypeCustom, storageDrivers.ContentTypeISO, project.StorageVolume(vol.Project, vol.Name), nil, nil)

			hasVol, err := p.Driver().HasVolume(existingVol)
			if err != nil {
				return fmt.Errorf("Failed to check if volume %q exists in pool %q: %w", existingVol.Name(), p.Name(), err)
			}

			// patchStorageRenameCustomISOBlockVolumes might have already set the *.iso suffix.
			// Check if the storage volume isn't already renamed.
			if hasVol {
				continue
			}

			// We need to use ContentTypeBlock here in order for the driver to figure out the correct (old) location.
			oldVol := storageDrivers.NewVolume(p.Driver(), p.Name(), storageDrivers.VolumeTypeCustom, storageDrivers.ContentTypeBlock, project.StorageVolume(vol.Project, vol.Name), nil, nil)

			err = p.Driver().RenameVolume(oldVol, fmt.Sprintf("%s.iso", oldVol.Name()), nil)
			if err != nil {
				return fmt.Errorf("Failed to rename volume %q in pool %q: %w", oldVol.Name(), p.Name(), err)
			}
		}
	}

	return nil
}

// patchStorageUnsetInvalidBlockSettingsV2 removes invalid block settings from LVM and Ceph RBD volumes.
// Its using an idempotent SQL query.
func patchStorageUnsetInvalidBlockSettingsV2(_ string, d *Daemon) error {
	// Use a subquery to get all volumes matching the criteria
	// as dqlite doesn't understand the `DELETE FROM xyz JOIN ...` syntax.
	_, err := d.State().DB.Cluster.DB().ExecContext(d.shutdownCtx, `
DELETE FROM storage_volumes_config
	WHERE storage_volumes_config.storage_volume_id IN (
		SELECT storage_volumes.id FROM storage_volumes
			LEFT JOIN storage_pools ON storage_pools.id = storage_volumes.storage_pool_id
				WHERE storage_volumes.type = 2
				AND storage_volumes.content_type = 1
				AND storage_pools.driver IN ("lvm", "ceph")
	)
	AND storage_volumes_config.key IN ("block.filesystem", "block.mount_options")
	`)
	return err
}

// patchRemoveCoreTrustPassword removes the core.trust_password config key from the cluster.
func patchRemoveCoreTrustPassword(_ string, d *Daemon) error {
	s := d.State()
	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateClusterConfig(map[string]string{
			"core.trust_password": "",
		})
	})
	if err != nil {
		return fmt.Errorf("Failed to remove core.trust_password config key: %w", err)
	}

	return nil
}

// patchEntityTypeInstanceSnapshotOnDeleteTriggerTypoFix drops the misspelled on_instance_snaphot_delete trigger on all cluster members, if it exists.
func patchEntityTypeInstanceSnapshotOnDeleteTriggerTypoFix(_ string, d *Daemon) error {
	var err error
	s := d.State()
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err = tx.Tx().Exec(`DROP TRIGGER IF EXISTS on_instance_snaphot_delete`)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to remove trigger: %w", err)
	}

	return nil
}

// patchInstanceRemoveVolatileLastStateIPAddresses removes the volatile.*.last_state.ip_addresses config key from instances.
func patchInstanceRemoveVolatileLastStateIPAddresses(_ string, d *Daemon) error {
	s := d.State()

	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		foundCandidateKey := func(k string) bool {
			if strings.HasPrefix(k, "volatile.") && strings.HasSuffix(k, ".last_state.ip_addresses") {
				return true
			}

			return false
		}

		// Get instances on this member.
		return tx.InstanceList(ctx, func(dbInst db.InstanceArgs, p api.Project) error {
			l := logger.AddContext(logger.Ctx{"project": dbInst.Project, "inst": dbInst.Name})

			for k := range dbInst.Config {
				if !foundCandidateKey(k) {
					continue
				}

				// Remove found config key.
				changes := map[string]string{
					k: "",
				}

				l.Debug("Removing config key from instance", logger.Ctx{"key": k})
				err := tx.UpdateInstanceConfig(dbInst.ID, changes)
				if err != nil {
					return fmt.Errorf("Failed removing config key %q for instance %q (Project %q): %w", k, dbInst.Name, dbInst.Project, err)
				}
			}

			// Get snapshots for instance so we can check those too.
			dbSnaps, err := tx.GetInstanceSnapshotsWithName(ctx, dbInst.Project, dbInst.Name)
			if err != nil {
				return fmt.Errorf("Failed getting snapshots for %q (Project %q): %w", dbInst.Name, dbInst.Project, err)
			}

			for _, dbSnap := range dbSnaps {
				snapConfig, err := dbCluster.GetInstanceSnapshotConfig(ctx, tx.Tx(), dbSnap.ID)
				if err != nil {
					return err
				}

				for k := range snapConfig {
					if !foundCandidateKey(k) {
						continue
					}

					// Remove found config key.
					changes := map[string]string{
						k: "",
					}

					l.Debug("Removing config key from instance snapshot", logger.Ctx{"snapshot": dbSnap.Name, "key": k})
					err := tx.UpdateInstanceSnapshotConfig(dbSnap.ID, changes)
					if err != nil {
						return fmt.Errorf("Failed removing config key %q for instance %q (Project %q): %w", k, dbSnap.Name, dbSnap.Project, err)
					}
				}
			}

			return nil
		}, dbCluster.InstanceFilter{Node: &s.ServerName})
	})
	if err != nil {
		return fmt.Errorf("Failed removing volatile.*.last_state.ip_addresses config keys: %w", err)
	}

	return nil
}

// patchContainerUpdateStopHook updates running container stop hooks.
func patchContainerUpdateStopHook(_ string, d *Daemon) error {
	// Only relevant when running in the LXD snap package.
	if !shared.InSnap() {
		return nil
	}

	s := d.State()

	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get containers on this member.
		return tx.InstanceList(ctx, func(dbInst db.InstanceArgs, p api.Project) error {
			l := logger.AddContext(logger.Ctx{"project": dbInst.Project, "inst": dbInst.Name})

			l.Warn("Updating container stop hooks")

			cname := project.Instance(dbInst.Project, dbInst.Name)

			cc, err := liblxc.NewContainer(cname, s.OS.LxcPath)
			if err != nil {
				return fmt.Errorf("Failed loading container %q (project %q): %w", dbInst.Name, dbInst.Project, err)
			}

			configPath := filepath.Join(shared.LogPath(cname), "lxc.conf")

			err = cc.LoadConfigFile(configPath)
			if err != nil {
				return fmt.Errorf("Failed to load container %q (project %q) config %q: %w", dbInst.Name, dbInst.Project, configPath, err)
			}

			l.Warn("tomp lxc.hook.stop")
			err = cc.ClearConfigItem("lxc.hook.stop")
			if err != nil {
				return err
			}

			l.Warn("tomp lxc.hook.post-stop")
			err = cc.ClearConfigItem("lxc.hook.post-stop")
			if err != nil {
				return err
			}

			err = cc.SetConfigItem("lxc.hook.stop", fmt.Sprintf("%s callhook %s %s %s stopns", "/snap/lxd/current/bin/lxd-lxc-stop", shared.VarPath(""), strconv.Quote(dbInst.Project), strconv.Quote(dbInst.Name)))
			if err != nil {
				return fmt.Errorf("Failed setting container %q (project %q) lxc.hook.stop: %w", dbInst.Name, dbInst.Project, err)

			}

			err = cc.SetConfigItem("lxc.hook.post-stop", fmt.Sprintf("%s callhook %s %s %s stop", "/snap/lxd/current/bin/lxd-lxc-stop", shared.VarPath(""), strconv.Quote(dbInst.Project), strconv.Quote(dbInst.Name)))
			if err != nil {
				return fmt.Errorf("Failed setting container %q (project %q) lxc.hook.post-stop: %w", dbInst.Name, dbInst.Project, err)
			}

			l.Warn(fmt.Sprintf("tomp %+v", cc.ConfigItem("lxc.hook.stop")))
			l.Warn(fmt.Sprintf("tomp %+v", cc.ConfigItem("lxc.hook.post-stop")))

			err = cc.SaveConfigFile(configPath)
			if err != nil {
				return fmt.Errorf("Failed saving container %q (project %q) config %q: %w", dbInst.Name, dbInst.Project, configPath, err)
			}

			_ = cc.Release()

			return nil
		}, dbCluster.InstanceFilter{Node: &s.ServerName, Type: instancetype.Container.Filter()})
	})
	if err != nil {
		return fmt.Errorf("Failed updating container stop hooks: %w", err)
	}

	return nil
}

// Patches end here
