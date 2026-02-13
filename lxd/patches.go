package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/lxd/cluster"
	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/config"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/device/filters"
	instanceDrivers "github.com/canonical/lxd/lxd/instance/drivers"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/network"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
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
	{name: "entity_type_identity_certificate_split", stage: patchPreLoadClusterConfig, run: patchSplitIdentityCertificateEntityTypes},
	{name: "storage_unset_powerflex_sdt_setting", stage: patchPostDaemonStorage, run: patchUnsetPowerFlexSDTSetting},
	{name: "oidc_groups_claim_scope", stage: patchPreLoadClusterConfig, run: patchOIDCGroupsClaimScope},
	{name: "remove_backupsimages_symlinks", stage: patchPostDaemonStorage, run: patchRemoveBackupsImagesSymlinks},
	{name: "move_images_storage", stage: patchPostDaemonStorage, run: patchMoveBackupsImagesStorage},
	{name: "cluster_config_volatile_uuid", stage: patchPreLoadClusterConfig, run: patchClusterConfigVolatileUUID},
	{name: "storage_update_powerflex_clone_copy_setting", stage: patchPostDaemonStorage, run: patchUpdatePowerFlexCloneCopySetting},
	{name: "storage_update_powerflex_snapshot_prefix", stage: patchPostDaemonStorage, run: patchUpdatePowerFlexSnapshotPrefix},
	{name: "config_remove_instances_placement_scriptlet", stage: patchPreLoadClusterConfig, run: patchRemoveInstancesPlacementScriptlet},
	{name: "event_entitlement_rename", stage: patchPreLoadClusterConfig, run: patchEventEntitlementNames},
	{name: "pool_fix_default_permissions", stage: patchPostDaemonStorage, run: patchDefaultStoragePermissions},
	{name: "storage_unset_cephfs_pristine_setting", stage: patchPostDaemonStorage, run: patchUnsetCephFSPristineSetting},
	{name: "update_volatile_attached_volumes_format", stage: patchPostDaemonStorage, run: patchUpdateVolatileAttachedVolumesFormat},
	{name: "storage_unset_ceph_force_reuse_setting", stage: patchPostDaemonStorage, run: patchUnsetCephForceReuseSetting},
	{name: "vm_rename_security_csm", stage: patchPostDaemonStorage, run: patchVMRenameSecurityCSM},
	{name: "vm_set_max_bus_ports", stage: patchPostDaemonStorage, run: patchVMSetMaxBusPorts},
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

		if slices.Contains(appliedPatches, patch.name) {
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
		return false, errors.New("Clustered but no member found")
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

		logger.Info("Trusted server certificates found in trust store for all cluster members")
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
		// Get state on every iteration in case of change, since this loop can run indefinitely.
		s := d.State()

		// Only apply patch if schema needs it.
		var schemaSQL string
		row := s.DB.Cluster.DB().QueryRow("SELECT sql FROM sqlite_master WHERE name = 'nodes'")
		err := row.Scan(&schemaSQL)
		if err != nil {
			return err
		}

		if strings.Contains(schemaSQL, "id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL") {
			logger.Debugf(`Skipping %q patch as "nodes" table id column already AUTOINCREMENT`, name)
			return nil // Nothing to do.
		}

		leaderInfo, err := s.LeaderInfo()
		if err != nil {
			return err
		}

		if leaderInfo.Leader {
			break // Apply change on leader node (or standalone node).
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

				logger.Debugf("Renaming config key %q to %q for VM %q (project %q)", oldUUIDKey, newUUIDKey, inst.Name, inst.Project)
				err := tx.UpdateInstanceConfig(inst.ID, changes)
				if err != nil {
					return fmt.Errorf("Failed renaming config key %q to %q for VM %q (project %q): %w", oldUUIDKey, newUUIDKey, inst.Name, inst.Project, err)
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

					logger.Debugf("Renaming config key %q to %q for VM %q (project %q)", oldUUIDKey, newUUIDKey, snap.Name, snap.Project)
					err = tx.UpdateInstanceSnapshotConfig(snap.ID, changes)
					if err != nil {
						return fmt.Errorf("Failed renaming config key %q to %q for VM %q (project %q): %w", oldUUIDKey, newUUIDKey, snap.Name, snap.Project, err)
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
	backupsPathBase := d.State().BackupsStoragePath("")
	if !shared.PathExists(backupsPathBase) {
		return nil // Nothing to do, no backups directory.
	}

	backupsPath := filepath.Join(backupsPathBase, "instances")

	err := os.MkdirAll(backupsPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed creating instances backup directory %q: %w", backupsPath, err)
	}

	backups, err := os.ReadDir(backupsPathBase)
	if err != nil {
		return fmt.Errorf("Failed listing existing backup directory %q: %w", backupsPathBase, err)
	}

	for _, backupDir := range backups {
		if backupDir.Name() == "instances" || strings.HasPrefix(backupDir.Name(), backup.WorkingDirPrefix) {
			continue // Don't try and move our new instances directory or temporary directories.
		}

		oldPath := filepath.Join(backupsPathBase, backupDir.Name())
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

			_, err = shared.RunCommand(d.shutdownCtx, "zfs", "set", "lxd:content_type="+vol.ContentType, zfsVolName)
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

	var volType dbCluster.StoragePoolVolumeType

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

			switch vol.Type {
			case dbCluster.StoragePoolVolumeTypeNameVM:
				volType = volTypeVM
			case dbCluster.StoragePoolVolumeTypeNameCustom:
				volType = volTypeCustom
			default:
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

			err = p.Driver().RenameVolume(oldVol, oldVol.Name()+".iso", nil)
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
		_, err := tx.Tx().ExecContext(ctx, `
			DELETE FROM instances_config WHERE id IN(
				SELECT instances_config.id
				FROM instances_config
				JOIN instances ON instances.id = instances_config.instance_id
				JOIN nodes ON nodes.id = instances.node_id
				WHERE key LIKE 'volatile.%.last_state.ip_addresses'
				AND nodes.name = ?
			)
		`, s.ServerName)
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, `
			DELETE FROM instances_snapshots_config WHERE id IN(
				SELECT instances_snapshots_config.id
				FROM instances_snapshots_config
				JOIN instances_snapshots ON instances_snapshots.id = instances_snapshots_config.instance_snapshot_id
				JOIN instances ON instances.id = instances_snapshots.instance_id
				JOIN nodes ON nodes.id = instances.node_id
				WHERE key LIKE 'volatile.%.last_state.ip_addresses'
				AND nodes.name = ?
			)
		`, s.ServerName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed removing volatile.*.last_state.ip_addresses config keys: %w", err)
	}

	return nil
}

func patchSplitIdentityCertificateEntityTypes(_ string, d *Daemon) error {
	s := d.State()
	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Notes:
		// - We don't need to handle warnings, there are no warnings for identities.
		// - No permissions have been created for OIDC identities against the certificate entity type, because the database
		//   definition for the certificate entity type ensures that the authentication method is TLS.
		// - We only need to fix permissions defined with the "identity" entity type that are certificates.

		// Select all permissions with entity type = "identity", that really are certificates (auth_method = "tls")
		// and set their entity type to "certificate" instead. Use "UPDATE OR REPLACE" in case of UNIQUE constraint violation.
		stmt := `
UPDATE OR REPLACE auth_groups_permissions
	SET entity_type = ?
	WHERE id IN (
	    SELECT auth_groups_permissions.id FROM auth_groups_permissions
			JOIN identities ON auth_groups_permissions.entity_id = identities.id
			WHERE auth_groups_permissions.entity_type = ?
			AND identities.auth_method = ?
	)
`

		// Set entity type to:
		certificateEntityTypeCode, _ := dbCluster.EntityType(entity.TypeCertificate).Value()
		// where entity type is:
		identityEntityTypeCode, _ := dbCluster.EntityType(entity.TypeIdentity).Value()
		// and authentication method is:
		tlsAuthMethodCode, _ := dbCluster.AuthMethod(api.AuthenticationMethodTLS).Value()
		_, err := tx.Tx().Exec(stmt, []any{certificateEntityTypeCode, identityEntityTypeCode, tlsAuthMethodCode}...)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to redefine certificate and identity entity types: %w", err)
	}

	return nil
}

// patchUnsetPowerFlexSDTSetting unsets the powerflex.sdt setting from all storage pools configs.
// The address used inside the config key was populated for all PowerFlex storage pools using the nvme mode.
// The single address was used together with the "nvme connect-all" command to discover the remaining SDTs to connect to all of them.
// Unsetting this key, discovering all SDTs from PowerFlex REST API and connecting to all of them using
// the "nvme connect" command has the exact same effect.
func patchUnsetPowerFlexSDTSetting(_ string, d *Daemon) error {
	_, err := d.State().DB.Cluster.DB().ExecContext(d.shutdownCtx, `
DELETE FROM storage_pools_config WHERE key = "powerflex.sdt"
	`)
	return err
}

// patchOIDCGroupsClaimScope adds the contents of oidc.groups.claim to the new configuration for oidc.scopes if present.
// The oidc.groups.claim value was initially added to scopes but shouldn't have been. This patch will allow users with
// working identity provider group mappings to continue using them by continuing to request the claim as an additional
// scope.
func patchOIDCGroupsClaimScope(_ string, d *Daemon) error {
	err := d.State().DB.Cluster.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get current configuration.
		globalConfig, err := clusterConfig.Load(ctx, tx)
		if err != nil {
			return err
		}

		// Get the groups claim and scopes (these will just be the default values at the time of the patch)
		_, _, _, scopes, _, groupsClaim := globalConfig.OIDCServer()

		// If the groups claim is not set, or this patch was already run on another member and the groups claim is
		// already present in the list of scopes, then there is nothing to do.
		if groupsClaim == "" || slices.Contains(scopes, groupsClaim) {
			return nil
		}

		// Add the groups claim as an additional scope.
		// The groups claim still needs to be set to extract group values from the token claims or userinfo.
		oidcScopes := append(scopes, groupsClaim)
		_, err = globalConfig.Patch(tx, map[string]string{
			"oidc.scopes": strings.Join(oidcScopes, " "),
		})

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to configure oidc.groups.claim as an OIDC scope: %w", err)
	}

	return nil
}

// Remove shared.VarPath("backups") and shared.VarPath("images") symlinks.
func patchRemoveBackupsImagesSymlinks(_ string, d *Daemon) error {
	dirs := []string{
		shared.VarPath("backups"),
		shared.VarPath("images"),
	}

	for _, dir := range dirs {
		info, err := os.Lstat(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // Nothing to do, symlink doesn't exist
			}

			return fmt.Errorf("Failed to call Lstat() on %q: %w", dir, err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			// Remove the symlink.
			err = os.Remove(dir)
			if err != nil {
				return fmt.Errorf("Failed to delete storage symlink at %q: %w", dir, err)
			}
		}
	}

	return nil
}

// If storage.images_volume is set, move images into an `images` subfolder.
func patchMoveBackupsImagesStorage(name string, d *Daemon) error {
	moveStorage := func(storageType config.DaemonStorageType, destPath string) error {
		sourcePath, dirName := filepath.Split(destPath)

		err := os.MkdirAll(destPath, 0700)
		if err != nil {
			return fmt.Errorf("Failed creating directory %q: %w", destPath, err)
		}

		items, err := os.ReadDir(sourcePath)
		if err != nil {
			return fmt.Errorf("Failed listing existing directory %q: %w", sourcePath, err)
		}

		for _, item := range items {
			if item.Name() == dirName {
				continue // Don't try and move our new directory.
			}

			oldPath := filepath.Join(sourcePath, item.Name())
			newPath := filepath.Join(destPath, item.Name())
			logger.Debugf("Moving %s from %q to %q", storageType, oldPath, newPath)
			err = os.Rename(oldPath, newPath)
			if err != nil {
				return fmt.Errorf("Failed moving file from %q to %q: %w", oldPath, newPath, err)
			}
		}

		return nil
	}

	if d.localConfig.StorageImagesVolume("") != "" {
		err := moveStorage(config.DaemonStorageTypeImages, d.State().ImagesStoragePath(""))
		if err != nil {
			return err
		}
	}

	if d.localConfig.StorageBackupsVolume("") != "" {
		err := moveStorage(config.DaemonStorageTypeBackups, d.State().BackupsStoragePath(""))
		if err != nil {
			return err
		}
	}

	return nil
}

// patchClusterConfigVolatileUUID checks if the clusterUUID is defined and if not, generates a new v7 UUID and saves it
// to the `config` table under `volatile.uuid`. Note that this means existing deployments will have a 'volatile.uuid'
// that does not match the contents of `$LXD_DIR/server.uuid`, whereas new deployments will have a 'volatile.uuid' that
// matches the contents of 'server.uuid' on the member that initially bootstrapped the cluster.
func patchClusterConfigVolatileUUID(name string, d *Daemon) error {
	return d.db.Cluster.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get current configuration.
		globalConfig, err := clusterConfig.Load(ctx, tx)
		if err != nil {
			return err
		}

		// If the cluster UUID has been set, return.
		if globalConfig.ClusterUUID() != "" {
			return nil
		}

		clusterUUID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("Failed to generate a cluster UUID: %w", err)
		}

		// Otherwise, insert the server UUID into the database.
		_, err = tx.Tx().Exec(`INSERT INTO config (key, value) VALUES ('volatile.uuid', ?)`, clusterUUID.String())
		if err != nil {
			return err
		}

		return nil
	})
}

