package acme

import (
	"crypto"

	"github.com/go-acme/lego/v4/registration"
)

type user struct {
	Email        string
	Registration *registration.Resource
	Key          crypto.PrivateKey
}

// GetEmail returns the email address for the user.
func (a *user) GetEmail() string {
	return a.Email
}

// GetRegistration returns the registration resource for the user.
func (a *user) GetRegistration() *registration.Resource {
	return a.Registration
}

// GetPrivateKey returns the private key for the user.
func (a *user) GetPrivateKey() crypto.PrivateKey {
	return a.Key
}
