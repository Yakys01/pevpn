// Package provider defines the contract between upvpn's proxy layer and the
// browser extensions whose VPN proxy it borrows.
package provider

import (
	"context"
	"net"
)

// Target is one upstream proxy endpoint plus the credentials to use it.
type Target struct {
	Host     string
	Port     string
	Scheme   string // "http" (plain forwarding proxy) or "https" (TLS proxy)
	Username string
	Password string
	Provider string
}

// Addr returns host:port.
func (t Target) Addr() string { return net.JoinHostPort(t.Host, t.Port) }

// Snapshot is a provider's current view of the world.
type Snapshot struct {
	Connected bool
	Location  string
	Targets   []Target
}

// Provider reads one extension's state and reports usable targets.
type Provider interface {
	Name() string
	Snapshot(ctx context.Context) (Snapshot, error)
}
