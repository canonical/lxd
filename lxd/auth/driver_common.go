package auth

import (
	"net/http"

	"github.com/canonical/lxd/shared/logger"
)

type commonAuthorizer struct {
	name            string
	config          map[string]any
	logger          logger.Logger
	projectsGetFunc func() (map[int64]string, error)
}

func (c *commonAuthorizer) init(name string, config map[string]any, l logger.Logger, projectsGetFunc func() (map[int64]string, error)) {
	c.name = name
	c.config = config
	c.logger = l
	c.projectsGetFunc = projectsGetFunc
}

func (c *commonAuthorizer) AddTuple(user string, relation Relation, object string) error {
	return ErrNotSupported
}

func (c *commonAuthorizer) DeleteTuple(user string, relation Relation, object string) error {
	return ErrNotSupported
}

func (c *commonAuthorizer) ListObjects(r *http.Request, relation Relation, objectType ObjectType) ([]string, error) {
	return nil, ErrNotSupported
}

func (c *commonAuthorizer) GetPermissionChecker(r *http.Request, relation Relation, objectType ObjectType) (func(object string) bool, error) {
	return func(object string) bool {
		return true
	}, nil
}