// patchUpdatePowerFlexCloneCopySetting checks whether or not the 'powerflex.clone_copy' setting is present on any applicable storage pool.
// If set it's getting replaced with the new 'powerflex.snapshot_copy' setting which also inverts the original value.
func patchUpdatePowerFlexCloneCopySetting(_ string, d *Daemon) error {
	err := d.State().DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get all storage pool names.
		pools, _, err := tx.GetStoragePools(ctx, nil)
		if err != nil {
			// Skip the rest of the patch if no storage pools were found.
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				return nil
			}

			return err
		}

		for _, pool := range pools {
			// Skip all pools which don't use the powerflex driver.
			if pool.Driver != "powerflex" {
				continue
			}

			if pool.Config["powerflex.clone_copy"] != "" {
				if shared.IsFalse(pool.Config["powerflex.clone_copy"]) {
					pool.Config["powerflex.snapshot_copy"] = "true"
				} else if shared.IsTrue(pool.Config["powerflex.clone_copy"]) {
					pool.Config["powerflex.snapshot_copy"] = "false"
				}

				// Delete the old config key.
				delete(pool.Config, "powerflex.clone_copy")

				// Persist the changes.
				err = tx.UpdateStoragePool(ctx, pool.Name, pool.Description, pool.Config)
				if err != nil {
					return fmt.Errorf("Failed updating storage pool %q: %w", pool.Name, err)
				}
			}
		}

		return nil
	})

	return err
}

