// LXD external REST API
//
// This is the REST API used by all LXD clients.
// Internal endpoints aren't included in this documentation.
//
// The LXD API is available over both a local unix+http and remote https API.
// Authentication for local users relies on group membership and access to the unix socket.
// For remote users, the default authentication method is TLS client.
//
//	Version: 1.0
//	License: AGPL-3.0-only https://www.gnu.org/licenses/agpl-3.0.en.html
//	Contact: LXD upstream <lxd@lists.canonical.com> https://github.com/canonical/lxd
//
// swagger:meta
package main

// Common error definitions.
