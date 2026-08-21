package util

import (
	"net/url"
	"testing"
)

// Table-driven PAC directive tests - the parsing lives in
// sysproxy_directive.go precisely so these run on every dev OS and in CI,
// not only on windows runners (review feedback: keep the windows-only
// surface as thin as possible).
func TestParsePACFirstDirective(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"PROXY proxy1:8080; PROXY proxy2:8080; DIRECT", "PROXY proxy1:8080"},
		{"DIRECT", "DIRECT"},
		{"  PROXY  a:1 ; DIRECT ", "PROXY  a:1"}, // trimmed, first only
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := ParsePACFirstDirective(c.in); got != c.want {
			t.Errorf("ParsePACFirstDirective(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestProxyURLFromDirective(t *testing.T) {
	mustURL := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return u
	}
	cases := []struct {
		name, in string
		want     *url.URL // nil = direct/no proxy
	}{
		{"proxy", "PROXY 127.0.0.1:8888", mustURL("http://127.0.0.1:8888")},
		{"proxy lower", "proxy corp-proxy:80", mustURL("http://corp-proxy:80")},
		{"https directive", "HTTPS proxy.example:443", mustURL("http://proxy.example:443")},
		{"socks5", "SOCKS5 10.0.0.1:1080", mustURL("socks5://10.0.0.1:1080")},
		{"socks maps to 5", "SOCKS 10.0.0.1:1080", mustURL("socks5://10.0.0.1:1080")},
		{"socks4 unsupported", "SOCKS4 10.0.0.1:1080", nil},
		{"direct", "DIRECT", nil},
		{"empty", "", nil},
		{"garbage", "WAT? 1.2.3.4", nil},
		{"malformed host", "PROXY ://", nil},
	}
	for _, c := range cases {
		got := ProxyURLFromDirective(c.in)
		var gotStr, wantStr string
		if got != nil {
			gotStr = got.String()
		}
		if c.want != nil {
			wantStr = c.want.String()
		}
		if gotStr != wantStr {
			t.Errorf("%s: ProxyURLFromDirective(%q) = %q, want %q", c.name, c.in, gotStr, wantStr)
		}
	}
}

// SmartProxyFunc must exist on every platform; on non-Windows it is the
// plain env proxy (behavior-parity with the pre-#761 transports).
func TestSmartProxyFuncReturnsFunc(t *testing.T) {
	f := SmartProxyFunc()
	if f == nil {
		t.Fatal("SmartProxyFunc must never return nil")
	}
}
