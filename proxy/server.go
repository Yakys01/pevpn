// Package proxy implements the local HTTP proxy that injects upstream
// credentials and tunnels to whichever target the selector picks.
package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/xeri/upvpn/provider"
)

const (
	dialTimeout = 8 * time.Second
	// firstByteTimeout bounds how long an upstream may stay silent before we
	// treat it as stalled. Covers proxies that accept CONNECT then never
	// forward (there is no other signal). Idle pooled tunnels are closed too;
	// clients reopen them.
	firstByteTimeout = 15 * time.Second
	idleTimeout      = 5 * time.Minute
	maxAttempts      = 2
)

// Server proxies client requests through the selector's targets.
type Server struct {
	sel *Selector
	log *slog.Logger
}

// NewServer builds a proxy server.
func NewServer(sel *Selector, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{sel: sel, log: log}
}

// Serve accepts connections until ctx is cancelled or the listener fails.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handle(ctx, conn)
	}
}

func (s *Server) handle(ctx context.Context, client net.Conn) {
	defer client.Close()
	client.SetDeadline(time.Now().Add(idleTimeout))
	req, err := http.ReadRequest(bufio.NewReader(client))
	if err != nil {
		return
	}
	s.forward(ctx, client, req)
}

func (s *Server) forward(ctx context.Context, client net.Conn, req *http.Request) {
	dest := req.Host
	if req.Method != http.MethodConnect {
		dest = requestDest(req)
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		t, ok := s.sel.Pick()
		if !ok {
			s.writeStatus(client, http.StatusBadGateway, "upvpn: no upstream targets")
			return
		}
		s.log.Info("upvpn: request",
			"dest", dest,
			"provider", t.Provider,
			"endpoint", t.Addr(),
			"scheme", t.Scheme,
			"attempt", attempt+1,
		)
		if req.Method == http.MethodConnect {
			up, rdr, status, err := dialConnect(ctx, t, req.Host)
			if err != nil {
				s.sel.Report(t, false)
				lastErr = err
				continue
			}
			switch {
			case status == http.StatusProxyAuthRequired:
				up.Close()
				s.sel.Report(t, false)
				s.log.Info("upvpn: upstream 407, refreshing credentials", "provider", t.Provider)
				s.sel.Refresh(ctx)
				lastErr = fmt.Errorf("upstream %s rejected credentials", t.Provider)
				continue
			case status != http.StatusOK:
				up.Close()
				s.sel.Report(t, false)
				s.writeStatus(client, http.StatusBadGateway, "upvpn: upstream "+http.StatusText(status))
				return
			}
			s.sel.Report(t, true)
			io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
			s.sel.Report(t, relay(client, rdr, up))
			return
		}

		if t.Scheme == "https" {
			up, rdr, status, err := dialConnect(ctx, t, requestDest(req))
			if err != nil {
				s.sel.Report(t, false)
				lastErr = err
				continue
			}
			if status == http.StatusProxyAuthRequired {
				up.Close()
				s.sel.Report(t, false)
				s.sel.Refresh(ctx)
				lastErr = fmt.Errorf("upstream %s rejected credentials", t.Provider)
				continue
			}
			if status != http.StatusOK {
				up.Close()
				s.sel.Report(t, false)
				s.writeStatus(client, http.StatusBadGateway, "upvpn: upstream "+http.StatusText(status))
				return
			}
			req.RequestURI = ""
			req.Header.Del("Proxy-Authorization")
			req.Header.Del("Proxy-Connection")
			if err := req.Write(up); err != nil {
				up.Close()
				s.sel.Report(t, false)
				lastErr = err
				continue
			}
			up.SetReadDeadline(time.Now().Add(firstByteTimeout))
			resp, err := http.ReadResponse(rdr, req)
			if err != nil {
				up.Close()
				s.sel.Report(t, false)
				lastErr = err
				continue
			}
			up.SetReadDeadline(time.Time{})
			s.sel.Report(t, true)
			resp.Write(client)
			up.Close()
			return
		}

		up, err := net.DialTimeout("tcp", t.Addr(), dialTimeout)
		if err != nil {
			s.sel.Report(t, false)
			lastErr = err
			continue
		}
		rdr := bufio.NewReader(up)
		req.RequestURI = ""
		req.Header.Set("Proxy-Authorization", basicAuth(t))
		req.Header.Del("Proxy-Connection")
		if err := req.WriteProxy(up); err != nil {
			up.Close()
			s.sel.Report(t, false)
			lastErr = err
			continue
		}
		up.SetReadDeadline(time.Now().Add(firstByteTimeout))
		resp, err := http.ReadResponse(rdr, req)
		if err != nil {
			up.Close()
			s.sel.Report(t, false)
			lastErr = err
			continue
		}
		up.SetReadDeadline(time.Time{})
		if resp.StatusCode == http.StatusProxyAuthRequired {
			resp.Body.Close()
			up.Close()
			s.sel.Report(t, false)
			s.sel.Refresh(ctx)
			lastErr = fmt.Errorf("upstream %s rejected credentials", t.Provider)
			continue
		}
		s.sel.Report(t, true)
		resp.Write(client)
		up.Close()
		return
	}

	s.log.Warn("upvpn: forwarding failed", "err", lastErr)
	s.writeStatus(client, http.StatusBadGateway, "upvpn: upstream unreachable")
}