// patchUpdatePowerFlexSnapshotPrefix adds the snapshot prefix to snapshots which actually belong to
// LXD volumes and were not created through the powerflex.snapshot_copy=true setting.
func patchUpdatePowerFlexSnapshotPrefix(_ string, d *Daemon) error {
	s := d.State()

	isSelectedPatchMember, err := selectedPatchClusterMember(s)
	if err != nil {
		return err
	}

	// Only run the patch on the selected member to ensure the change is only ever performed once on the
	// remote storage which is shared across all cluster members.
	if !isSelectedPatchMember {
		return nil
	}

	// Cache a list of snapshots, by pool and volume.
	poolVolumesSnapshots := make(map[string]map[*db.StorageVolume][]db.StorageVolumeArgs)

	err = d.State().DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get all storage pool names.
		pools, _, err := tx.GetStoragePools(ctx, nil)
		if err != nil {
			// Skip the rest of the patch if no storage pools were found.
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				return nil
			}

			return err
		}

		for poolID, pool := range pools {
			// Skip all pools which don't use the powerflex driver.
			if pool.Driver != "powerflex" {
				continue
			}

			poolVolumesSnapshots[pool.Name] = make(map[*db.StorageVolume][]db.StorageVolumeArgs)

			volumes, err := tx.GetStorageVolumes(ctx, false, db.StorageVolumeFilter{PoolID: &poolID})
			if err != nil {
				return fmt.Errorf("Failed getting storage volumes for pool %q: %w", pool.Name, err)
			}

			for _, volume := range volumes {
				volType, err := dbCluster.StoragePoolVolumeTypeFromName(volume.Type)
				if err != nil {
					return err
				}

				snapshots, err := tx.GetLocalStoragePoolVolumeSnapshotsWithType(ctx, volume.Project, volume.Name, volType, poolID)
				if err != nil {
					return err
				}

				poolVolumesSnapshots[pool.Name][volume] = snapshots
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Iterate over the pools, volumes and snapshots.
	for poolName, volumesSnapshots := range poolVolumesSnapshots {
		p, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return fmt.Errorf("Failed loading pool %q: %w", poolName, err)
		}

		var snapVols []storageDrivers.Volume

		for volume, snapshots := range volumesSnapshots {
			for _, snapshot := range snapshots {
				snapshotName := storageDrivers.GetSnapshotVolumeName(volume.Name, snapshot.Name)
				snapshotStorageName := project.StorageVolume(volume.Project, snapshotName)

				dbVolType, err := dbCluster.StoragePoolVolumeTypeFromName(volume.Type)
				if err != nil {
					return err
				}

				// Get the right storage level volume type.
				// The volume might either be a container, VM or custom snapshot.
				volType := storagePools.VolumeDBTypeToType(dbVolType)

				snapVol := storageDrivers.NewVolume(p.Driver(), p.Name(), volType, storageDrivers.ContentType(volume.ContentType), snapshotStorageName, snapshot.Config, p.ToAPI().Config)
				snapVols = append(snapVols, snapVol)
			}
		}

		// Invoke the driver level patch function.
		// We are passing a list of volumes which require patching the snapshot prefix.
		err = storageDrivers.PatchUpdatePowerFlexSnapshotPrefix(p.Driver(), snapVols)
		if err != nil {
			return fmt.Errorf("Failed patching volume snapshot prefixes on pool %q: %w", poolName, err)
		}
	}

	return nil
}

