// Package tunnelbear reads the TunnelBear VPN (Brave extension) state.
//
// TunnelBear proxies through a TLS ("https") proxy on port 8080 and
// authenticates with the account's vpn_token used as both username and
// password. The server list and token live in chrome.storage.local.
package tunnelbear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/xeri/upvpn/provider"
	"github.com/xeri/upvpn/provider/extstore"
)

// Name is the provider identifier used in config.
const Name = "tunnelbearvpn"

// ExtensionID is TunnelBear VPN's Chrome Web Store id.
const ExtensionID = "omdakjcmkglenbhjadbccaookpfjihpa"

// URL is the extension's homepage, reported when detection fails.
const URL = "https://www.tunnelbear.com/"

const (
	port = "8080"
	// scheme is https because TunnelBear's PAC uses "HTTPS <server>:8080".
	scheme = "https"
)

var (
	tokenRe   = regexp.MustCompile(`"vpn_token":"([^"]+)"`)
	serversRe = regexp.MustCompile(`\[(?:"[^"]*"(?:,"[^"]*")*)\]`)
	countryRe = regexp.MustCompile(`"country_iso":"([A-Z]{2})"`)
)

// Provider implements provider.Provider.
type Provider struct {
	ProfileDir  string
	ExtensionID string
}

// New returns a provider. Empty profileDir means "discover the Brave profile".
func New(profileDir, extensionID string) *Provider {
	if extensionID == "" {
		extensionID = ExtensionID
	}
	return &Provider{ProfileDir: profileDir, ExtensionID: extensionID}
}

// Name reports the provider id.
func (p *Provider) Name() string { return Name }

// Snapshot reads the extension storage and returns the current targets.
func (p *Provider) Snapshot(ctx context.Context) (provider.Snapshot, error) {
	data, err := extstore.Load(p.ProfileDir, p.ExtensionID)
	if err != nil {
		return provider.Snapshot{}, fmt.Errorf("%s: extension not detected; install and connect it in Brave (%s)", Name, URL)
	}
	snap := parse(data)
	if len(snap.Targets) == 0 {
		return snap, fmt.Errorf("%s: no active proxy; open Brave and connect the extension (%s)", Name, URL)
	}
	return snap, nil
}

func parse(data []byte) provider.Snapshot {
	snap := provider.Snapshot{
		Location: lastSubmatch(countryRe, data),
	}
	if i := bytes.LastIndex(data, []byte("PB_IS_VPN_CONNECTED")); i >= 0 {
		tail := data[i:]
		if len(tail) > 64 {
			tail = tail[:64]
		}
		snap.Connected = bytes.Contains(tail, []byte("true"))
	}

	token := lastSubmatch(tokenRe, data)
	if token == "" {
		return snap
	}
	for _, host := range servers(data) {
		snap.Targets = append(snap.Targets, provider.Target{
			Host: host, Port: port, Scheme: scheme,
			Username: token, Password: token, Provider: Name,
		})
	}
	return snap
}

func servers(data []byte) []string {
	i := bytes.LastIndex(data, []byte("PB_VPN_SERVERS"))
	if i < 0 {
		return nil
	}
	m := serversRe.Find(data[i:])
	if m == nil {
		return nil
	}
	var out []string
	if err := json.Unmarshal(m, &out); err != nil {
		return nil
	}
	return out
}

func lastSubmatch(re *regexp.Regexp, data []byte) string {
	ms := re.FindAllSubmatch(data, -1)
	if len(ms) == 0 {
		return ""
	}
	return string(ms[len(ms)-1][1])
}
