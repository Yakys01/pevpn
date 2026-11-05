// Package urbanvpn reads the Urban VPN Proxy (Brave extension) state.
//
// The extension stores its per-session proxy credentials double-encoded as a
// JSON string inside chrome.storage.local; the last occurrence in the LevelDB
// log is the active one.
package urbanvpn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/xeri/upvpn/provider"
	"github.com/xeri/upvpn/provider/extstore"
)

// Name is the provider identifier used in config.
const Name = "urbanvpn"

// ExtensionID is Urban VPN Proxy's Chrome Web Store id.
const ExtensionID = "eppiocemhmnlbhjplcgkofciiegomcon"

// URL is the extension's homepage, reported when detection fails.
const URL = "https://www.urban-vpn.com/"

const (
	keyCreds = "LOCATION_PROCESSOR_CREDENTIALS"
	keyState = "LOCATION_PROCESSOR_STATE"
)

var jsonStringRe = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

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

// Snapshot reads the extension storage and returns the current target.
func (p *Provider) Snapshot(ctx context.Context) (provider.Snapshot, error) {
	data, err := extstore.Load(p.ProfileDir, p.ExtensionID)
	if err != nil {
		return provider.Snapshot{}, fmt.Errorf("%s: extension not detected; install and connect it in Brave (%s)", Name, URL)
	}
	snap := parse(data)
	if !snap.Connected {
		return snap, fmt.Errorf("%s: not connected; open Brave and connect the extension (%s)", Name, URL)
	}
	if len(snap.Targets) == 0 {
		return snap, fmt.Errorf("%s: no active proxy; open Brave and connect the extension (%s)", Name, URL)
	}
	return snap, nil
}

func parse(data []byte) provider.Snapshot {
	creds := valueAfter(data, keyCreds)
	state := valueAfter(data, keyState)

	snap := provider.Snapshot{}
	username, _ := creds["username"].(string)
	password, _ := creds["password"].(string)
	if password == "" {
		password = "1"
	}

	host, port := "", "8081"
	if proxy, ok := state["proxy"].(map[string]any); ok {
		if server, ok := proxy["server"].(map[string]any); ok {
			host, _ = server["host"].(string)
			if n, ok := server["port"].(float64); ok && n > 0 {
				port = strconv.Itoa(int(n))
			}
		}
	}
	if c, ok := state["connection"].(string); ok {
		snap.Connected = c == "CONNECTED"
	}
	if loc, ok := state["location"].(map[string]any); ok {
		snap.Location, _ = loc["code"].(string)
	}
	if username != "" && host != "" {
		snap.Targets = []provider.Target{{
			Host: host, Port: port, Scheme: "http",
			Username: username, Password: password, Provider: Name,
		}}
	}
	return snap
}

// valueAfter decodes the double-encoded JSON value that follows the last
// occurrence of key.
func valueAfter(data []byte, key string) map[string]any {
	i := bytes.LastIndex(data, []byte(key))
	if i < 0 {
		return nil
	}
	rest := data[i+len(key):]
	if len(rest) > 1<<20 {
		rest = rest[:1<<20]
	}
	m := jsonStringRe.Find(rest)
	if m == nil {
		return nil
	}
	var inner string
	if err := json.Unmarshal(m, &inner); err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(inner), &out); err != nil {
		return nil
	}
	return out
}
