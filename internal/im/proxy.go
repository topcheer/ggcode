package im

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

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

var hostProxyRoutes = struct {
	sync.RWMutex
	m map[string]*url.URL
}{m: make(map[string]*url.URL)}

var proxyInterceptorOnce sync.Once

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

func UnregisterHostProxy(host string) {
	hostProxyRoutes.Lock()
	delete(hostProxyRoutes.m, host)
	hostProxyRoutes.Unlock()
}

func pinnedProxyFor(req *http.Request) (*url.URL, bool) {
	hostProxyRoutes.RLock()
	u, hit := hostProxyRoutes.m[req.URL.Host]
	hostProxyRoutes.RUnlock()
	return u, hit
}

func installProxyInterceptor() {
	proxyInterceptorOnce.Do(func() {
		t, ok := http.DefaultTransport.(*http.Transport)
		if !ok || t == nil {
			return
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

	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", targetAddr, targetAddr)

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

	for {
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			break
		}
	}

	return conn, nil
}

func socks5Dial(proxy *url.URL, targetAddr string) (net.Conn, error) {
	proxyAddr := proxy.Host
	if !strings.Contains(proxyAddr, ":") {
		proxyAddr = proxyAddr + ":1080"
	}

	conn, err := net.DialTimeout("tcp", proxyAddr, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial socks5 proxy %s: %w", proxyAddr, err)
	}

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting: %w", err)
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting response: %w", err)
	}
	if buf[0] != 0x05 || buf[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 auth not supported: %x", buf)
	}

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

	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect response: %w", err)
	}
	if head[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect failed: status %d", head[1])
	}

	var boundLen int
	switch head[3] {
	case 0x01:
		boundLen = 4
	case 0x03:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 connect response bound addr: %w", err)
		}
		boundLen = int(lenBuf[0])
	case 0x04:
		boundLen = 16
	default:
		conn.Close()
		return nil, fmt.Errorf("socks5 connect failed: unknown address type %d", head[3])
	}
	if _, err := io.CopyN(io.Discard, conn, int64(boundLen+2)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect response bound addr: %w", err)
	}

	return conn, nil
}
