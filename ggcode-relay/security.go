package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const (
	relayTrustProxyEnv  = "GGCODE_RELAY_TRUST_PROXY"
	relayRequireTLSEnv  = "GGCODE_RELAY_REQUIRE_TLS"
	relayPublicStatsEnv = "GGCODE_RELAY_PUBLIC_STATS"
)

type relaySecurityConfig struct {
	TrustProxy  bool
	RequireTLS  bool
	PublicStats bool
}

func loadRelaySecurityConfig() relaySecurityConfig {
	railway := runningOnRailway()
	return relaySecurityConfig{
		TrustProxy:  boolEnv(relayTrustProxyEnv, railway),
		RequireTLS:  boolEnv(relayRequireTLSEnv, railway),
		PublicStats: boolEnv(relayPublicStatsEnv, false),
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
	log.Printf("[relay] security: transport=%s require_tls=%t stats=%s",
		cfg.describeTransportMode(), cfg.RequireTLS, statsMode)
}

func (h *hub) requireSecureTransport(w http.ResponseWriter, r *http.Request) bool {
	if !h.security.RequireTLS || h.security.requestUsesTLS(r) {
		return true
	}
	http.Error(w, "TLS required", http.StatusUpgradeRequired)
	return false
}
