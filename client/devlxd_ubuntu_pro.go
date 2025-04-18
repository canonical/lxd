package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetUbuntuPro retrieves the guest's Ubuntu Pro settings.
func (r *ProtocolDevLXD) GetUbuntuPro() (*api.UbuntuProSettings, error) {
	var info api.UbuntuProSettings

	_, err := r.queryStruct(http.MethodGet, "/ubuntu-pro", nil, "", &info)
	if err != nil {
		return nil, err
	}

	return &info, nil
}

// CreateUbuntuProToken creates a new Ubuntu Pro token.
func (r *ProtocolDevLXD) CreateUbuntuProToken() (*api.UbuntuProGuestTokenResponse, error) {
	var token api.UbuntuProGuestTokenResponse

	_, err := r.queryStruct(http.MethodPost, "/ubuntu-pro/token", nil, "", &token)
	if err != nil {
		return nil, err
	}

	return &token, nil
}
