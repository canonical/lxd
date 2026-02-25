package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetImageRegistries returns all image registries.
func (r *ProtocolLXD) GetImageRegistries() ([]api.ImageRegistry, error) {
	err := r.CheckExtension("image_registries")
	if err != nil {
		return nil, err
	}

	imageRegistries := []api.ImageRegistry{}
	_, err = r.queryStruct(http.MethodGet, api.NewURL().Path("image-registries").WithQuery("recursion", "1").String(), nil, "", &imageRegistries)
	if err != nil {
		return nil, err
	}

	return imageRegistries, nil
}

// GetImageRegistryNames returns a list of image registry names.
func (r *ProtocolLXD) GetImageRegistryNames() ([]string, error) {
	err := r.CheckExtension("image_registries")
	if err != nil {
		return nil, err
	}

	urls := []string{}
	baseURL := api.NewURL().Path("image-registries").String()
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse URLs to extract the names.
	return urlsToResourceNames(baseURL, urls...)
}

// GetImageRegistry returns information about an image registry.
func (r *ProtocolLXD) GetImageRegistry(name string) (*api.ImageRegistry, string, error) {
	err := r.CheckExtension("image_registries")
	if err != nil {
		return nil, "", err
	}

	imageRegistry := &api.ImageRegistry{}
	etag, err := r.queryStruct(http.MethodGet, api.NewURL().Path("image-registries", name).String(), nil, "", &imageRegistry)
	if err != nil {
		return nil, "", err
	}

	return imageRegistry, etag, nil
}

// GetImageRegistryImages returns a list of available images provided by the image registry.
func (r *ProtocolLXD) GetImageRegistryImages(name string) ([]api.Image, error) {
	err := r.CheckExtension("image_registries")
	if err != nil {
		return nil, err
	}

	images := []api.Image{}
	_, err = r.queryStruct(http.MethodGet, api.NewURL().Path("image-registries", name, "images").WithQuery("recursion", "1").String(), nil, "", &images)
	if err != nil {
		return nil, err
	}

	return images, nil
}

// CreateImageRegistry defines a new image registry using the provided struct.
func (r *ProtocolLXD) CreateImageRegistry(imageRegistry api.ImageRegistriesPost) error {
	err := r.CheckExtension("image_registries")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPost, api.NewURL().Path("image-registries").String(), imageRegistry, "")

	return err
}

// UpdateImageRegistry updates an existing image registry to match the provided struct.
func (r *ProtocolLXD) UpdateImageRegistry(name string, imageRegistry api.ImageRegistryPut, ETag string) error {
	err := r.CheckExtension("image_registries")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPut, api.NewURL().Path("image-registries", name).String(), imageRegistry, ETag)

	return err
}

// RenameImageRegistry renames an existing image registry.
func (r *ProtocolLXD) RenameImageRegistry(name string, imageRegistry api.ImageRegistryPost) error {
	err := r.CheckExtension("image_registries")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPost, api.NewURL().Path("image-registries", name).String(), imageRegistry, "")

	return err
}

// DeleteImageRegistry deletes an existing image registry.
func (r *ProtocolLXD) DeleteImageRegistry(name string) error {
	err := r.CheckExtension("image_registries")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodDelete, api.NewURL().Path("image-registries", name).String(), nil, "")

	return err
}
