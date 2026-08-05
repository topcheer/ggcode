package agent

// HTTP Plaintext Detection in Source Code
//
// Research basis: Insecure transport is OWASP Top 10 A02:2021 (Cryptographic
// Failures). AI coding agents frequently generate http:// URLs when they
// should use https://, especially when:
//   1. Writing API client code (http://api.example.com/... instead of https://)
//   2. Setting up webhook URLs
//   3. Configuring redirect URLs
//   4. Writing test fixtures with hardcoded endpoints
//
// This is DIFFERENT from insecure_pattern_check.go which detects TLS bypass
// (InsecureSkipVerify: true). THIS check catches the simpler but equally
// dangerous case of using plaintext HTTP for non-localhost traffic.
//
// Competitor analysis:
//   - GitHub Copilot: no write-time HTTP plaintext detection
//   - Cursor: no detection (relies on external linters)
//   - Claude Code: no detection
//   - gosec (G107): only flags SSRF via http.Get(userInput)
//
// ggcode's approach: delta-aware text pattern matching. Flags http:// URLs
// pointing to non-localhost hosts (localhost, 127.0.0.1, 0.0.0.0, [::1] are
// exempted). Zero LLM cost, <1ms.

import (
	"fmt"
	"regexp"
	"strings"
)

// httpPlaintextRe matches http:// URLs in source code.
// Captures the host portion after "http://".
var httpPlaintextRe = regexp.MustCompile(`http://([a-zA-Z0-9._:-]+)`)

// localhostHosts lists host patterns that are exempt from plaintext HTTP
// warnings because they represent local development environments.
var localhostHosts = []string{
	"localhost", "127.0.0.1", "0.0.0.0", "[::1]", "::1",
}

// maxPlaintextWarnings limits warnings per file to avoid noise.
const maxPlaintextWarnings = 3

// checkHTTPPlaintext detects http:// URLs introduced by this edit that point
// to non-localhost hosts. Delta-aware: only flags URLs not present in oldContent.
func checkHTTPPlaintext(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Find all http:// URLs in old and new content.
	oldURLs := extractHTTPHosts(oldContent)
	newURLs := extractHTTPHosts(newContent)

	// Find newly-introduced non-localhost URLs.
	var warnings []string
	seen := make(map[string]bool)
	for u := range newURLs {
		if oldURLs[u] {
			continue // existed before
		}
		if seen[u] {
			continue // already reported
		}
		if isLocalhost(u) {
			continue // exempt
		}
		seen[u] = true
		warnings = append(warnings, fmt.Sprintf(
			"[Security] Plaintext HTTP URL http://%s introduced in this file. "+
				"HTTP traffic is unencrypted and vulnerable to interception (OWASP A02:2021). "+
				"Use https:// instead. If this is a legitimate non-TLS endpoint, "+
				"add a comment explaining why.",
			u))
		if len(warnings) >= maxPlaintextWarnings {
			break
		}
	}

	return warnings
}

// extractHTTPHosts finds all http:// host portions in the content and returns
// them as a set (map) for quick lookup.
func extractHTTPHosts(content string) map[string]bool {
	matches := httpPlaintextRe.FindAllStringSubmatch(content, -1)
	result := make(map[string]bool, len(matches))
	for _, m := range matches {
		if len(m) >= 2 {
			// Take only the host part (before port or path)
			host := m[1]
			if idx := strings.IndexByte(host, ':'); idx > 0 {
				host = host[:idx] // strip port
			}
			result[host] = true
		}
	}
	return result
}

// isLocalhost checks if a host represents a local development address.
func isLocalhost(host string) bool {
	lower := strings.ToLower(host)
	for _, lh := range localhostHosts {
		if lower == strings.ToLower(lh) {
			return true
		}
	}
	return false
}
