package auth

import (
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
