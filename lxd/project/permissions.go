package project

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

// CheckRestrictedDevicesDiskPaths checks whether the disk's source path is within the allowed paths specified in
// the project's restricted.devices.disk.paths config setting.
// If no allowed paths are specified in project, then it allows all paths, and returns true and empty string.
// If allowed paths are specified, and one matches, returns true and the matching allowed parent source path.
// Otherwise if sourcePath not allowed returns false and empty string.
func CheckRestrictedDevicesDiskPaths(projectConfig map[string]string, sourcePath string) (bool, string) {
	if projectConfig["restricted.devices.disk.paths"] == "" {
		return true, ""
	}

	// Clean, then add trailing slash, to ensure we are prefix matching on whole path.
	sourcePath = filepath.Clean(shared.HostPath(sourcePath)) + "/"
	for parentSourcePath := range strings.SplitSeq(projectConfig["restricted.devices.disk.paths"], ",") {
		// Clean, then add trailing slash, to ensure we are prefix matching on whole path.
		parentSourcePathTrailing := filepath.Clean(shared.HostPath(parentSourcePath)) + "/"
		if strings.HasPrefix(sourcePath, parentSourcePathTrailing) {
			return true, parentSourcePath
		}
	}

	return false, ""
}

// CheckStandbyReplica returns a Forbidden error when the project is a standby replica and the
// requestor is not the identity of the cluster link named by replica.cluster. It is a no-op for
// cluster notifications and for projects that are not in standby mode.
func CheckStandbyReplica(ctx context.Context, tx *db.ClusterTx, targetProject *api.Project, requestor *request.Requestor) error {
	// Only replicator runs from the configured cluster link (or internal cluster
	// notifications forwarded by the coordinator) can write to a standby replica
	// project.
	if targetProject.ReplicaMode != api.ReplicatorProjectModeStandby || requestor.IsClusterNotification() {
		return nil
	}

	// A project can be force demoted to standby without replica.cluster set, which leaves no
	// identity that is allowed to write to it.
	expectedCluster := targetProject.Config["replica.cluster"]
	if expectedCluster == "" {
		return api.StatusErrorf(http.StatusForbidden, "Cannot write to a standby replica project")
	}

	// Verify the request comes from the configured cluster link identity.
	clusterLink, err := cluster.GetClusterLink(ctx, tx.Tx(), expectedCluster)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return api.StatusErrorf(http.StatusForbidden, "Cannot write to a standby replica project")
		}

		return fmt.Errorf("Failed loading cluster link %q: %w", expectedCluster, err)
	}

	// Only allow the expected cluster link identity to write to the standby replica project.
	// We can check this using the requestor identity ID, since cluster links always communicate using a named
	// TLS identity. We need to ensure the identity is not nil - since it can be nil for admin protocols.
	// (Admins are not allowed to write to this project while it is in standby either).
	// The cluster link identity can also be nil, for example a unidirectional link stored on the initiating
	// side has no local identity, so treat that as forbidden rather than dereferencing a nil identity.
	if requestor.IdentityID == nil || clusterLink.IdentityID == nil || *requestor.IdentityID != *clusterLink.IdentityID {
		return api.StatusErrorf(http.StatusForbidden, "Cannot write to a standby replica project")
	}

	return nil
}

// FilterUsedBy filters a UsedBy list based on the entities that the requestor is able to view.
func FilterUsedBy(ctx context.Context, authorizer auth.Authorizer, entries []string) []string {
	// Get a map of URLs by entity type. If there are multiple entries of a particular entity type we can reduce the
	// number of calls to the authorizer.
	urlsByEntityType := make(map[entity.Type][]*api.URL)
	for _, entry := range entries {
		u, err := url.Parse(entry)
		if err != nil {
			logger.Warn("Failed parsing project used-by entity URL", logger.Ctx{"url": entry, "err": err})
			continue
		}

		entityType, _, _, _, err := entity.ParseURL(*u)
		if err != nil {
			logger.Warn("Failed parsing project used-by entity URL", logger.Ctx{"url": entry, "err": err})
			continue
		}

		urlsByEntityType[entityType] = append(urlsByEntityType[entityType], &api.URL{URL: *u})
	}

	// Filter the entries.
	usedBy := make([]string, 0, len(entries))

	for entityType, urls := range urlsByEntityType {
		// If only one entry of this type, check directly.
		if len(urls) == 1 {
			err := authorizer.CheckPermissionWithoutEffectiveProject(ctx, urls[0], auth.EntitlementCanView)
			if err != nil {
				continue
			}

			usedBy = append(usedBy, urls[0].String())
			continue
		}

		// Otherwise get a permission checker for the entity type.
		canViewEntity, err := authorizer.GetPermissionCheckerWithoutEffectiveProject(ctx, auth.EntitlementCanView, entityType)
		if err != nil {
			logger.Error("Failed getting permission checker for project used-by filtering", logger.Ctx{"entity_type": entityType, "err": err})
			continue
		}

		// Check each url and append.
		for _, u := range urls {
			if canViewEntity(u) {
				usedBy = append(usedBy, u.String())
			}
		}
	}

	return usedBy
}
