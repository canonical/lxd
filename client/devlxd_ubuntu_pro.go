package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetUbuntuPro retrieves the guest's Ubuntu Pro settings.
func (r *ProtocolDevLXD) GetUbuntuPro() (*api.DevLXDUbuntuProSettings, error) {
	var info api.DevLXDUbuntuProSettings

	_, err := r.queryStruct(http.MethodGet, "/ubuntu-pro", nil, "", &info)
	if err != nil {
		return nil, err
	}

	return &info, nil
}

// CreateUbuntuProToken creates a new Ubuntu Pro token.
func (r *ProtocolDevLXD) CreateUbuntuProToken() (*api.DevLXDUbuntuProGuestTokenResponse, error) {
	var token api.DevLXDUbuntuProGuestTokenResponse

	_, err := r.queryStruct(http.MethodPost, "/ubuntu-pro/token", nil, "", &token)
	if err != nil {
		return nil, err
	}

	return &token, nil
}
