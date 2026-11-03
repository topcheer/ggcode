package agent

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

var httpPlaintextRe = regexp.MustCompile(`http://(\[[0-9a-fA-F:.]+\]|[a-zA-Z0-9._:-]+)`)

var localhostHosts = []string{
	"localhost", "127.0.0.1", "0.0.0.0", "[::1]", "::1",
}

const maxPlaintextWarnings = 3

func checkHTTPPlaintext(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	oldURLs := extractHTTPHosts(oldContent)
	newURLs := extractHTTPHosts(newContent)

	var warnings []string
	seen := make(map[string]bool)
	for u := range newURLs {
		if oldURLs[u] {
			continue
		}
		if seen[u] {
			continue
		}
		if isLocalhost(u) {
			continue
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

func extractHTTPHosts(content string) map[string]bool {
	matches := httpPlaintextRe.FindAllStringSubmatch(content, -1)
	result := make(map[string]bool, len(matches))
	for _, m := range matches {
		if len(m) >= 2 {

			host := m[1]
			if strings.HasPrefix(host, "[") {

				host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
			} else if idx := strings.IndexByte(host, ':'); idx > 0 {
				host = host[:idx]
			}
			result[host] = true
		}
	}
	return result
}

func isLocalhost(host string) bool {
	lower := strings.ToLower(host)

	for _, lh := range localhostHosts {
		if lower == strings.ToLower(lh) {
			return true
		}
	}

	if strings.HasSuffix(lower, ".localhost") {
		return true
	}

	if ip := net.ParseIP(lower); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}
