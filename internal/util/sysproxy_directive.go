package util

import (
	"net/url"
	"strings"
)

// PAC directive parsing - platform-neutral so it is unit-testable on any
// dev OS (the Windows-only pieces live in sysproxy_windows.go).
//
// A PAC FindProxyForURL return value looks like:
//
//	"PROXY proxy1:8080; PROXY proxy2:8080; DIRECT"
//
// We take the first directive; per-fault fallback across the list needs
// connection-level plumbing http.Transport does not expose.

// ParsePACFirstDirective returns the first PAC directive from a
// FindProxyForURL result, or "" when the result is empty/whitespace.
func ParsePACFirstDirective(result string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(result), ";", 2)[0])
}

// ProxyURLFromDirective converts a single PAC directive into an http
// transport proxy URL. Returns nil for DIRECT, empty, or malformed
// directives (the caller treats nil as "no proxy for this request").
// SOCKS5 directives map to socks5:// URLs which http.Transport dials
// natively; SOCKS4 has no stdlib mapping and yields nil.
func ProxyURLFromDirective(directive string) *url.URL {
	d := strings.TrimSpace(directive)
	upper := strings.ToUpper(d)
	switch {
	case d == "", upper == "DIRECT":
		return nil
	case strings.HasPrefix(upper, "PROXY "), strings.HasPrefix(upper, "HTTPS "):
		return parseHostPortDirective(d[6:], "http")
	case strings.HasPrefix(upper, "SOCKS5 "):
		return parseHostPortDirective(d[len("SOCKS5 "):], "socks5")
	case strings.HasPrefix(upper, "SOCKS "):
		// PAC "SOCKS" means SOCKS4 per spec but is universally served by
		// SOCKS5-capable servers; map both to socks5 (SOCKS4 is extinct).
		// NOTE: slice by len("SOCKS ") (6), NOT len("SOCKS5 ") (7) - the
		// mismatch trimmed the first host char ("10.0.0.1" -> "0.0.1").
		return parseHostPortDirective(d[len("SOCKS "):], "socks5")
	case strings.HasPrefix(upper, "SOCKS4 "):
		return nil // no stdlib socks4 dialer
	default:
		return nil
	}
}

func parseHostPortDirective(raw, scheme string) *url.URL {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = scheme + "://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil
	}
	return u
}