// patchRemoveInstancesPlacementScriptlet removes the 'instances.placement.scriptlet' config key from the cluster.
// If the key is set, its value is copied to 'user.instances.placement.scriptlet'.
func patchRemoveInstancesPlacementScriptlet(name string, d *Daemon) error {
	s := d.State()
	return s.DB.Cluster.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		config, err := tx.Config(ctx)
		if err != nil {
			return err
		}

		const oldKey = "instances.placement.scriptlet"
		const newKey = "user.instances.placement.scriptlet"

		oldVal, ok := config[oldKey]
		if !ok || oldVal == "" {
			return nil
		}

		updates := map[string]string{
			oldKey: "",
		}

		if config[newKey] == "" {
			updates[newKey] = oldVal
		}

		return tx.UpdateClusterConfig(updates)
	})
}

func patchEventEntitlementNames(name string, d *Daemon) error {
	s := d.State()
	return s.DB.Cluster.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		q := `UPDATE auth_groups_permissions SET entitlement = ? WHERE entitlement = ? AND entity_type = ?`

		// Rename `can_view_privileged_events` on `server` to `can_view_events`.
		_, err := tx.Tx().ExecContext(ctx, q, auth.EntitlementCanViewEvents, "can_view_privileged_events", dbCluster.EntityType(entity.TypeServer))
		if err != nil {
			return err
		}

		return nil
	})
}

