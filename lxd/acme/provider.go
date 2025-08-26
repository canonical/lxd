package acme

import (
	"sync"

	"github.com/go-acme/lego/v4/challenge"
)

// HTTP01Provider is an extension of the challenge.Provider interface.
type HTTP01Provider interface {
	challenge.Provider

	Domain() string
	KeyAuth() string
	Token() string
}

type http01Provider struct {
	mu      sync.Mutex
	domain  string
	token   string
	keyAuth string
}

// NewHTTP01Provider returns a HTTP01Provider.
func NewHTTP01Provider() HTTP01Provider {
	return &http01Provider{}
}

// Present sets the provider's domain, token, and keyAuth values.
func (p *http01Provider) Present(domain string, token string, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.domain = domain
	p.token = token
	p.keyAuth = keyAuth

	return nil
}

// CleanUp clears the provider's domain, token, and keyAuth values.
func (p *http01Provider) CleanUp(domain string, token string, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.domain = ""
	p.token = ""
	p.keyAuth = ""

	return nil
}

// KeyAuth returns the key authorization value.
func (p *http01Provider) KeyAuth() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.keyAuth
}

// Domain returns the domain value.
func (p *http01Provider) Domain() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.domain
}

// Token returns the token value.
func (p *http01Provider) Token() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.token
}
