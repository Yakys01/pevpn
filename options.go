// Package upvpn exposes a local HTTP proxy that borrows the VPN tunnel of
// browser extensions (Urban VPN, TunnelBear) by reading their credentials from
// Brave's extension storage.
package upvpn

import (
	"log/slog"

	"github.com/xeri/upvpn/config"
)

// Options configures a Client.
type Options struct {
	Config config.Config
	Logger *slog.Logger
}
