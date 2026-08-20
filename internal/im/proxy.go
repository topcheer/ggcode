package im

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// resolveProxy returns the proxy URL from adapter config or env fallback.
// Only checks the adapter's own config value, then a platform-specific env var.
// Returns "" if no proxy is configured (direct connection).
func resolveProxy(configValue, envKey string) string {
	if v := strings.TrimSpace(configValue); v != "" {
		return v
	}
	if envKey != "" {
		if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
			return v
		}
	}
	return ""
}

// setEnvTemp previously lived here: it set HTTPS_PROXY around nostr connects,
// but net/http caches the env-proxy func in a sync.Once at first use, so the
// hack either never took effect (cache already primed by earlier HTTP traffic)
// or permanently latched the temporary proxy into the process-wide transport
// (#758). Replaced by the per-host transport interceptor below.

// hostProxyRoutes maps destination hosts to the proxy they must use,
// overriding the environment default. Populated by adapters whose relays
// need a proxy (nostr), consulted by nostrTransportProxy.
var hostProxyRoutes = struct {
	sync.RWMutex
	m map[string]*url.URL
}{m: make(map[string]*url.URL)}

// proxyInterceptorOnce guards the one-time wrap of http.DefaultTransport's
// Proxy func. Wrapping is idempotent and behavior-preserving for hosts that
// are not registered: they fall through to http.ProxyFromEnvironment.
var proxyInterceptorOnce sync.Once

// RegisterHostProxy pins proxyURL for all requests to host (host:port form
// as it appears in request URLs). Call UnregisterHostProxy when the adapter
// stops using the host.
func RegisterHostProxy(host, proxyURL string) error {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
	}
	hostProxyRoutes.Lock()
	hostProxyRoutes.m[host] = u
	hostProxyRoutes.Unlock()
	installProxyInterceptor()
	return nil
}

// UnregisterHostProxy removes a host's pinned proxy.
func UnregisterHostProxy(host string) {
	hostProxyRoutes.Lock()
	delete(hostProxyRoutes.m, host)
	hostProxyRoutes.Unlock()
}

// pinnedProxyFor returns the registered proxy for the request's host, or
// (nil, false) when the host has no pinned proxy.
func pinnedProxyFor(req *http.Request) (*url.URL, bool) {
	hostProxyRoutes.RLock()
	u, hit := hostProxyRoutes.m[req.URL.Host]
	hostProxyRoutes.RUnlock()
	return u, hit
}

// installProxyInterceptor wraps http.DefaultTransport.Proxy so registered
// hosts are routed through their pinned proxy while every other request
// keeps the standard env behavior. Unlike the old HTTPS_PROXY env hack this
// is deterministic (no sync.Once env caching) and scoped (no process-wide
// pollution of unrelated traffic).
func installProxyInterceptor() {
	proxyInterceptorOnce.Do(func() {
		t, ok := http.DefaultTransport.(*http.Transport)
		if !ok || t == nil {
			return // custom transport; do not touch
		}
		orig := t.Proxy
		if orig == nil {
			orig = http.ProxyFromEnvironment
		}
		t.Proxy = func(req *http.Request) (*url.URL, error) {
			if u, hit := pinnedProxyFor(req); hit {
				return u, nil
			}
			return orig(req)
		}
	})
}

// proxyDial creates a TCP connection through an HTTP CONNECT or SOCKS5 proxy.
// Returns a raw net.Conn ready for TLS wrapping if needed.
func proxyDial(proxyURL, targetAddr string) (net.Conn, error) {
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "http", "https", "":
		return httpConnectDial(parsed, targetAddr)
	case "socks5", "socks5h":
		return socks5Dial(parsed, targetAddr)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (use http, https, or socks5)", scheme)
	}
}

// httpConnectDial connects through an HTTP CONNECT proxy tunnel.
func httpConnectDial(proxy *url.URL, targetAddr string) (net.Conn, error) {
	proxyAddr := proxy.Host
	if !strings.Contains(proxyAddr, ":") {
		proxyAddr = proxyAddr + ":8080"
	}

	useTLS := strings.ToLower(proxy.Scheme) == "https"
	var conn net.Conn
	var err error

	if useTLS {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", proxyAddr, &tls.Config{})
	} else {
		conn, err = net.DialTimeout("tcp", proxyAddr, 15*time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("dial proxy %s: %w", proxyAddr, err)
	}

	// Build CONNECT request
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", targetAddr, targetAddr)

	// Add proxy auth if configured
	if proxy.User != nil {
		username := proxy.User.Username()
		password, _ := proxy.User.Password()
		if username != "" {
			creds := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
			connectReq += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", creds)
		}
	}
	connectReq += "\r\n"

	if _, err := fmt.Fprint(conn, connectReq); err != nil {
		conn.Close()
		return nil, fmt.Errorf("CONNECT write: %w", err)
	}

	// Read proxy response
	reader := bufio.NewReader(conn)
	resp, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("CONNECT read: %w", err)
	}
	if !strings.Contains(resp, "200") {
		conn.Close()
		return nil, fmt.Errorf("proxy refused CONNECT to %s: %s", targetAddr, strings.TrimSpace(resp))
	}

	// Consume remaining headers until blank line
	for {
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			break
		}
	}

	return conn, nil
}

// socks5Dial connects through a SOCKS5 proxy.
func socks5Dial(proxy *url.URL, targetAddr string) (net.Conn, error) {
	proxyAddr := proxy.Host
	if !strings.Contains(proxyAddr, ":") {
		proxyAddr = proxyAddr + ":1080"
	}

	conn, err := net.DialTimeout("tcp", proxyAddr, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial socks5 proxy %s: %w", proxyAddr, err)
	}

	// SOCKS5 handshake: no auth
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting: %w", err)
	}
	buf := make([]byte, 2)
	if _, err := conn.Read(buf); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting response: %w", err)
	}
	if buf[0] != 0x05 || buf[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 auth not supported: %x", buf)
	}

	// SOCKS5 connect
	host, portStr, _ := net.SplitHostPort(targetAddr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	ip := net.ParseIP(host)
	var req []byte
	if ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = []byte{0x05, 0x01, 0x00, 0x01, ip4[0], ip4[1], ip4[2], ip4[3],
				byte(port >> 8), byte(port)}
		} else {
			req = []byte{0x05, 0x01, 0x00, 0x04}
			req = append(req, ip.To16()...)
			req = append(req, byte(port>>8), byte(port))
		}
	} else {
		req = []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
		req = append(req, []byte(host)...)
		req = append(req, byte(port>>8), byte(port))
	}

	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect write: %w", err)
	}

	resp := make([]byte, 256)
	n, err := conn.Read(resp)
	if err != nil || n < 4 {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect response: %w", err)
	}
	if resp[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect failed: status %d", resp[1])
	}

	return conn, nil
}
