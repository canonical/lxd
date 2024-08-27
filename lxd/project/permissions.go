package project

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/lxd/auth"
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
	sourcePath = fmt.Sprintf("%s/", filepath.Clean(shared.HostPath(sourcePath)))
	for _, parentSourcePath := range strings.SplitN(projectConfig["restricted.devices.disk.paths"], ",", -1) {
		// Clean, then add trailing slash, to ensure we are prefix matching on whole path.
		parentSourcePathTrailing := fmt.Sprintf("%s/", filepath.Clean(shared.HostPath(parentSourcePath)))
		if strings.HasPrefix(sourcePath, parentSourcePathTrailing) {
			return true, parentSourcePath
		}
	}

	return false, ""
}

// FilterUsedBy filters a UsedBy list based on the entities that the requestor is able to view.
func FilterUsedBy(authorizer auth.Authorizer, r *http.Request, entries []string) []string {
	// Get a map of URLs by entity type. If there are multiple entries of a particular entity type we can reduce the
	// number of calls to the authorizer.
	urlsByEntityType := make(map[entity.Type][]*api.URL)
	for _, entry := range entries {
		u, err := url.Parse(entry)
		if err != nil {
			logger.Warn("Failed to parse project used-by entity URL", logger.Ctx{"url": entry, "err": err})
			continue
		}

		entityType, _, _, _, err := entity.ParseURL(*u)
		if err != nil {
			logger.Warn("Failed to parse project used-by entity URL", logger.Ctx{"url": entry, "err": err})
			continue
		}

		urlsByEntityType[entityType] = append(urlsByEntityType[entityType], &api.URL{URL: *u})
	}

	// Filter the entries.
	usedBy := make([]string, 0, len(entries))

	for entityType, urls := range urlsByEntityType {
		// If only one entry of this type, check directly.
		if len(urls) == 1 {
			err := authorizer.CheckPermission(r.Context(), urls[0], auth.EntitlementCanView)
			if err != nil {
				continue
			}

			usedBy = append(usedBy, urls[0].String())
			continue
		}

		// Otherwise get a permission checker for the entity type.
		canViewEntity, err := authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entityType)
		if err != nil {
			logger.Error("Failed to get permission checker for project used-by filtering", logger.Ctx{"entity_type": entityType, "err": err})
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
