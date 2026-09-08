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
	"sync"
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
	// #1751: resolved IPs of the entries above, for dial-time checks.
	// #1889: hostname resolution is a snapshot that goes stale when the
	// LAN collector renews its DHCP lease - keep the hostnames that need
	// resolution and re-resolve (rate-limited) when a dial-time check
	// misses, so registration-side and delivery-side agreement survives
	// DNS drift without a server restart.
	allowHostIPs []net.IP
	resolveNames []string
	mu           sync.Mutex
	lastResolve  time.Time
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
		// #1751 case 1: the Control hook sees the kernel-resolved IP, not the
		// hostname - a bare-IP or hostname entry was never consulted there,
		// so the delivery side rejected 100% of what the registration side
		// exempted. Resolve what's resolvable now and keep the IP set for the
		// dial-time check.
		if ip := net.ParseIP(entry); ip != nil {
			g.allowHostIPs = append(g.allowHostIPs, ip)
			continue
		}
		g.resolveNames = append(g.resolveNames, entry)
		// #1889: per-entry ctx (a shared one starved later entries) and a
		// log on failure (it used to fail silently and look exactly like a
		// stale snapshot - undiagnosable).
		ectx, ecancel := context.WithTimeout(context.Background(), 3*time.Second)
		addrs, err := net.DefaultResolver.LookupIPAddr(ectx, entry)
		ecancel()
		if err != nil {
			debug.Log("a2a.push", "push allowlist hostname %q failed to resolve at startup: %v", entry, err)
		} else {
			for _, a := range addrs {
				g.allowHostIPs = append(g.allowHostIPs, a.IP)
			}
		}
	}
	return g
}

// refreshResolvedHosts re-resolves the allowlisted hostnames at most once
// per resolveTTL. Called from ipAllowed on a miss, before rejecting: a
// DHCP/DDNS lease change moves the collector to an IP that is not in the
// startup snapshot, which would otherwise break delivery again (#1889).
const pushGuardResolveTTL = 60 * time.Second

func (g *pushGuard) refreshResolvedHosts() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Since(g.lastResolve) < pushGuardResolveTTL || len(g.resolveNames) == 0 {
		return
	}
	g.lastResolve = time.Now()
	// Keep ALL existing IPs and append fresh resolutions - stale hostname
	// IPs are harmless (they simply stop matching), while dropping them
	// could break a peer whose DNS has not propagated everywhere yet.
	for _, name := range g.resolveNames {
		ectx, ecancel := context.WithTimeout(context.Background(), 3*time.Second)
		addrs, err := net.DefaultResolver.LookupIPAddr(ectx, name)
		ecancel()
		if err != nil {
			debug.Log("a2a.push", "push allowlist hostname %q re-resolve failed: %v", name, err)
			continue
		}
		for _, a := range addrs {
			if !containsIP(g.allowHostIPs, a.IP) {
				g.allowHostIPs = append(g.allowHostIPs, a.IP)
			}
		}
	}
}

func containsIP(ips []net.IP, ip net.IP) bool {
	for _, v := range ips {
		if v.Equal(ip) {
			return true
		}
	}
	return false
}

// hostAllowed reports whether the literal URL hostname is allowlisted.
func (g *pushGuard) hostAllowed(host string) bool {
	if g == nil || len(g.allowHosts) == 0 {
		return false
	}
	_, ok := g.allowHosts[strings.ToLower(strings.TrimSpace(host))]
	return ok
}

// ipAllowed reports whether an IP falls inside an allowlisted CIDR or
// matches an allowlisted host's resolved IP (#1751 case 1: hostname/bare-IP
// entries must survive into the dial-time check, or delivery is rejected
// while registration exempted it). #1889: on a miss, refresh the hostname
// resolutions first (rate-limited) - a DHCP lease change moves the collector
// off the startup snapshot and the miss is stale, not hostile.
func (g *pushGuard) ipAllowed(ip net.IP) bool {
	if g == nil {
		return false
	}
	for _, ipNet := range g.allowCIDRs {
		if ipNet.Contains(ip) {
			return true
		}
	}
	g.mu.Lock()
	hostIPs := make([]net.IP, len(g.allowHostIPs))
	copy(hostIPs, g.allowHostIPs)
	g.mu.Unlock()
	if containsIP(hostIPs, ip) {
		return true
	}
	g.refreshResolvedHosts()
	g.mu.Lock()
	hostIPs = make([]net.IP, len(g.allowHostIPs))
	copy(hostIPs, g.allowHostIPs)
	g.mu.Unlock()
	return containsIP(hostIPs, ip)
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
// pushControlHook returns the Dialer Control function that pins the
// kernel-resolved IP at connection establishment (#1463-A - the
// rebinding-proof place to check).
// #1568-A: allowlisted IPs are exempt - registration exempts allowlisted
// hosts/CIDRs and the header comment promises "Allowlisted hosts and CIDRs
// are exempt", but the old closure pinned EVERY private IP: the advertised
// main use case (private collectors) registered fine and then had 100% of
// deliveries blocked as "disallowed IP".
func pushControlHook(guard *pushGuard) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip != nil {
			if guard != nil && guard.ipAllowed(ip) {
				return nil
			}
			if isDisallowedCallbackIP(ip) {
				return fmt.Errorf("dial to disallowed IP %s blocked (rebinding guard)", host)
			}
		}
		return nil
	}
}

func (s *Server) pushHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: pushControlHook(s.pushGuard),
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
