package auth

import (
	"net/http"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared"
)

type tls struct {
	commonAuthorizer
}

func (a *tls) load() error {
	return nil
}

// AddProject is a no-op. It notifies the authorization service about new projects.
func (a *tls) AddProject(projectID int64, name string) error {
	return nil
}

// DeleteProject is a no-op. It notifies the authorization service about deleted projects.
func (a *tls) DeleteProject(projectID int64) error {
	return nil
}

// RenameProject is a no-op. It notifies the authorization service that a project has been renamed.
func (a *tls) RenameProject(projectID int64, newName string) error {
	return nil
}

// StopStatusCheck is a no-op.
func (a *tls) StopStatusCheck() {
}

func (a *tls) UserAccess(username string) (*UserAccess, error) {
	return &UserAccess{Admin: true}, nil
}

// UserIsAdmin checks whether the requestor is a global admin.
func (a *tls) UserIsAdmin(r *http.Request) bool {
	val := r.Context().Value(request.CtxAccess)
	if val == nil {
		return false
	}

	ua := val.(*UserAccess)
	return ua.Admin
}

// UserHasPermission checks whether the requestor has a specific permission on a project.
func (a *tls) UserHasPermission(r *http.Request, projectName string, _ string, relation Relation) bool {
	val := r.Context().Value(request.CtxAccess)
	if val == nil {
		return false
	}

	ua := val.(*UserAccess)
	if ua.Admin {
		return true
	}

	return shared.StringInSlice(a.relationToPermission(relation), ua.Projects[projectName])
}

func (a *tls) relationToPermission(relation Relation) string {
	switch relation {
	case RelationImageManager:
		return "manage-images"
	case RelationInstanceManager:
		return "manage-containers"
	case RelationNetworkManager, RelationNetworkACLManager, RelationNetworkZoneManager:
		return "manage-networks"
	case RelationProfileManager:
		return "manage-profiles"
	case RelationStorageBucketManager, RelationStorageVolumeManager:
		return "manage-storage-volumes"
	case RelationInstanceOperator:
		return "operate-containers"
	case RelationImageViewer, RelationInstanceViewer, RelationNetworkViewer, RelationNetworkACLViewer, RelationNetworkZoneViewer, RelationProfileViewer, RelationStorageVolumeViewer, RelationViewer:
		return "view"
	}

	return ""
}
