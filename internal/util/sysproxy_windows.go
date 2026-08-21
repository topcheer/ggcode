//go:build windows

package util

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-ieproxy"
	"github.com/saucelabs/forwarder/pac"
	"golang.org/x/net/http/httpproxy"
)

// Windows system-proxy + PAC support (#761).
//
// Historically every outbound transport used http.ProxyFromEnvironment,
// which ignores Windows "Internet Options" entirely: a user (or enterprise
// GPO) configuring a static proxy or a PAC/auto-config script got direct
// connections and hard-to-diagnose timeouts unless they also exported
// HTTPS_PROXY. This file gives transports a smarter Proxy func:
//
//  1. explicit env vars win first (existing behavior is a subset, so
//     nothing regresses for env-configured users and CI);
//  2. else the Windows static system proxy (registry via ieproxy,
//     per-protocol "http=...;https=..." formats handled);
//  3. else the PAC script from AutoConfigURL, fetched with a TTL and
//     evaluated per-request (forwarder/pac on goja, which ggcode already
//     depends on for unrelated reasons - no new JS engine).
//
// Broken PAC configuration degrades to DIRECT, never to a transport error:
// a bad proxy policy must not take down all networking.

const (
	pacScriptTTL    = 5 * time.Minute
	pacResultCap    = 512
	pacFetchTimeout = 10 * time.Second
)

var (
	sysProxyOnce sync.Once
	sysProxyFunc func(*http.Request) (*url.URL, error)
)

// SmartProxyFunc returns an http.Transport Proxy function that layers
// Windows system settings (static proxy, then PAC) under the environment
// variables. On non-Windows platforms this is a plain
// http.ProxyFromEnvironment alias (see sysproxy_other.go).
func SmartProxyFunc() func(*http.Request) (*url.URL, error) {
	sysProxyOnce.Do(func() {
		sysProxyFunc = buildWindowsProxyFunc()
	})
	return sysProxyFunc
}

type pacState struct {
	mu        sync.Mutex
	scriptURL string
	resolver  *pac.ProxyResolver
	fetchedAt time.Time
	// resultCache maps "scheme|host" -> first PAC directive ("" = DIRECT).
	// FindProxyForURL is deterministic per script, so caching per host is
	// safe; the cache is dropped when the script is re-fetched.
	resultCache sync.Map
}

// staticSystemProxy resolves the registry static proxy (ProxyEnable /
// ProxyServer / ProxyOverride) for a single request, mirroring
// ieproxy's staticProxy but never consulting the auto-config entry.
// Returns (nil, nil) when the static proxy is disabled.
func staticSystemProxy(req *http.Request) (*url.URL, error) {
	conf := ieproxy.GetConf()
	if conf.Automatic.Active || !conf.Static.Active || req.URL == nil {
		return nil, nil
	}
	cfg := httpproxy.Config{
		HTTPSProxy: protocolFallback(conf.Static.Protocols, "https"),
		HTTPProxy:  protocolFallback(conf.Static.Protocols, "http"),
		NoProxy:    conf.Static.NoProxy,
	}
	return cfg.ProxyFunc()(req.URL)
}

// protocolFallback returns the per-protocol static proxy, falling back to
// the protocol-less default entry ("http=...;https=..." vs "host:port").
func protocolFallback(m map[string]string, proto string) string {
	if v, ok := m[proto]; ok {
		return v
	}
	return m[""]
}

var thePAC pacState

func buildWindowsProxyFunc() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		// 1. Environment always wins: preserves every existing setup.
		if u, err := http.ProxyFromEnvironment(req); err != nil || u != nil {
			return u, err
		}

		// 2. Static system proxy. Read the registry conf ourselves and
		// build an httpproxy.Config only from the static entry. We must NOT
		// use ieproxy.GetProxyFunc() here: when AutoConfig is active it
		// resolves PAC itself via WinHTTP and returns scheme-less URLs
		// (&url.URL{Host: ...}), which both short-circuits our own PAC
		// engine below (dead code) and mis-handles SOCKS directives.
		if u, err := staticSystemProxy(req); err == nil && u != nil {
			return u, nil
		}

		// 3. PAC / auto-config script.
		return pacProxy(req)
	}
}

