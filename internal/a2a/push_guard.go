package a2a

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

const pushValidationDNSTimeout = 3 * time.Second

type pushGuard struct {
	allowCIDRs   []*net.IPNet
	allowHosts   map[string]struct{}
	allowHostIPs []net.IP
	resolveNames []string
	mu           sync.Mutex
	lastResolve  time.Time
}

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
		if ip := net.ParseIP(entry); ip != nil {
			g.allowHostIPs = append(g.allowHostIPs, ip)
			continue
		}
		g.resolveNames = append(g.resolveNames, entry)
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

const pushGuardResolveTTL = 60 * time.Second

func (g *pushGuard) refreshResolvedHosts() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Since(g.lastResolve) < pushGuardResolveTTL || len(g.resolveNames) == 0 {
		return
	}
	g.lastResolve = time.Now()
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

func (g *pushGuard) hostAllowed(host string) bool {
	if g == nil || len(g.allowHosts) == 0 {
		return false
	}
	_, ok := g.allowHosts[strings.ToLower(strings.TrimSpace(host))]
	return ok
}

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

	if hostListed || ipListed {
		return nil
	}
	return validatePushHost(host)
}

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
			return ""
		}
	}
	return "only the default public API key is configured"
}

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
