package auth

import (
	"fmt"
	"net/http"

	"github.com/canonical/lxd/shared/logger"
)

// ErrUnknownDriver is the "Unknown driver" error.
var ErrUnknownDriver = fmt.Errorf("Unknown driver")

// ErrNotSupported is the "Not supported" error.
var ErrNotSupported = fmt.Errorf("Not supported")

var authorizers = map[string]func() authorizer{
	"tls": func() authorizer { return &tls{} },
	"rbac": func() authorizer {
		return &rbac{
			resources:   map[string]string{},
			permissions: map[string]map[string][]string{},
		}
	},
}

type authorizer interface {
	Authorizer

	init(name string, config map[string]any, logger logger.Logger, projectsGetFunc func() (map[int64]string, error))
	load() error
}

type Authorizer interface {
	AddProject(projectID int64, name string) error
	DeleteProject(projectID int64) error
	RenameProject(projectID int64, newName string) error

	StopStatusCheck()

	UserAccess(username string) (*UserAccess, error)
	UserIsAdmin(r *http.Request) bool
	UserHasPermission(r *http.Request, projectName string, objectName string, relation Relation) bool
	ListObjects(r *http.Request, relation Relation, objectType ObjectType) ([]string, error)
	GetPermissionChecker(r *http.Request, relation Relation, objectType ObjectType) (func(object string) bool, error)

	AddTuple(user string, relation Relation, object string) error
	DeleteTuple(user string, relation Relation, object string) error
}

// UserAccess struct for permission checks.
type UserAccess struct {
	Admin    bool
	Projects map[string][]string
}

func LoadAuthorizer(name string, config map[string]any, logger logger.Logger, projectsGetFunc func() (map[int64]string, error)) (Authorizer, error) {
	driverFunc, ok := authorizers[name]
	if !ok {
		return nil, ErrUnknownDriver
	}

	d := driverFunc()
	d.init(name, config, logger, projectsGetFunc)

	err := d.load()
	if err != nil {
		return nil, err
	}

	return d, nil
}
