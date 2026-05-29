package main

import (
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	relayTrustProxyEnv             = "GGCODE_RELAY_TRUST_PROXY"
	relayRequireTLSEnv             = "GGCODE_RELAY_REQUIRE_TLS"
	relayPublicStatsEnv            = "GGCODE_RELAY_PUBLIC_STATS"
	relayDisableRateLimitEnv       = "GGCODE_RELAY_DISABLE_RATE_LIMIT"
	relayShareSessionRateLimitEnv  = "GGCODE_RELAY_SHARE_SESSION_PER_MINUTE"
	relayWSIPRateLimitEnv          = "GGCODE_RELAY_WS_IP_PER_MINUTE"
	relayWSRoomRateLimitEnv        = "GGCODE_RELAY_WS_ROOM_PER_MINUTE"
	relayServerPublishRateLimitEnv = "GGCODE_RELAY_SERVER_PUBLISH_PER_10S"
	relayClientPublishRateLimitEnv = "GGCODE_RELAY_CLIENT_PUBLISH_PER_10S"
)

const (
	defaultShareSessionRateLimit  = 30
	defaultWSIPRateLimit          = 60
	defaultWSRoomRateLimit        = 30
	defaultServerPublishRateLimit = 3000
	defaultClientPublishRateLimit = 300
)

type relaySecurityConfig struct {
	TrustProxy          bool
	RequireTLS          bool
	PublicStats         bool
	DisableRateLimiting bool
	ShareSessionPerMin  int
	WSPerIPPerMin       int
	WSPerRoomPerMin     int
	ServerPublishPer10s int
	ClientPublishPer10s int
}

type relayRateLimiters struct {
	shareSessionByIP *fixedWindowLimiter
	wsByIP           *fixedWindowLimiter
	wsByRoom         *fixedWindowLimiter
	serverPublish    *fixedWindowLimiter
	clientPublish    *fixedWindowLimiter
}

type fixedWindowLimiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	entries map[string]fixedWindowEntry
	calls   int
}

type fixedWindowEntry struct {
	windowStart time.Time
	count       int
}

func loadRelaySecurityConfig() relaySecurityConfig {
	railway := runningOnRailway()
	return relaySecurityConfig{
		TrustProxy:          boolEnv(relayTrustProxyEnv, railway),
		RequireTLS:          boolEnv(relayRequireTLSEnv, railway),
		PublicStats:         boolEnv(relayPublicStatsEnv, false),
		DisableRateLimiting: boolEnv(relayDisableRateLimitEnv, false),
		ShareSessionPerMin:  intEnv(relayShareSessionRateLimitEnv, defaultShareSessionRateLimit),
		WSPerIPPerMin:       intEnv(relayWSIPRateLimitEnv, defaultWSIPRateLimit),
		WSPerRoomPerMin:     intEnv(relayWSRoomRateLimitEnv, defaultWSRoomRateLimit),
		ServerPublishPer10s: intEnv(relayServerPublishRateLimitEnv, defaultServerPublishRateLimit),
		ClientPublishPer10s: intEnv(relayClientPublishRateLimitEnv, defaultClientPublishRateLimit),
	}
}

