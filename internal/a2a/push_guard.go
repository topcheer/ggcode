package a2a

// Push callback SSRF guard (#715).
//
// The A2A spec lets the CLIENT choose the push-notification callback URL.
// Before this guard, handlePushConfigSet stored any URL as-is and
// firePushNotifications POSTed every task status snapshot there via
// http.DefaultClient (which follows redirects). A LAN peer that
// authenticated with the well-known default key could register
// http://169.254.169.254/... or an RFC1918 target and use the agent as a
// blind SSRF relay into internal networks, while exfiltrating every task
// snapshot to an arbitrary external URL.
//
// Guard rules (enforced at registration AND on every redirect hop):
//   - URL must be absolute with a host.
//   - Scheme must be https. Plain http is only accepted for hosts that are
//     explicitly allowlisted (PushCallbackAllowlist).
//   - The host must not resolve to loopback / RFC1918 / ULA / link-local
//     (covers 169.254.169.254 metadata) / multicast / unspecified ranges.
//     Allowlisted hosts and CIDRs are exempt (explicit operator opt-in).
//   - Configs with TaskID == "" match ALL tasks; they require the
//     AllowWildcardPushCallbacks opt-in.
//   - When the deployment authenticates with only the default public key
//     (config.DefaultA2AAPIKey) or has no auth at all, push registration is
//     refused outright — there is no way to tell an attacker's registration
//     from a peer's, so the data-exfil channel stays closed until a real
//     key / token validator / mTLS is configured.

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

// pushValidationDNSTimeout caps hostname resolution during URL validation so
// a bogus hostname cannot stall the JSON-RPC handler.
const pushValidationDNSTimeout = 3 * time.Second

// pushGuard holds the parsed explicit allowlist for callback targets that
// the default rules would reject (private/loopback ranges, plain http).
type pushGuard struct {
	allowCIDRs []*net.IPNet
	allowHosts map[string]struct{} // lowercase hostnames / bare IPs
}

// newPushGuard parses allowlist entries. Accepted forms:
//   - CIDR: "10.0.0.0/8", "fd00::/8" — any resolved IP inside is allowed.
//   - bare IP or hostname: "collector.lan", "127.0.0.1" — exact host match
//     (case-insensitive); exempts that host from scheme/range checks.
//
// Invalid entries are logged and skipped (never widen the guard silently).
func newPushGuard(allowlist []string) *pushGuard {
	g := &pushGuard{allowHosts: make(map[string]struct{})}
	for _, entry := range allowlist {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, ipNet, err := net.ParseCIDR(entry); err == nil {
				g.allowCIDRs = append(g.allowCIDRs, ipNet)
			} else {
				debug.Log("a2a.push", "ignoring invalid push allowlist CIDR %q: %v", entry, err)
			}
			continue
		}
		g.allowHosts[entry] = struct{}{}
	}
	return g
}

// hostAllowed reports whether the literal URL hostname is allowlisted.
func (g *pushGuard) hostAllowed(host string) bool {
	if g == nil || len(g.allowHosts) == 0 {
		return false
	}
	_, ok := g.allowHosts[strings.ToLower(strings.TrimSpace(host))]
	return ok
}

