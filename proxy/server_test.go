package proxy

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/xeri/upvpn/provider"
)

var originTransport = &http.Transport{}

const wantBasic = "Basic " + "dTpw" // base64("u:p")

func TestServerPlainHTTP(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello-origin")
	}))
	defer origin.Close()

	addr, authCh := startProxy(t, targetFor(origin.URL))
	resp, err := proxyClient(t, addr, false).Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello-origin" {
		t.Fatalf("body = %q, want hello-origin", body)
	}
	if got := waitAuth(t, authCh); got != wantBasic {
		t.Fatalf("upstream auth = %q, want %q", got, wantBasic)
	}
}

func TestServerConnect(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello-tls")
	}))
	defer origin.Close()

	addr, authCh := startProxy(t, targetFor(origin.URL))
	resp, err := proxyClient(t, addr, true).Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello-tls" {
		t.Fatalf("body = %q, want hello-tls", body)
	}
	if got := waitAuth(t, authCh); got != wantBasic {
		t.Fatalf("upstream auth = %q, want %q", got, wantBasic)
	}
}

// startProxy runs the local proxy in front of a fake upstream forward proxy and
// returns the local address plus a channel with the auth header each upstream
// request carried.
func startProxy(t *testing.T, target provider.Target) (string, chan string) {
	t.Helper()
	authCh := make(chan string, 8)
	upstream := httptest.NewServer(fakeUpstream(authCh))
	t.Cleanup(upstream.Close)

	host, port, err := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	target.Host, target.Port = host, port

	sel := NewSelector([]provider.Provider{&fakeProvider{name: "fake", targets: []provider.Target{target}}},
		"round-robin", 1, time.Minute, discardLog())
	sel.Refresh(t.Context())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go NewServer(sel, discardLog()).Serve(t.Context(), ln)
	return ln.Addr().String(), authCh
}

func fakeUpstream(authCh chan string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case authCh <- r.Header.Get("Proxy-Authorization"):
		default:
		}
		if r.Method == http.MethodConnect {
			if r.Header.Get("Proxy-Authorization") == "" {
				w.WriteHeader(http.StatusProxyAuthRequired)
				return
			}
			dst, err := net.Dial("tcp", r.Host)
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				dst.Close()
				return
			}
			conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
			go func() { io.Copy(dst, conn); conn.Close() }()
			io.Copy(conn, dst)
			return
		}
		out := r.Clone(r.Context())
		out.RequestURI = ""
		resp, err := originTransport.RoundTrip(out)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})
}

func targetFor(rawURL string) provider.Target {
	u, _ := url.Parse(rawURL)
	return provider.Target{Scheme: "http", Username: "u", Password: "p", Provider: "fake", Host: u.Hostname(), Port: u.Port()}
}

func proxyClient(t *testing.T, addr string, insecure bool) *http.Client {
	t.Helper()
	u, _ := url.Parse("http://" + addr)
	tr := &http.Transport{Proxy: http.ProxyURL(u)}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func waitAuth(t *testing.T, ch chan string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		return ""
	}
}