func runningOnRailway() bool {
	for _, key := range []string{
		"RAILWAY_PROJECT_ID",
		"RAILWAY_SERVICE_ID",
		"RAILWAY_ENVIRONMENT_ID",
		"RAILWAY_PUBLIC_DOMAIN",
	} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

func boolEnv(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func intEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func newRelayRateLimiters(cfg relaySecurityConfig) *relayRateLimiters {
	if cfg.DisableRateLimiting {
		return &relayRateLimiters{}
	}
	return &relayRateLimiters{
		shareSessionByIP: newFixedWindowLimiter(cfg.ShareSessionPerMin, time.Minute),
		wsByIP:           newFixedWindowLimiter(cfg.WSPerIPPerMin, time.Minute),
		wsByRoom:         newFixedWindowLimiter(cfg.WSPerRoomPerMin, time.Minute),
		serverPublish:    newFixedWindowLimiter(cfg.ServerPublishPer10s, 10*time.Second),
		clientPublish:    newFixedWindowLimiter(cfg.ClientPublishPer10s, 10*time.Second),
	}
}

func newFixedWindowLimiter(limit int, window time.Duration) *fixedWindowLimiter {
	if limit <= 0 || window <= 0 {
		return nil
	}
	return &fixedWindowLimiter{
		limit:   limit,
		window:  window,
		entries: make(map[string]fixedWindowEntry),
	}
}

func (l *fixedWindowLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	if key == "" {
		key = "unknown"
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.entries[key]
	if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= l.window {
		entry = fixedWindowEntry{windowStart: now}
	}
	entry.count++
	l.entries[key] = entry

	l.calls++
	if l.calls%256 == 0 {
		cutoff := now.Add(-2 * l.window)
		for existingKey, existing := range l.entries {
			if existing.windowStart.Before(cutoff) {
				delete(l.entries, existingKey)
			}
		}
	}

	if entry.count <= l.limit {
		return true, 0
	}
	retryAfter := l.window - now.Sub(entry.windowStart)
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return false, retryAfter
}

func (cfg relaySecurityConfig) clientIP(r *http.Request) string {
	if cfg.TrustProxy {
		if ip := cleanForwardedIP(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
		if forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ","); len(forwarded) > 0 {
			if ip := cleanForwardedIP(forwarded[0]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func cleanForwardedIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(raw); err == nil && host != "" {
		return host
	}
	return raw
}

func (cfg relaySecurityConfig) requestUsesTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if cfg.TrustProxy {
		return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
	}
	return false
}

func (cfg relaySecurityConfig) describeTransportMode() string {
	mode := "direct"
	if cfg.TrustProxy {
		mode = "proxy"
	}
	return mode
}

func (cfg relaySecurityConfig) logStartup(adminToken string) {
	statsMode := "public"
	switch {
	case cfg.PublicStats:
		statsMode = "public"
	case adminToken != "":
		statsMode = "admin"
	default:
		statsMode = "disabled"
	}
	rateLimits := "enabled"
	if cfg.DisableRateLimiting {
		rateLimits = "disabled"
	}
	log.Printf("[relay] security: transport=%s require_tls=%t stats=%s rate_limits=%s",
		cfg.describeTransportMode(), cfg.RequireTLS, statsMode, rateLimits)
}

func (h *hub) requireSecureTransport(w http.ResponseWriter, r *http.Request) bool {
	if !h.security.RequireTLS || h.security.requestUsesTLS(r) {
		return true
	}
	http.Error(w, "TLS required", http.StatusUpgradeRequired)
	return false
}

func (h *hub) enforceRateLimit(w http.ResponseWriter, limiter *fixedWindowLimiter, key, scope string) bool {
	allowed, retryAfter := limiter.allow(key, time.Now())
	if allowed {
		return true
	}
	writeRateLimited(w, retryAfter)
	if scope != "" {
		log.Printf("[relay] rate limited: scope=%s key=%s retry=%s", scope, key, retryAfter)
	}
	return false
}

func (h *hub) enforceIPRateLimit(w http.ResponseWriter, r *http.Request, limiter *fixedWindowLimiter, scope string) bool {
	return h.enforceRateLimit(w, limiter, h.security.clientIP(r), scope)
}

func (h *hub) allowPublishedMessage(p *peer, msgType string) bool {
	var limiter *fixedWindowLimiter
	var scope string
	switch {
	case p.role == "server" && (msgType == "encrypted" || msgType == "active_session" || msgType == "language_change" || msgType == "key_accept"):
		limiter = h.limiters.serverPublish
		scope = "publish_server"
	case p.role == "client" && (msgType == "encrypted" || msgType == "language_change" || msgType == "key_offer" || msgType == "key_ready"):
		limiter = h.limiters.clientPublish
		scope = "publish_client"
	default:
		return true
	}
	key := p.role + ":" + p.room.token
	allowed, retryAfter := limiter.allow(key, time.Now())
	if allowed {
		return true
	}
	log.Printf("[relay] rate limited: scope=%s room=%s retry=%s", scope, shortToken(p.room.token), retryAfter)
	return false
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	http.Error(w, "rate limited", http.StatusTooManyRequests)
}
