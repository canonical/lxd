package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/rsync"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

type patchStage int

// Define the stages that patches can run at.
const (
	patchNoStageSet patchStage = iota
	patchPreDaemonStorage
	patchPostDaemonStorage
	patchPostNetworks
)

/* Patches are one-time actions that are sometimes needed to update
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
}

type patch struct {
	name  string
	stage patchStage
	run   func(name string, d *Daemon) error
}

func (p *patch) apply(d *Daemon) error {
	logger.Infof("Applying patch %q", p.name)

	err := p.run(p.name, d)
	if err != nil {
		return fmt.Errorf("Failed applying patch %q: %w", p.name, err)
	}

	err = d.db.MarkPatchAsApplied(p.name)
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

// patchesApplyPostDaemonStorage applies the patches that need to run after the daemon storage is initialised.
func patchesApply(d *Daemon, stage patchStage) error {
	appliedPatches, err := d.db.GetAppliedPatches()
	if err != nil {
		return err
	}

	for _, patch := range patches {
		if patch.stage == patchNoStageSet {
			return fmt.Errorf("Patch %q has no stage set: %d", patch.name, patch.stage)
		}

		if shared.StringInSlice(patch.name, appliedPatches) {
			continue
		}

		err := patch.apply(d)
		if err != nil {
			return err
		}
	}

	return nil
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
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		warnings, err := tx.GetWarnings(db.WarningFilter{})
		if err != nil {
			return err
		}

		for _, w := range warnings {
			if w.Node == "" {
				err = tx.DeleteWarning(w.UUID)
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
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return err
	}

	if !clustered {
		return nil
	}

	var serverName string
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		serverName, err = tx.GetLocalNodeName()
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
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		return cluster.EnsureServerCertificateTrusted(serverName, serverCert, tx)
	})
	if err != nil {
		return err
	}
	logger.Infof("Added local server certificate to global trust store for %q patch", name)

	// Check all other members have done the same.
	for {
		var err error
		var dbCerts []db.Certificate
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			dbCerts, err = tx.GetCertificates(db.CertificateFilter{})
			return err
		})
		if err != nil {
			return err
		}

		trustedServerCerts := make(map[string]*db.Certificate)

		for _, c := range dbCerts {
			if c.Type == db.CertificateTypeServer {
				trustedServerCerts[c.Name] = &c
			}
		}

		var nodes []db.NodeInfo
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			nodes, err = tx.GetNodes()
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}

		missingCerts := false
		for _, n := range nodes {
			if _, found := trustedServerCerts[n.Name]; !found {
				logger.Warnf("Missing trusted server certificate for cluster member %q", n.Name)
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
	updateCertificateCache(d)

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
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		projectNames, err = tx.GetProjectNames()
		return err
	})
	if err != nil {
		return err
	}

	// Get ACLs in projects.
	for _, projectName := range projectNames {
		aclNames, err := d.cluster.GetNetworkACLs(projectName)
		if err != nil {
			return err
		}

		for _, aclName := range aclNames {
			aclID, acl, err := d.cluster.GetNetworkACL(projectName, aclName)
			if err != nil {
				return err
			}

			modified := false

			// Remove the offending keys if found.
			if _, found := acl.Config["default.action"]; found {
				delete(acl.Config, "default.action")
				modified = true
			}

			if _, found := acl.Config["default.logged"]; found {
				delete(acl.Config, "default.logged")
				modified = true
			}

			// Write back modified config if needed.
			if modified {
				err = d.cluster.UpdateNetworkACL(aclID, &acl.NetworkACLPut)
				if err != nil {
					return fmt.Errorf("Failed updating network ACL %d: %w", aclID, err)
				}
			}
		}
	}

	return nil
}

// patchDBNodesAutoInc re-creates the nodes table id column as AUTOINCREMENT.
// Its done as a patch rather than a schema update so we can use PRAGMA foreign_keys = OFF without a transaction.
func patchDBNodesAutoInc(name string, d *Daemon) error {
	for {
		// Only apply patch if schema needs it.
		var schemaSQL string
		row := d.State().Cluster.DB().QueryRow("SELECT sql FROM sqlite_master WHERE name = 'nodes'")
		err := row.Scan(&schemaSQL)
		if err != nil {
			return err
		}

		if strings.Contains(schemaSQL, "id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL") {
			logger.Debugf(`Skipping %q patch as "nodes" table id column already AUTOINCREMENT`, name)
			return nil // Nothing to do.
		}

		// Only apply patch on leader, otherwise wait for it to be applied.
		clusterAddress, err := node.ClusterAddress(d.db)
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

		if clusterAddress == leaderAddress {
			break // Apply change on leader node.
		}

		logger.Warnf("Waiting for %q patch to be applied on leader cluster member", name)
		time.Sleep(time.Second)
	}

	// Apply patch.
	_, err := d.State().Cluster.DB().Exec(`
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

	return d.State().Cluster.InstanceList(nil, func(inst db.Instance, p db.Project, profiles []api.Profile) error {
		if inst.Type != instancetype.VM {
			return nil
		}

		return d.State().Cluster.Transaction(func(tx *db.ClusterTx) error {
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

			snaps, err := tx.GetInstanceSnapshotsWithName(inst.Project, inst.Name)
			if err != nil {
				return err
			}

			for _, snap := range snaps {
				uuid := snap.Config[oldUUIDKey]
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
	tx, err := d.cluster.Begin()
	if err != nil {
		return fmt.Errorf("Failed to begin transaction: %w", err)
	}

	revert.Add(func() { tx.Rollback() })

	// Fetch the IDs of all existing nodes.
	nodeIDs, err := query.SelectIntegers(tx, "SELECT id FROM nodes")
	if err != nil {
		return fmt.Errorf("Failed to get IDs of current nodes: %w", err)
	}

	// Fetch the IDs of all existing lvm pools.
	poolIDs, err := query.SelectIntegers(tx, "SELECT id FROM storage_pools WHERE driver='lvm'")
	if err != nil {
		return fmt.Errorf("Failed to get IDs of current lvm pools: %w", err)
	}

	for _, poolID := range poolIDs {
		// Fetch the config for this lvm pool and check if it has the lvm.thinpool_name.
		config, err := query.SelectConfig(
			tx, "storage_pools_config", "storage_pool_id=? AND node_id IS NULL", poolID)
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
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		projectNetworks, err := tx.GetCreatedNetworks()
		if err != nil {
			return err
		}

		for _, networks := range projectNetworks {
			for networkID, network := range networks {
				if network.Type != "bridge" {
					continue
				}

				if network.Config["bridge.mode"] != "fan" {
					continue
				}

				modified := false

				// Enable ipv4.nat if setting not specified.
				if _, found := network.Config["ipv4.nat"]; !found {
					modified = true
					network.Config["ipv4.nat"] = "true"
				}

				if modified {
					err = tx.UpdateNetwork(networkID, network.Description, network.Config)
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
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		projectNetworks, err := tx.GetCreatedNetworks()
		if err != nil {
			return err
		}

		for _, networks := range projectNetworks {
			for networkID, network := range networks {
				if network.Type != "ovn" {
					continue
				}

				modified := false

				// Ensure existing behaviour of having NAT enabled if IP address was set.
				if _, found := network.Config["ipv4.routes.external"]; found {
					modified = true
					delete(network.Config, "ipv4.routes.external")
				}

				if _, found := network.Config["ipv6.routes.external"]; found {
					modified = true
					delete(network.Config, "ipv6.routes.external")
				}

				if modified {
					err = tx.UpdateNetwork(networkID, network.Description, network.Config)
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
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		projectNetworks, err := tx.GetCreatedNetworks()
		if err != nil {
			return err
		}

		for _, networks := range projectNetworks {
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
					err = tx.UpdateNetwork(networkID, network.Description, network.Config)
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

	backups, err := ioutil.ReadDir(shared.VarPath("backups"))
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
	return d.State().Cluster.Transaction(func(tx *db.ClusterTx) error {
		nodes, err := tx.GetNodes()
		if err != nil {
			return err
		}
		for _, node := range nodes {
			err := tx.UpdateNodeRoles(node.ID, nil)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// patchNetworkClearBridgeVolatileHwaddr removes the unsupported `volatile.bridge.hwaddr` config key from networks.
func patchNetworkClearBridgeVolatileHwaddr(name string, d *Daemon) error {
	// Use project.Default, as bridge networks don't support projects.
	projectName := project.Default

	// Get the list of networks.
	networks, err := d.cluster.GetNetworks(projectName)
	if err != nil {
		return fmt.Errorf("Failed loading networks for network_clear_bridge_volatile_hwaddr patch: %w", err)
	}

	for _, networkName := range networks {
		_, net, _, err := d.cluster.GetNetworkInAnyState(projectName, networkName)
		if err != nil {
			return fmt.Errorf("Failed loading network %q for network_clear_bridge_volatile_hwaddr patch: %w", networkName, err)
		}

		if net.Config["volatile.bridge.hwaddr"] != "" {
			delete(net.Config, "volatile.bridge.hwaddr")
			err = d.cluster.UpdateNetwork(projectName, net.Name, net.Description, net.Config)
			if err != nil {
				return fmt.Errorf("Failed updating network %q for network_clear_bridge_volatile_hwaddr patch: %w", networkName, err)
			}
		}
	}

	return nil
}

// Patches end here

// Here are a couple of legacy patches that were originally in
// db_updates.go and were written before the new patch mechanism
// above. To preserve exactly their semantics we treat them
// differently and still apply them during the database upgrade. In
// principle they could be converted to regular patches like the ones
// above, however that seems an unnecessary risk at this moment. See
// also PR #3322.
//
// NOTE: don't add any legacy patch here, instead use the patches
// mechanism above.
var legacyPatches = map[int](func(tx *sql.Tx) error){
	11: patchUpdateFromV10,
	12: patchUpdateFromV11,
	16: patchUpdateFromV15,
	30: patchUpdateFromV29,
	31: patchUpdateFromV30,
}

func patchUpdateFromV10(_ *sql.Tx) error {
	// Logic was moved to Daemon.init().
	return nil
}

func patchUpdateFromV11(_ *sql.Tx) error {
	instances, err := instancesOnDisk(nil)
	if err != nil {
		return err
	}

	errors := 0

	// tomp TODO this whole patch seems to be oriented around snapshots, and yet instancesOnDisk doesn't
	// return snapshots, so it may do something unexpected/nothing.
	for _, inst := range instances {
		// Only interested in containers in default project.
		if inst.Type() != instancetype.Container || inst.Project() != project.Default {
			continue
		}

		cName := inst.Name()

		snapParentName, snapOnlyName, _ := shared.InstanceGetParentAndSnapshotName(cName)
		oldPath := shared.VarPath("containers", snapParentName, "snapshots", snapOnlyName)
		newPath := shared.VarPath("snapshots", snapParentName, snapOnlyName)
		if shared.PathExists(oldPath) && !shared.PathExists(newPath) {
			logger.Info(
				"Moving snapshot",
				log.Ctx{
					"snapshot": cName,
					"oldPath":  oldPath,
					"newPath":  newPath})

			// Rsync
			// containers/<container>/snapshots/<snap0>
			// to
			// snapshots/<container>/<snap0>
			output, err := rsync.LocalCopy(oldPath, newPath, "", true)
			if err != nil {
				logger.Error(
					"Failed rsync snapshot",
					log.Ctx{
						"snapshot": cName,
						"output":   string(output),
						"err":      err})
				errors++
				continue
			}

			// Remove containers/<container>/snapshots/<snap0>
			if err := os.RemoveAll(oldPath); err != nil {
				logger.Error(
					"Failed to remove the old snapshot path",
					log.Ctx{
						"snapshot": cName,
						"oldPath":  oldPath,
						"err":      err})

				// Ignore this error.
				// errors++
				// continue
			}

			// Remove /var/lib/lxd/containers/<container>/snapshots
			// if its empty.
			cPathParent := filepath.Dir(oldPath)
			if ok, _ := shared.PathIsEmpty(cPathParent); ok {
				os.Remove(cPathParent)
			}

		} // if shared.PathExists(oldPath) && !shared.PathExists(newPath) {
	} // for _, cName := range cNames {

	// Refuse to start lxd if a rsync failed.
	if errors > 0 {
		return fmt.Errorf("Got errors while moving snapshots, see the log output")
	}

	return nil
}

func patchUpdateFromV15(tx *sql.Tx) error {
	// munge all LVM-backed containers' LV names to match what is
	// required for snapshot support
	instances, err := instancesOnDisk(nil)
	if err != nil {
		return err
	}

	vgName := ""
	config, err := query.SelectConfig(tx, "config", "")
	if err != nil {
		return err
	}
	vgName = config["storage.lvm_vg_name"]

	// tomp TODO this patch seems to be accounting for snapshots, and yet instancesOnDisk doesn't
	// return snapshots, so it may do something unexpected/not enough.
	for _, inst := range instances {
		// Only interested in containers in default project.
		if inst.Type() != instancetype.Container || inst.Project() != project.Default {
			continue
		}

		cName := inst.Name()

		var lvLinkPath string
		if strings.Contains(cName, shared.SnapshotDelimiter) {
			lvLinkPath = shared.VarPath("snapshots", fmt.Sprintf("%s.lv", cName))
		} else {
			lvLinkPath = shared.VarPath("containers", fmt.Sprintf("%s.lv", cName))
		}

		if !shared.PathExists(lvLinkPath) {
			continue
		}

		newLVName := strings.Replace(cName, "-", "--", -1)
		newLVName = strings.Replace(newLVName, shared.SnapshotDelimiter, "-", -1)

		if cName == newLVName {
			logger.Debug("No need to rename, skipping", log.Ctx{"cName": cName, "newLVName": newLVName})
			continue
		}

		logger.Debug("About to rename cName in lv upgrade", log.Ctx{"lvLinkPath": lvLinkPath, "cName": cName, "newLVName": newLVName})

		_, err := shared.RunCommand("lvrename", vgName, cName, newLVName)
		if err != nil {
			return fmt.Errorf("Could not rename LV '%s' to '%s': %w", cName, newLVName, err)
		}

		if err := os.Remove(lvLinkPath); err != nil {
			return fmt.Errorf("Couldn't remove lvLinkPath '%s'", lvLinkPath)
		}
		newLinkDest := fmt.Sprintf("/dev/%s/%s", vgName, newLVName)
		if err := os.Symlink(newLinkDest, lvLinkPath); err != nil {
			return fmt.Errorf("Couldn't recreate symlink '%s' to '%s'", lvLinkPath, newLinkDest)
		}
	}

	return nil
}

func patchUpdateFromV29(_ *sql.Tx) error {
	if shared.PathExists(shared.VarPath("zfs.img")) {
		err := os.Chmod(shared.VarPath("zfs.img"), 0600)
		if err != nil {
			return err
		}
	}

	return nil
}

func patchUpdateFromV30(_ *sql.Tx) error {
	entries, err := ioutil.ReadDir(shared.VarPath("containers"))
	if err != nil {
		/* If the directory didn't exist before, the user had never
		 * started containers, so we don't need to fix up permissions
		 * on anything.
		 */
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !shared.IsDir(shared.VarPath("containers", entry.Name(), "rootfs")) {
			continue
		}

		info, err := os.Stat(shared.VarPath("containers", entry.Name(), "rootfs"))
		if err != nil {
			return err
		}

		if int(info.Sys().(*syscall.Stat_t).Uid) == 0 {
			err := os.Chmod(shared.VarPath("containers", entry.Name()), 0700)
			if err != nil {
				return err
			}

			err = os.Chown(shared.VarPath("containers", entry.Name()), 0, 0)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
