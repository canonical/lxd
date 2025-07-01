package request

import (
	"net/http"
	"net/url"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// ProjectParams returns the project name and the value of the all projects query parameter. It returns an
// [api.StatusError] with [http.StatusBadRequest] if both parameters are specified.
func ProjectParams(r *http.Request) (string, bool, error) {
	projectName := QueryParam(r, "project")
	allProjects := shared.IsTrue(QueryParam(r, "all-projects"))

	// The requested project name is only valid for project specific requests.
	if allProjects && projectName != "" {
		return "", false, api.StatusErrorf(http.StatusBadRequest, "Cannot specify a project when requesting all projects")
	} else if !allProjects && projectName == "" {
		projectName = api.ProjectDefaultName
	}

	return projectName, allProjects, nil
}

// ProjectParam returns the project query parameter from the given request or "default" if parameter is not set.
func ProjectParam(request *http.Request) string {
	projectParam := QueryParam(request, "project")
	if projectParam == "" {
		projectParam = api.ProjectDefaultName
	}

	return projectParam
}

// QueryParam extracts the given query parameter directly from the URL, never from an
// encoded body.
func QueryParam(request *http.Request, key string) string {
	var values url.Values
	var err error

	if request.URL != nil {
		values, err = url.ParseQuery(request.URL.RawQuery)
		if err != nil {
			logger.Warnf("Failed to parse query string %q: %v", request.URL.RawQuery, err)
			return ""
		}
	}

	if values == nil {
		values = make(url.Values)
	}

	return values.Get(key)
}
