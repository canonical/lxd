package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetSites returns all sites.
func (r *ProtocolLXD) GetSites() ([]api.Site, error) {
	err := r.CheckExtension("sites")
	if err != nil {
		return nil, err
	}

	sites := []api.Site{}
	u := api.NewURL().Path("sites")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &sites)
	if err != nil {
		return nil, err
	}

	return sites, nil
}

// GetSite returns information about a site.
func (r *ProtocolLXD) GetSite(name string) (*api.Site, string, error) {
	err := r.CheckExtension("sites")
	if err != nil {
		return nil, "", err
	}

	site := &api.Site{}
	u := api.NewURL().Path("sites", name)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &site)
	if err != nil {
		return nil, "", err
	}

	return site, etag, nil
}

// JoinSite requests add a new site.
func (r *ProtocolLXD) JoinSite(site api.SitePost) (Operation, error) {
	err := r.CheckExtension("sites")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("site", "join")
	op, _, err := r.queryOperation(http.MethodPost, u.String(), site, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateSiteName updates a site's name.
func (r *ProtocolLXD) UpdateSiteName(name string) (Operation, error) {
	// TODO: expand function to provide support for updating site addresses.
	err := r.CheckExtension("sites")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("sites", name)
	op, _, err := r.queryOperation(http.MethodPut, u.String(), nil, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteSite deletes a site.
func (r *ProtocolLXD) DeleteSite(name string) (Operation, error) {
	err := r.CheckExtension("sites")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("sites", name)
	op, _, err := r.queryOperation(http.MethodDelete, u.String(), nil, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}
