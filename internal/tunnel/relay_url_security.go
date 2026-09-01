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
	// #1400-B: the old heuristic treated EVERY dotless name and every
	// *.local as local - but single-label names are registrable public
	// TLDs (ws://ai/relay) and unicast .local exists, so plaintext-allowed
	// credentials could go to a public host. Explicit local names only;
	// everything else must prove it via the IP ranges below.
	switch lower {
	case "localhost", "host.docker.internal", "gateway.docker.internal", "kubernetes.default.svc":
		return true
	}
	if ip := net.ParseIP(strings.Trim(lower, ".")); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
	}
	return false
}
