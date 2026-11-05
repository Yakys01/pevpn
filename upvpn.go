package upvpn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xeri/upvpn/config"
	"github.com/xeri/upvpn/provider"
	"github.com/xeri/upvpn/provider/builtin"
	"github.com/xeri/upvpn/proxy"
)

// Sentinel errors returned by Client methods.
var (
	ErrNotConnected        = errors.New("vpn not connected or no credentials in extension storage")
	ErrNoCredentials       = errors.New("no credentials found")
	ErrAuthRejected        = errors.New("upstream rejected credentials")
	ErrUpstreamUnreachable = errors.New("no upstream reachable")
)

// Client is the library entry point.
type Client struct {
	cfg config.Config
	log *slog.Logger
	sel *proxy.Selector
}

// New builds a Client from Options.
func New(opts Options) (*Client, error) {
	cfg := opts.Config
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	providers, err := builtin.Resolve(cfg.Providers)
	if err != nil {
		return nil, err
	}
	sel := proxy.NewSelector(providers, cfg.Selector.Strategy, cfg.Selector.MaxFails,
		time.Duration(cfg.Selector.FailTimeoutSeconds)*time.Second, log)
	return &Client{cfg: cfg, log: log, sel: sel}, nil
}

// Snapshot reads every provider and returns the aggregated state.
func (c *Client) Snapshot(ctx context.Context) (provider.Snapshot, error) {
	c.sel.Refresh(ctx)
	return c.sel.Snapshot(), nil
}

// Serve listens on addr and proxies until ctx is cancelled.
func (c *Client) Serve(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return c.ServeListener(ctx, ln)
}

// ServeListener serves on an existing listener.
func (c *Client) ServeListener(ctx context.Context, ln net.Listener) error {
	c.sel.Refresh(ctx)
	snap := c.sel.Snapshot()
	if len(snap.Targets) == 0 {
		ln.Close()
		return ErrNotConnected
	}
	c.log.Info("upvpn: proxy local escuchando",
		"addr", ln.Addr().String(),
		"targets", len(snap.Targets),
		"location", snap.Location,
		"connected", snap.Connected)
	return proxy.NewServer(c.sel, c.log).Serve(ctx, ln)
}

// Addr returns the configured bind address.
func (c *Client) Addr() string {
	return net.JoinHostPort(c.cfg.Bind, fmt.Sprint(c.cfg.Port))
}

// Check routes one HTTPS request through a throwaway local proxy and reports
// the observed exit IP.
func (c *Client) Check(ctx context.Context) (string, bool, error) {
	var ip string
	if err := c.withProxy(ctx, func(server string) error {
		var err error
		ip, err = probeIP(server, "https://api.ipify.org")
		return err
	}); err != nil {
		return "", false, err
	}
	return ip, c.isExitIP(ctx, ip), nil
}

// SelfTest is Check plus a plain-HTTP request, covering both forwarding paths.
func (c *Client) SelfTest(ctx context.Context) (string, bool, error) {
	var results []string
	if err := c.withProxy(ctx, func(server string) error {
		for _, target := range []string{"https://api.ipify.org", "http://api.ipify.org"} {
			ip, err := probeIP(server, target)
			if err != nil {
				return err
			}
			results = append(results, ip)
		}
		return nil
	}); err != nil {
		return "", false, err
	}
	if len(results) == 0 {
		return "", false, ErrUpstreamUnreachable
	}
	ok := true
	for _, ip := range results {
		if !c.isExitIP(ctx, ip) {
			ok = false
		}
	}
	return results[0], ok, nil
}

// withProxy starts a temporary local proxy and calls fn with its address.
func (c *Client) withProxy(ctx context.Context, fn func(server string) error) error {
	c.sel.Refresh(ctx)
	if len(c.sel.Snapshot().Targets) == 0 {
		return ErrNotConnected
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.ServeListener(cctx, ln)
	return fn(ln.Addr().String())
}

// isExitIP reports whether ip equals one of the targets, either literally or
// via DNS for hostname targets.
func (c *Client) isExitIP(ctx context.Context, ip string) bool {
	if net.ParseIP(ip) == nil {
		return false
	}
	for _, t := range c.sel.Snapshot().Targets {
		if t.Host == ip {
			return true
		}
		if net.ParseIP(t.Host) == nil {
			ips, err := net.DefaultResolver.LookupHost(ctx, t.Host)
			if err == nil {
				for _, resolved := range ips {
					if resolved == ip {
						return true
					}
				}
			}
		}
	}
	return false
}

func probeIP(server, target string) (string, error) {
	u, err := url.Parse("http://" + server)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		Timeout:   25 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(u)},
	}
	resp, err := client.Get(target)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}