// ipAllowed reports whether an IP falls inside an allowlisted CIDR.
func (g *pushGuard) ipAllowed(ip net.IP) bool {
	if g == nil {
		return false
	}
	for _, ipNet := range g.allowCIDRs {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

// isDisallowedCallbackIP reports whether an IP is in a range the push guard
// refuses by default: loopback, RFC1918/ULA private, link-local (including
// the 169.254.169.254 metadata endpoint), multicast, unspecified.
func isDisallowedCallbackIP(ip net.IP) bool {
	return ip == nil ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// checkPushScheme enforces the https-only rule (#715). Plain http is only
// accepted for targets the operator explicitly allowlisted (hostname or
// CIDR) — that prevents downgrade-to-http exfiltration while still letting
// internal collectors receive callbacks.
func checkPushScheme(u *url.URL, hostListed, ipListed bool) error {
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if hostListed || ipListed {
			return nil
		}
		return fmt.Errorf("plain http is only allowed for hosts in the push callback allowlist")
	default:
		return fmt.Errorf("scheme must be https (got %q)", u.Scheme)
	}
}

// validatePushHost checks that the URL hostname is routable (#715):
// literal IPs must not be loopback/private/link-local/multicast; hostnames
// are resolved (bounded by pushValidationDNSTimeout) and EVERY resolved
// address must be routable — rejecting when any address is private also
// defeats partial DNS-rebinding setups.
func validatePushHost(host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if isDisallowedCallbackIP(ip) {
			return fmt.Errorf("IP %s is in a non-routable/private range", host)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), pushValidationDNSTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("cannot resolve host %q: %v", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("host %q resolved to no addresses", host)
	}
	for _, addr := range addrs {
		if isDisallowedCallbackIP(addr.IP) {
			return fmt.Errorf("host %q resolves to non-routable/private address %s", host, addr.IP)
		}
	}
	return nil
}

// validatePushCallbackURL enforces the #715 rules on a callback URL.
func (s *Server) validatePushCallbackURL(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	if !u.IsAbs() || u.Hostname() == "" {
		return fmt.Errorf("must be an absolute http(s) URL with a host")
	}

	host := u.Hostname()
	hostListed := s.pushGuard.hostAllowed(host)
	ipListed := false
	if ip := net.ParseIP(host); ip != nil && s.pushGuard.ipAllowed(ip) {
		ipListed = true
	}

	if err := checkPushScheme(u, hostListed, ipListed); err != nil {
		return err
	}

	// Explicitly allowlisted targets are exempt from range checks — the
	// operator vouched for them.
	if hostListed || ipListed {
		return nil
	}
	return validatePushHost(host)
}

// pushRegistrationDisabled reports why push-config registration must be
// refused (#715), or "" when registration is allowed. Push callbacks stream
// every task status snapshot to a third-party URL, so they require real
// authentication: an explicit non-default API key, a token validator, or
// mTLS. The well-known default key is public (documented at
// config.DefaultA2AAPIKey) and "no auth" trusts the whole LAN — with either,
// any peer can register an exfiltration endpoint.
func (s *Server) pushRegistrationDisabled() string {
	if s.tokenValidator != nil || s.mtlsEnabled {
		return ""
	}
	if len(s.apiKeys) == 0 {
		return "no authentication configured"
	}
	for _, key := range s.apiKeys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(key), []byte(config.DefaultA2AAPIKey)) != 1 {
			return "" // at least one real, non-public key
		}
	}
	return "only the default public API key is configured"
}

// pushHTTPClient returns the dedicated client used for callback delivery.
// http.DefaultClient followed redirects to arbitrary (internal) targets and
// had no timeout; this client has both a hard timeout and a CheckRedirect
// that re-validates every hop with the same rules as registration.
// #1463-A: validation-side LookupIPAddr and dial-side resolution were
// TWO INDEPENDENT DNS lookups (Transport nil -> DefaultTransport dials and
// resolves on its own) - a rebinding attacker answers the validation query
// with a public IP and the dial query with 169.254.169.254/127.0.0.1.
// The transport's Control hook now validates the IP the kernel is about
// to connect to - pinning at connection time, the standard rebinding
// defense.
func (s *Server) pushHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		// #1463-A: Control fires at connection establishment with the
		// kernel-resolved address - the rebinding-proof place to check.
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip != nil {
				if isDisallowedCallbackIP(ip) {
					return fmt.Errorf("dial to disallowed IP %s blocked (rebinding guard)", host)
				}
			}
			return nil
		},
	}
	transport := &http.Transport{DialContext: dialer.DialContext}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			if err := s.validatePushCallbackURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect to disallowed target: %w", err)
			}
			return nil
		},
	}
}
