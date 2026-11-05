// Package vpnbrowser reads the VPN Browser (Brave extension) state.
//
// Unlike Urban/TunnelBear, the extension keeps its proxy credentials only in
// service-worker memory. They can be re-minted through its API though: the
// long-lived securityToken (Bearer) and the per-server signature are stored in
// chrome.storage.local, and POSTing the signature returns a short-lived proxy
// token (valid ~30min) used as Basic username with "1" as password.
package vpnbrowser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/xeri/upvpn/provider"
	"github.com/xeri/upvpn/provider/extstore"
)

// Name is the provider identifier used in config.
const Name = "vpnbrowser"

// ExtensionID is VPN Browser's Chrome Web Store id.
const ExtensionID = "abnmgemfnjndaolfflfpjcckiamamein"

// URL is the extension's homepage, reported when detection fails.
const URL = "https://vpn-browser.com/"

const (
	apiBase     = "https://api.vpn-browser.com"
	clientApp   = "FREE_VPN_BROWSER_EXTENSION"
	mintTimeout = 10 * time.Second
)

var (
	secTokenRe = regexp.MustCompile(`"value":"(eyJ[A-Za-z0-9_.+/=-]{100,})"`)
	stateRe    = regexp.MustCompile(`\{"(?:connectedAt":[\d.eE+]+,)?"connection":"(CONNECTED|DISCONNECTED)"(?:,"countryCode":"([A-Z]{2})")?(?:,"ip":"([\d.]+)")?\}`)
	serverRe   = regexp.MustCompile(`"address":\{"primary":\{"host":"([^"]+)","ip":"[^"]*","port":(\d+)\}\}.{0,300}?"signature":"([^"]+)"`)
	regTokenRe = regexp.MustCompile(`"value":"([A-Za-z0-9_-]{8,64})"`)
)

// errAuth marks an expired/invalid security token; the caller then renews it
// with the registration token and retries.
var errAuth = errors.New("security token rejected")

type server struct{ host, port, sig string }

type parsed struct {
	connected bool
	location  string
	exitIP    string
	secToken  string
	regToken  string
	servers   map[string]server
}

// Provider implements provider.Provider.
type Provider struct {
	ProfileDir  string
	ExtensionID string

	base   string
	client *http.Client
}

// New returns a provider. Empty profileDir means "discover the Brave profile".
func New(profileDir, extensionID string) *Provider {
	if extensionID == "" {
		extensionID = ExtensionID
	}
	return &Provider{
		ProfileDir: profileDir, ExtensionID: extensionID,
		base: apiBase, client: &http.Client{Timeout: mintTimeout},
	}
}

// Name reports the provider id.
func (p *Provider) Name() string { return Name }

// Snapshot reads the extension storage and mints a proxy token for the
// server the extension is currently connected through.
func (p *Provider) Snapshot(ctx context.Context) (provider.Snapshot, error) {
	data, err := extstore.Load(p.ProfileDir, p.ExtensionID)
	if err != nil {
		return provider.Snapshot{}, fmt.Errorf("%s: extension not detected; install and connect it in Brave (%s)", Name, URL)
	}
	d := parseData(data)
	snap := provider.Snapshot{Connected: d.connected, Location: d.location}
	if !d.connected {
		return snap, fmt.Errorf("%s: not connected; open Brave and connect the extension (%s)", Name, URL)
	}
	cur, ok := d.servers[d.exitIP]
	if !ok {
		return snap, fmt.Errorf("%s: current server not in extension cache; reconnect the extension (%s)", Name, URL)
	}
	token, err := p.proxyToken(ctx, d.secToken, d.regToken, cur.sig)
	if err != nil {
		return snap, fmt.Errorf("%s: %w", Name, err)
	}
	snap.Targets = []provider.Target{{
		Host: cur.host, Port: cur.port, Scheme: "http",
		Username: token, Password: "1", Provider: Name,
	}}
	return snap, nil
}

// proxyToken mints an access token for the server signature, renewing the
// security token first if the upstream rejects it (it expires hourly).
func (p *Provider) proxyToken(ctx context.Context, sec, reg, sig string) (string, error) {
	token, err := p.mint(ctx, sec, sig)
	if err == nil {
		return token, nil
	}
	if !errors.Is(err, errAuth) || reg == "" {
		return "", err
	}
	sec, err = p.renewSecurity(ctx, reg)
	if err != nil {
		return "", err
	}
	return p.mint(ctx, sec, sig)
}

func (p *Provider) mint(ctx context.Context, sec, sig string) (string, error) {
	if sec == "" {
		return "", fmt.Errorf("security token missing; reconnect the extension (%s)", URL)
	}
	body := fmt.Sprintf(`{"type":"accs-proxy","clientApp":{"name":%q},"signature":%q}`, clientApp, sig)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/rest/v1/security/tokens/accs-proxy", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("authorization", "Bearer "+sec)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "*/*")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", errAuth
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("proxy token API: %s", resp.Status)
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out); err != nil {
		return "", err
	}
	if out.Value == "" {
		return "", fmt.Errorf("proxy token API: empty token")
	}
	return out.Value, nil
}

func (p *Provider) renewSecurity(ctx context.Context, reg string) (string, error) {
	body := fmt.Sprintf(`{"type":"accs","clientApp":{"name":%q}}`, clientApp)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/rest/v1/security/tokens/accs", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("authorization", "Bearer "+reg)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "*/*")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("registration token rejected; reconnect the extension (%s)", URL)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("security token API: %s", resp.Status)
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&out); err != nil {
		return "", err
	}
	if out.Value == "" {
		return "", fmt.Errorf("security token API: empty token")
	}
	return out.Value, nil
}

func parseData(data []byte) parsed {
	d := parsed{servers: map[string]server{}}
	if ms := stateRe.FindAllSubmatch(data, -1); len(ms) > 0 {
		m := ms[len(ms)-1]
		d.connected = string(m[1]) == "CONNECTED"
		d.location = string(m[2])
		d.exitIP = string(m[3])
	}
	for _, m := range serverRe.FindAllSubmatch(data, -1) {
		d.servers[string(m[1])] = server{host: string(m[1]), port: string(m[2]), sig: string(m[3])}
	}
	d.secToken = valueAfter(data, "securityToken", secTokenRe, 1<<13)
	d.regToken = valueAfter(data, "registrationToken", regTokenRe, 1<<11)
	return d
}

func valueAfter(data []byte, key string, re *regexp.Regexp, limit int) string {
	i := bytes.LastIndex(data, []byte(key))
	if i < 0 {
		return ""
	}
	tail := data[i:]
	if len(tail) > limit {
		tail = tail[:limit]
	}
	if m := re.FindSubmatch(tail); m != nil {
		return string(m[1])
	}
	return ""
}
