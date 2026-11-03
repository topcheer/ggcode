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

const (
	pacScriptTTL = 5 * time.Minute

	pacFetchErrCooldown = 30 * time.Second
	pacResultCap        = 512
	pacFetchTimeout     = 10 * time.Second
)

var (
	sysProxyOnce sync.Once
	sysProxyFunc func(*http.Request) (*url.URL, error)
)

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

	lastFetchErrAt time.Time

	resultCache sync.Map
}

func staticSystemProxy(req *http.Request) (*url.URL, error) {
	conf := ieproxy.GetConf()
	if !conf.Static.Active || req.URL == nil {
		return nil, nil
	}
	cfg := httpproxy.Config{
		HTTPSProxy: protocolFallback(conf.Static.Protocols, "https"),
		HTTPProxy:  protocolFallback(conf.Static.Protocols, "http"),
		NoProxy:    conf.Static.NoProxy,
	}
	return cfg.ProxyFunc()(req.URL)
}

func protocolFallback(m map[string]string, proto string) string {
	if v, ok := m[proto]; ok {
		return v
	}
	return m[""]
}

var thePAC pacState

func buildWindowsProxyFunc() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {

		if u, err := http.ProxyFromEnvironment(req); err != nil || u != nil {
			return u, err
		}

		if u, err := staticSystemProxy(req); err == nil && u != nil {
			return u, nil
		}

		return pacProxy(req)
	}
}

func pacProxy(req *http.Request) (*url.URL, error) {
	conf := ieproxy.GetConf()
	if !conf.Automatic.Active || req.URL == nil || req.URL.Host == "" {
		return nil, nil
	}
	scriptURL := strings.TrimSpace(conf.Automatic.PreConfiguredURL)
	if scriptURL == "" {

		return nil, nil
	}

	resolver, err := thePAC.resolverFor(scriptURL)
	if err != nil || resolver == nil {

		return nil, nil
	}

	cacheKey := req.URL.Scheme + "|" + req.URL.Hostname()
	if cached, ok := thePAC.resultCache.Load(cacheKey); ok {
		if first, ok := cached.(string); ok {
			return ProxyURLFromDirective(first), nil
		}
	}

	thePAC.mu.Lock()
	directive, err := resolver.FindProxyForURL(req.URL, req.URL.Hostname())
	thePAC.mu.Unlock()
	if err != nil {
		return nil, nil
	}

	first := ParsePACFirstDirective(directive)
	if thePAC.resultCount() < pacResultCap {
		thePAC.resultCache.Store(cacheKey, first)
	}
	return ProxyURLFromDirective(first), nil
}

func (e *pacState) resolverFor(scriptURL string) (*pac.ProxyResolver, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if e.resolver != nil && e.scriptURL == scriptURL && now.Sub(e.fetchedAt) < pacScriptTTL {
		return e.resolver, nil
	}

	if now.Sub(e.lastFetchErrAt) < pacFetchErrCooldown {
		return nil, fmt.Errorf("pac script fetch on cooldown since %v", e.lastFetchErrAt)
	}
	script, err := fetchPACScript(scriptURL)
	if err != nil {
		e.lastFetchErrAt = now
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
	e.resultCache = sync.Map{}
	return resolver, nil
}

func fetchPACScript(rawURL string) (string, error) {
	if u, err := url.Parse(rawURL); err == nil && u.Scheme == "file" {
		path := u.Path
		path = strings.TrimPrefix(path, "/")
		body, err := os.ReadFile(path)
		if err != nil {

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