// patchDefaultStoragePermissions re-applies the default modes to all storage pools.
func patchDefaultStoragePermissions(_ string, d *Daemon) error {
	s := d.State()

	var pools []string

	err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
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

	for _, pool := range pools {
		for _, volEntry := range storageDrivers.BaseDirectories {
			for _, volDir := range volEntry.Paths {
				path := storageDrivers.GetPoolMountPath(pool) + "/" + volDir

				err := os.Chmod(path, volEntry.Mode)
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("Failed to set directory mode %q: %w", path, err)
				}
			}
		}
	}

	return nil
}

// patchUnsetCephFSPristineSetting unsets the volatile.pool.pristine setting from all CephFS storage pool's configs.
func patchUnsetCephFSPristineSetting(_ string, d *Daemon) error {
	_, err := d.State().DB.Cluster.DB().ExecContext(d.shutdownCtx, `
		DELETE FROM storage_pools_config
			WHERE key = "volatile.pool.pristine"
			AND storage_pool_id IN (
				SELECT id FROM storage_pools
					WHERE driver = "cephfs"
			)
	`)
	return err
}

// patchUpdateVolatileAttachedVolumesFormat updates "volatile.attached_volumes" from old format (map of volume UUID -> snapshot UUID) to new format (map of device_name -> snapshot_UUID).
func patchUpdateVolatileAttachedVolumesFormat(_ string, d *Daemon) error {
	s := d.State()

	// Only run on a single cluster member to avoid concurrent updates.
	isSelectedMember, err := selectedPatchClusterMember(s)
	if err != nil {
		return err
	}

	if !isSelectedMember {
		return nil
	}

	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		type snapshotData struct {
			snapshotID                   int
			instanceID                   int
			name                         string
			projectName                  string
			volatileAttachedVolumesValue string
		}

		var snapshots []snapshotData

		// Query to get all instance snapshots with "volatile.attached_volumes".
		q := `
SELECT
	instances_snapshots.id,
	instances_snapshots.name,
	instances_snapshots.instance_id,
	projects.name,
	instances_snapshots_config.value
FROM instances_snapshots_config
JOIN instances_snapshots ON instances_snapshots.id = instances_snapshots_config.instance_snapshot_id
JOIN instances ON instances.id = instances_snapshots.instance_id
JOIN projects ON projects.id = instances.project_id
WHERE instances_snapshots_config.key = "volatile.attached_volumes"
`
		err := query.Scan(ctx, tx.Tx(), q, func(scan func(dest ...any) error) error {
			var snap snapshotData

			err := scan(&snap.snapshotID, &snap.name, &snap.instanceID, &snap.projectName, &snap.volatileAttachedVolumesValue)
			if err != nil {
				return err
			}

			snapshots = append(snapshots, snap)

			return nil
		})
		if err != nil {
			return err
		}

		// isAlreadyNewFormat checks if "volatile.attached_volumes" is already in the new format.
		// New format uses device names as keys, old format uses volume UUIDs as keys.
		isAlreadyNewFormat := func(attachedVolumes map[string]string, snapshotDevices map[string]dbCluster.Device) bool {
			for key := range attachedVolumes {
				_, keyMatchesDeviceName := snapshotDevices[key]
				if keyMatchesDeviceName {
					return true
				}
			}

			return false
		}

		for _, snap := range snapshots {
			var volatileAttachedVolumes map[string]string
			err = json.Unmarshal([]byte(snap.volatileAttachedVolumesValue), &volatileAttachedVolumes)
			if err != nil {
				logger.Warn(`Failed parsing "volatile.attached_volumes", skipping snapshot`, logger.Ctx{"err": err, "snapshotName": snap.name, "snapshotID": snap.snapshotID, "instanceID": snap.instanceID, "project": snap.projectName})
				continue
			}

			// Get instance snapshot devices.
			devices, err := dbCluster.GetInstanceSnapshotDevices(ctx, tx.Tx(), snap.snapshotID)
			if err != nil {
				return fmt.Errorf("Failed getting instance snapshot %q devices: %w", snap.name, err)
			}

			if isAlreadyNewFormat(volatileAttachedVolumes, devices) {
				continue
			}

			// Convert from old format (volume UUID -> snapshot UUID) to new format (device name -> snapshot UUID).

			// Collect all volume UUIDs.
			uuids := make([]string, 0, len(volatileAttachedVolumes))
			for uuid := range volatileAttachedVolumes {
				uuids = append(uuids, uuid)
			}

			customType := dbCluster.StoragePoolVolumeTypeCustom

			filter := db.StorageVolumeFilter{
				UUIDs: uuids,
				Type:  &customType,
			}

			// Get all custom volumes with matching UUIDs.
			volumes, err := tx.GetStorageVolumes(ctx, true, filter)
			if err != nil {
				return fmt.Errorf("Failed getting storage volumes: %w", err)
			}

			type volKey struct {
				name string
				pool string
			}

			// Create a map of [volKey] -> volume UUID for looking up volumes by device config.
			volumeByPoolAndName := make(map[volKey]string)
			for _, vol := range volumes {
				volumeByPoolAndName[volKey{name: vol.Name, pool: vol.Pool}] = vol.Config["volatile.uuid"]
			}

			newVolatileAttachedVolumes := make(map[string]string, len(volatileAttachedVolumes))
			for name, dev := range devices {
				if !filters.IsCustomVolumeDisk(dev.Config) {
					continue
				}

				// Look up the volume UUID by pool and name.
				volumeUUID, found := volumeByPoolAndName[volKey{name: dev.Config["source"], pool: dev.Config["pool"]}]
				if !found {
					continue
				}

				// Look up the snapshot UUID by volume UUID.
				snapshotUUID, found := volatileAttachedVolumes[volumeUUID]
				if !found {
					continue
				}

				newVolatileAttachedVolumes[name] = snapshotUUID
			}

			// Skip if no volumes were converted.
			// This is a safety check and optimization to prevent an unnecessary write of an empty map, which could happen if the snapshot's "volatile.attached_volumes" references deleted volumes.
			if len(newVolatileAttachedVolumes) == 0 {
				continue
			}

			marshalled, err := json.Marshal(newVolatileAttachedVolumes)
			if err != nil {
				return fmt.Errorf(`Failed marshalling new "volatile.attached_volumes" format for instance snapshot %q: %w`, snap.name, err)
			}

			_, err = tx.Tx().ExecContext(ctx, `
UPDATE instances_snapshots_config
SET value = ?
WHERE instance_snapshot_id = ? AND key = "volatile.attached_volumes"
`, string(marshalled), snap.snapshotID)
			if err != nil {
				return fmt.Errorf(`Failed setting instance snapshot %q "volatile.attached_volumes": %w`, snap.name, err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf(`Failed updating "volatile.attached_volumes" to new format: %w`, err)
	}

	return nil
}

// patchUnsetCephForceReuseSetting unsets the ceph.osd.force_reuse setting from all storage pools' configs.
func patchUnsetCephForceReuseSetting(_ string, d *Daemon) error {
	_, err := d.State().DB.Cluster.DB().ExecContext(d.shutdownCtx, `
DELETE FROM storage_pools_config WHERE key = "ceph.osd.force_reuse"
	`)
	return err
}

// patchVMRenameSecurityCSM migrates VM boot config keys to boot.mode in instance, snapshot, and profile configs.
// Converts legacy keys:
// - security.csm=true -> boot.mode=bios.
// - security.secureboot=false -> boot.mode=uefi-nosecureboot.
// - default/no explicit secureboot -> boot.mode=uefi-secureboot.
func patchVMRenameSecurityCSM(name string, d *Daemon) error {
	oldCSMKey := "security.csm"
	oldSecureBootKey := "security.secureboot"
	newKey := "boot.mode"

	s := d.State()

	// Only run on a single cluster member to avoid concurrent updates.
	isSelectedMember, err := selectedPatchClusterMember(s)
	if err != nil {
		return err
	}

	if !isSelectedMember {
		return nil
	}

	return s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.InstanceList(ctx, func(inst db.InstanceArgs, p api.Project) error {
			if inst.Type != instancetype.VM {
				return nil
			}

			csmValue := inst.Config[oldCSMKey]
			secureBootValue := inst.Config[oldSecureBootKey]
			if csmValue != "" || secureBootValue != "" {
				targetMode := instancetype.BootModeUEFISecureBoot
				if shared.IsTrue(csmValue) {
					targetMode = instancetype.BootModeBIOS
				} else if shared.IsFalse(secureBootValue) {
					targetMode = instancetype.BootModeUEFINoSecureBoot
				}

				changes := map[string]string{}
				if csmValue != "" {
					changes[oldCSMKey] = "" // Remove old key.
				}

				if secureBootValue != "" {
					changes[oldSecureBootKey] = "" // Remove old key.
				}

				changes[newKey] = targetMode
				logger.Debugf("Converting VM %q (project %q) boot config to %q=%q", inst.Name, inst.Project, newKey, targetMode)

				err := tx.UpdateInstanceConfig(inst.ID, changes)
				if err != nil {
					return fmt.Errorf("Failed updating config for VM %q (project %q): %w", inst.Name, inst.Project, err)
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

				csmValue := config[oldCSMKey]
				secureBootValue := config[oldSecureBootKey]
				if csmValue != "" || secureBootValue != "" {
					targetMode := instancetype.BootModeUEFISecureBoot
					if shared.IsTrue(csmValue) {
						targetMode = instancetype.BootModeBIOS
					} else if shared.IsFalse(secureBootValue) {
						targetMode = instancetype.BootModeUEFINoSecureBoot
					}

					changes := map[string]string{}
					if csmValue != "" {
						changes[oldCSMKey] = "" // Remove old key.
					}

					if secureBootValue != "" {
						changes[oldSecureBootKey] = "" // Remove old key.
					}

					changes[newKey] = targetMode
					logger.Debugf("Converting VM snapshot %q (project %q) boot config to %q=%q", snap.Name, snap.Project, newKey, targetMode)

					err = tx.UpdateInstanceSnapshotConfig(snap.ID, changes)
					if err != nil {
						return fmt.Errorf("Failed updating config for VM snapshot %q (project %q): %w", snap.Name, snap.Project, err)
					}
				}
			}

			return nil
		})

		if err != nil {
			return err
		}

		profiles, err := dbCluster.GetProfiles(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, profile := range profiles {
			config, err := dbCluster.GetProfileConfig(ctx, tx.Tx(), profile.ID)
			if err != nil {
				return err
			}

			csmValue := config[oldCSMKey]
			secureBootValue := config[oldSecureBootKey]
			if csmValue == "" && secureBootValue == "" {
				continue
			}

			targetMode := instancetype.BootModeUEFISecureBoot
			if shared.IsTrue(csmValue) {
				targetMode = instancetype.BootModeBIOS
			} else if shared.IsFalse(secureBootValue) {
				targetMode = instancetype.BootModeUEFINoSecureBoot
			}

			if csmValue != "" {
				delete(config, oldCSMKey)
			}

			if secureBootValue != "" {
				delete(config, oldSecureBootKey)
			}

			config[newKey] = targetMode
			logger.Debugf("Converting profile %q (project %q) boot config to %q=%q", profile.Name, profile.Project, newKey, targetMode)

			err = dbCluster.UpdateProfileConfig(ctx, tx.Tx(), int64(profile.ID), config)
			if err != nil {
				return fmt.Errorf("Failed updating config for profile %q (project %q): %w", profile.Name, profile.Project, err)
			}
		}

		return nil
	})
}

// patchVMSetMaxBusPorts sets the "limits.max_bus_ports" config option for VMs that have more PCIe devices attached than
// the default value of "limits.max_bus_ports". It sets the value equal to the number of attached PCIe devices, so that
// the VM can start successfully.
func patchVMSetMaxBusPorts(_ string, d *Daemon) error {
	s := d.State()

	// Only run on a single cluster member to avoid concurrent updates.
	isSelectedMember, err := selectedPatchClusterMember(s)
	if err != nil {
		return err
	}

	if !isSelectedMember {
		return nil
	}

	// countPCIeDevices returns the number of attached PCIe devices.
	countPCIeDevices := func(config map[string]string) int {
		pciDevices := 0
		for key := range config {
			if strings.HasPrefix(key, "volatile.") && strings.HasSuffix(key, ".bus") {
				pciDevices++
			}
		}

		return pciDevices
	}

	return s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(inst db.InstanceArgs, project api.Project) error {
			if inst.Type != instancetype.VM {
				return nil
			}

			pcieDevices := countPCIeDevices(inst.Config)
			_, hasCustomLimitSet := inst.Config["limits.max_bus_ports"]

			// Only update the limit if the instance currently does not have it set
			// and the number of attached PCIe devices is higher than the default value.
			if !hasCustomLimitSet && pcieDevices > int(instanceDrivers.QEMUDefaultMaxBusPorts) {
				err := tx.UpdateInstanceConfig(inst.ID, map[string]string{"limits.max_bus_ports": strconv.Itoa(pcieDevices)})
				if err != nil {
					return fmt.Errorf("Failed setting config key %q to value %d for VM %q (project %q): %w", "limits.max_bus_ports", pcieDevices, inst.Name, inst.Project, err)
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

				pcieDevices := countPCIeDevices(config)
				_, hasCustomLimitSet := config["limits.max_bus_ports"]

				// Only update the limit if the instance snapshot currently does not have it set
				// and the number of attached PCIe devices is higher than the default value.
				if !hasCustomLimitSet && pcieDevices > int(instanceDrivers.QEMUDefaultMaxBusPorts) {
					err = tx.UpdateInstanceSnapshotConfig(snap.ID, map[string]string{"limits.max_bus_ports": strconv.Itoa(pcieDevices)})
					if err != nil {
						return fmt.Errorf("Failed setting config key %q to value %d for VM snapshot %q (project %q): %w", "limits.max_bus_ports", pcieDevices, snap.Name, snap.Project, err)
					}
				}
			}

			return nil
		})
	})
}

// Patches end here
