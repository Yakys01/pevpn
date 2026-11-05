# upvpn

Local HTTP proxy that borrows the VPN tunnel of a Brave extension (Urban VPN,
TunnelBear), so any process can use it with `http_proxy` — browsers ignore the
extension's proxy outside the browser.

Reads the extension's per-session credentials from Brave's storage
(`Local Extension Settings/<extension-id>`), injects them into the upstream
proxy, and refreshes them on a `407`. VPN Browser is the exception: its token
lives only in the service worker's memory, so upvpn mints a fresh one through
the extension's own API using the `securityToken` and the server `signature`
it finds in storage.

## Build

```sh
go build -o ~/.local/bin/upvpn .
```

## Usage

```sh
upvpn serve --port 8888        # local proxy (default 127.0.0.1:8888)
upvpn curl -s https://api.ipify.org   # one-shot: runs a command with the proxy
upvpn --show                   # providers, endpoints, state
upvpn --json                   # same, machine readable
upvpn --check                  # per-provider HTTPS probe, PASS if the exit IP is that provider's proxy
upvpn --self-test              # same, plus a plain HTTP probe
upvpn --print-config           # effective config (defaults + file + flags)
```

Then point any tool at it:

```sh
export http_proxy=http://127.0.0.1:8888 https_proxy=$http_proxy
curl https://api.ipify.org
```

## Config

`~/.config/upvpn/config.json` (optional; defaults are used when absent):

```json
{
  "bind": "127.0.0.1",
  "port": 8888,
  "providers": [{ "name": "urbanvpn" }, { "name": "tunnelbearvpn" }],
  "selector": { "strategy": "round-robin", "maxFails": 1, "failTimeoutSeconds": 30 }
}
```

Precedence: flags > env > file > defaults. Env: `UPVPN_PROVIDER`, `UPVPN_BIND`,
`UPVPN_PORT`, `UPVPN_PROFILE_DIR`, `UPVPN_EXTENSION_ID`, `UPVPN_CONFIG`,
`UPVPN_BRAVE_ROOT`.

`strategy` is `round-robin` (default) or `random`. A target that fails
`maxFails` times is skipped for `failTimeoutSeconds`; a successful request
resets it.

## Providers

| name | extension | link | proxy | auth |
|---|---|---|---|---|
| `urbanvpn` | Urban VPN Proxy | [urban-vpn.com](https://www.urban-vpn.com/) | `http` on the chosen server | Basic, token as username, `1` as password |
| `tunnelbearvpn` | TunnelBear VPN | [tunnelbear.com](https://www.tunnelbear.com/) | `https` (TLS) on port 8080 | Basic, `vpn_token` as both fields |
| `vpnbrowser` | VPN Browser | [vpn-browser.com](https://vpn-browser.com/) | `http` on the current server (port 80) | Basic, API-minted token as username, `1` as password |

Extensions are autodetected: upvpn scans every Brave profile for the extension
id, so no config is needed. The browser extension must be running and connected;
upvpn only reads its storage, it does not start or configure it.

When an extension is not detected (or installed but not connected), upvpn logs
an error naming it and pointing at the link above, e.g.:

```
level=ERROR msg="upvpn: provider unavailable" err="tunnelbearvpn: extension not detected; install and connect it in Brave (https://www.tunnelbear.com/)"
```

## Limits

- Binds to `127.0.0.1` only. Any local process can use it without auth.
- Using the tunnel outside the app may violate the provider's ToS.
- One request per client connection; no SOCKS/HTTPS client-facing support.
