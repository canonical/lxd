package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetServices returns all services.
func (r *ProtocolLXD) GetServices() ([]api.Service, error) {
	err := r.CheckExtension("services")
	if err != nil {
		return nil, err
	}

	services := []api.Service{}
	u := api.NewURL().Path("services")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &services)
	if err != nil {
		return nil, err
	}

	return services, nil
}

// GetService returns information about a service.
func (r *ProtocolLXD) GetService(name string) (*api.Service, string, error) {
	err := r.CheckExtension("services")
	if err != nil {
		return nil, "", err
	}

	service := &api.Service{}
	u := api.NewURL().Path("services", name)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &service)
	if err != nil {
		return nil, "", err
	}

	return service, etag, nil
}

// AddService requests add a new service.
func (r *ProtocolLXD) AddService(service api.ServicePost) error {
	err := r.CheckExtension("services")
	if err != nil {
		return err
	}

	u := api.NewURL().Path("service", "add")
	_, _, err = r.query(http.MethodPost, u.String(), service, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateService updates a service.
func (r *ProtocolLXD) UpdateService(name string, service api.ServicePut, ETag string) error {
	err := r.CheckExtension("services")
	if err != nil {
		return err
	}

	u := api.NewURL().Path("services", name)
	_, _, err = r.query(http.MethodPut, u.String(), service, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteService deletes a service.
func (r *ProtocolLXD) DeleteService(name string) error {
	err := r.CheckExtension("services")
	if err != nil {
		return err
	}

	u := api.NewURL().Path("services", name)
	_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}