// dialConnect opens a tunnel to dest through target t. For an https upstream
// the connection is TCP+TLS; the proxy itself still speaks HTTP/1.1 CONNECT.
func dialConnect(ctx context.Context, t provider.Target, dest string) (net.Conn, *bufio.Reader, int, error) {
	d := &net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", t.Addr())
	if err != nil {
		return nil, nil, 0, err
	}
	if t.Scheme == "https" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: t.Host})
		tlsConn.SetDeadline(time.Now().Add(dialTimeout))
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, nil, 0, err
		}
		tlsConn.SetDeadline(time.Time{})
		conn = tlsConn
	}
	rdr := bufio.NewReader(conn)
	conn.SetDeadline(time.Now().Add(firstByteTimeout))
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\nProxy-Connection: keep-alive\r\n\r\n",
		dest, dest, basicAuth(t))
	status, err := readHead(rdr)
	if err != nil {
		conn.Close()
		return nil, nil, 0, err
	}
	conn.SetDeadline(time.Time{})
	return conn, rdr, status, nil
}

// readHead reads a status line plus headers up to the blank line and returns
// the status code. It deliberately avoids http.ReadResponse, whose body
// handling would drain a CONNECT tunnel.
func readHead(rdr *bufio.Reader) (int, error) {
	status := 0
	for {
		line, err := rdr.ReadString('\n')
		if err != nil {
			return 0, err
		}
		line = strings.TrimRight(line, "\r\n")
		if status == 0 {
			parts := strings.SplitN(line, " ", 3)
			if len(parts) < 2 {
				return 0, fmt.Errorf("malformed status line %q", line)
			}
			if status, err = strconv.Atoi(parts[1]); err != nil {
				return 0, err
			}
		}
		if line == "" {
			return status, nil
		}
	}
}

// relay pumps bytes both ways until either side closes. It reports whether the
// upstream produced any byte before firstByteTimeout; false means it stalled.
func relay(client net.Conn, upRdr *bufio.Reader, up net.Conn) bool {
	var flowed atomic.Bool
	errs := make(chan struct{}, 2)
	go func() {
		io.Copy(up, client)
		errs <- struct{}{}
	}()
	go func() {
		up.SetReadDeadline(time.Now().Add(firstByteTimeout))
		buf := make([]byte, 32*1024)
		n, err := upRdr.Read(buf)
		if n > 0 {
			flowed.Store(true)
			up.SetReadDeadline(time.Time{})
			if _, werr := client.Write(buf[:n]); werr != nil {
				errs <- struct{}{}
				return
			}
		}
		if err == nil {
			io.Copy(client, upRdr)
		}
		errs <- struct{}{}
	}()
	<-errs
	client.Close()
	up.Close()
	<-errs
	return flowed.Load()
}

func requestDest(req *http.Request) string {
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "80")
	}
	return host
}

func basicAuth(t provider.Target) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(t.Username+":"+t.Password))
}

func (s *Server) writeStatus(client net.Conn, code int, msg string) {
	body := msg + "\n"
	fmt.Fprintf(client, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), len(body), body)
}