// pacProxy resolves req through the PAC script when the system has an
// auto-config URL configured. Returns (nil, nil) = direct connection.
func pacProxy(req *http.Request) (*url.URL, error) {
	conf := ieproxy.GetConf()
	if !conf.Automatic.Active || req.URL == nil || req.URL.Host == "" {
		return nil, nil
	}
	scriptURL := strings.TrimSpace(conf.Automatic.PreConfiguredURL)
	if scriptURL == "" {
		// Active auto-config without a preconfigured URL = WPAD discovery,
		// which needs DNS/DHCP machinery we do not implement; DIRECT.
		return nil, nil
	}

	resolver, err := thePAC.resolverFor(scriptURL)
	if err != nil || resolver == nil {
		// Broken PAC config must not break connectivity.
		return nil, nil
	}

	cacheKey := req.URL.Scheme + "|" + req.URL.Hostname()
	if cached, ok := thePAC.resultCache.Load(cacheKey); ok {
		if first, ok := cached.(string); ok {
			return ProxyURLFromDirective(first), nil
		}
	}

	// ProxyResolver is documented as not concurrency-safe; PAC proxying
	// sits after env+static checks and is the rare path, so serialize it.
	thePAC.mu.Lock()
	directive, err := resolver.FindProxyForURL(req.URL, req.URL.Hostname())
	thePAC.mu.Unlock()
	if err != nil {
		return nil, nil
	}
	// PAC may return a ";"-separated fallback list ("PROXY a:80; DIRECT").
	// Take the first entry; per-fault fallbacks need connection-level
	// plumbing that http.Transport does not expose.
	first := ParsePACFirstDirective(directive)
	if thePAC.resultCount() < pacResultCap {
		thePAC.resultCache.Store(cacheKey, first)
	}
	return ProxyURLFromDirective(first), nil
}

// resolverFor returns a cached PAC resolver, re-fetching the script when
// the URL changed or the TTL expired.
func (e *pacState) resolverFor(scriptURL string) (*pac.ProxyResolver, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if e.resolver != nil && e.scriptURL == scriptURL && now.Sub(e.fetchedAt) < pacScriptTTL {
		return e.resolver, nil
	}
	script, err := fetchPACScript(scriptURL)
	if err != nil {
		return nil, fmt.Errorf("pac script fetch: %w", err)
	}
	resolver, err := pac.NewProxyResolver(&pac.ProxyResolverConfig{
		Script:    script,
		AlertSink: io.Discard,
	}, &net.Resolver{})
	if err != nil {
		return nil, fmt.Errorf("pac resolver: %w", err)
	}
	e.resolver, e.scriptURL, e.fetchedAt = resolver, scriptURL, now
	e.resultCache = sync.Map{} // new script invalidates cached directives
	return resolver, nil
}

// fetchPACScript downloads the PAC script body. file:// URLs are also
// accepted (GPOs sometimes configure local paths). Windows file URLs
// carry a leading slash before the drive letter (file:///C:/pac.js ->
// "/C:/pac.js") which os.ReadFile rejects; trim it.
func fetchPACScript(rawURL string) (string, error) {
	if u, err := url.Parse(rawURL); err == nil && u.Scheme == "file" {
		path := u.Path
		path = strings.TrimPrefix(path, "/")
		body, err := os.ReadFile(path)
		if err != nil {
			// Retry with the untrimmed path: UNC-ish or unix absolute forms.
			if body2, err2 := os.ReadFile(u.Path); err2 == nil {
				return string(body2), nil
			}
			return "", err
		}
		return string(body), nil
	}
	client := &http.Client{Timeout: pacFetchTimeout}
	resp, err := client.Get(rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pac fetch status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (e *pacState) resultCount() int {
	n := 0
	e.resultCache.Range(func(_, _ any) bool { n++; return true })
	return n
}
