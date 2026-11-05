// Package builtin maps config provider names to implementations. This is the
// only place that couples the framework to a concrete provider.
package builtin

import (
	"fmt"

	"github.com/xeri/upvpn/config"
	"github.com/xeri/upvpn/provider"
	"github.com/xeri/upvpn/provider/tunnelbear"
	"github.com/xeri/upvpn/provider/urbanvpn"
	"github.com/xeri/upvpn/provider/vpnbrowser"
)

// Resolve builds providers from config.
func Resolve(list []config.Provider) ([]provider.Provider, error) {
	out := make([]provider.Provider, 0, len(list))
	for _, c := range list {
		switch c.Name {
		case urbanvpn.Name:
			out = append(out, urbanvpn.New(c.ProfileDir, c.ExtensionID))
		case tunnelbear.Name:
			out = append(out, tunnelbear.New(c.ProfileDir, c.ExtensionID))
		case vpnbrowser.Name:
			out = append(out, vpnbrowser.New(c.ProfileDir, c.ExtensionID))
		default:
			return nil, fmt.Errorf("unknown provider %q", c.Name)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no providers configured")
	}
	return out, nil
}
