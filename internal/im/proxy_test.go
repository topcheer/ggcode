//go:build localproxy

package im

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestResolveProxy_Config(t *testing.T) {
	got := resolveProxy("http://192.168.1.1:7890", "")
	if got != "http://192.168.1.1:7890" {
		t.Errorf("got %q", got)
	}
}

func TestResolveProxy_Empty(t *testing.T) {
	got := resolveProxy("", "")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestResolveProxy_Env(t *testing.T) {
	os.Setenv("TEST_PROXY", "socks5://localhost:1080")
	defer os.Unsetenv("TEST_PROXY")
	got := resolveProxy("", "TEST_PROXY")
	if got != "socks5://localhost:1080" {
		t.Errorf("got %q", got)
	}
}

func TestProxyDial_HTTPConnect(t *testing.T) {
	// Start a fake target TCP server
	targetLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer targetLn.Close()
	targetAddr := targetLn.Addr().String()

	// Accept one connection on target and send hello
	go func() {
		conn, err := targetLn.Accept()
		if err != nil {
			return
		}
		conn.Write([]byte("hello from target"))
		conn.Close()
	}()

	// Start an HTTP CONNECT proxy
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("expected CONNECT, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.String(), targetAddr) {
			t.Errorf("expected target %s, got %s", targetAddr, r.URL.String())
		}

		// Tunnel
		targetConn, err := net.Dial("tcp", targetAddr)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("proxy cannot hijack")
		}
		proxyConn, _, _ := hj.Hijack()
		go func() {
			io.Copy(proxyConn, targetConn)
		}()
		io.Copy(targetConn, proxyConn)
	}))
	defer proxy.Close()

	proxyURL := "http://" + strings.TrimPrefix(proxy.URL, "http://")
	conn, err := proxyDial(proxyURL, targetAddr)
	if err != nil {
		t.Fatalf("proxyDial: %v", err)
	}
	defer conn.Close()

	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != "hello from target" {
		t.Errorf("got %q", buf[:n])
	}
}

func TestProxyDial_UnsupportedScheme(t *testing.T) {
	_, err := proxyDial("ftp://proxy:21", "target:80")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected unsupported error, got %v", err)
	}
}

func TestProxyDial_InvalidURL(t *testing.T) {
	_, err := proxyDial("://bad", "target:80")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}
