package util

import (
	"net/url"
	"strings"
)

func ParsePACFirstDirective(result string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(result), ";", 2)[0])
}

func ProxyURLFromDirective(directive string) *url.URL {
	d := strings.TrimSpace(directive)
	upper := strings.ToUpper(d)
	switch {
	case d == "", upper == "DIRECT":
		return nil
	case strings.HasPrefix(upper, "PROXY "):
		return parseHostPortDirective(d[6:], "http")
	case strings.HasPrefix(upper, "HTTPS "):

		return parseHostPortDirective(d[6:], "https")
	case strings.HasPrefix(upper, "SOCKS5 "):
		return parseHostPortDirective(d[len("SOCKS5 "):], "socks5")
	case strings.HasPrefix(upper, "SOCKS "):

		return parseHostPortDirective(d[len("SOCKS "):], "socks5")
	case strings.HasPrefix(upper, "SOCKS4 "):
		return nil
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
