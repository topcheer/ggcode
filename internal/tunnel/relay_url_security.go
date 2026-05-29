package tunnel

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

func validateRelayURLSecurity(relayURL string) error {
	parsed, err := parseRelayURLBase(relayURL)
	if err != nil {
		return err
	}
	if !relayURLUsesInsecureTransport(parsed) {
		return nil
	}
	host := strings.TrimSpace(parsed.Hostname())
	if isLocalRelayHost(host) {
		return nil
	}
	return fmt.Errorf("insecure relay URL requires a local/private host: %s", parsed.Redacted())
}

func parseRelayURLBase(relayURL string) (*url.URL, error) {
	base := strings.TrimSpace(strings.TrimSuffix(relayURL, "/"))
	if base == "" {
		return nil, fmt.Errorf("empty relay URL")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("parse relay URL: %w", err)
	}
	switch parsed.Scheme {
	case "ws", "wss", "http", "https":
	default:
		return nil, fmt.Errorf("unsupported relay scheme %q", parsed.Scheme)
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return nil, fmt.Errorf("relay URL missing host")
	}
	return parsed, nil
}

func relayURLUsesInsecureTransport(parsed *url.URL) bool {
	return parsed != nil && (parsed.Scheme == "ws" || parsed.Scheme == "http")
}

func isLocalRelayHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" {
		return false
	}
	lower := strings.ToLower(host)
	if lower == "localhost" ||
		lower == "host.docker.internal" ||
		lower == "gateway.docker.internal" ||
		strings.HasSuffix(lower, ".local") ||
		!strings.Contains(lower, ".") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
