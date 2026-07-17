package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

// maxResponseBodyBytes limits HTTP response body reads to prevent memory exhaustion.
const maxResponseBodyBytes = 10 * 1024 * 1024 // 10 MB

var (
	privateNetworksOnce sync.Once
	privateNetworks     []*net.IPNet
	privateNetworksErr  error
)

// WebFetch implements the web_fetch tool — fetches a URL and returns text content.
type WebFetch struct {
	// AllowPrivate disables SSRF protection. Only use for testing.
	AllowPrivate bool
}

func (t WebFetch) Name() string { return "web_fetch" }
func (t WebFetch) Description() string {
	return "Fetch a URL and return its text content. Strips HTML tags, truncates to 50000 chars. Does not summarize — use the optional prompt to instruct the LLM. For interactive/login pages, use browser automation."
}

func (t WebFetch) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"url": {
			"type": "string",
			"description": "The URL to fetch"
		},
		"prompt": {
			"type": "string",
			"description": "Optional instruction prepended to the fetched text for the LLM. The tool itself does not execute this prompt or summarize the page."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"url",
		"description"
	]
}`)
}

func (t WebFetch) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		URL    string `json:"url"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if args.URL == "" {
		return Result{IsError: true, Content: "url is required"}, nil
	}

	u, err := url.Parse(args.URL)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid URL: %v", err)}, nil
	}

	if !t.AllowPrivate && isPrivateHost(u.Hostname()) {
		return Result{IsError: true, Content: "Error: access to private/internal network addresses is not allowed"}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid request: %v", err)}, nil
	}
	req.Header.Set("User-Agent", "ggcode/1.0 (web fetch tool)")

	client := &http.Client{Timeout: 30 * time.Second}

	if !t.AllowPrivate {
		var transport *http.Transport
		if dt, ok := http.DefaultTransport.(*http.Transport); ok {
			transport = dt.Clone()
		} else {
			transport = &http.Transport{}
		}

		// When a proxy is configured (HTTP_PROXY/HTTPS_PROXY), the custom
		// DialContext receives the proxy address (often localhost/internal)
		// rather than the target host. Skip the DialContext override in that
		// case so the proxy connection succeeds. SSRF protection is still
		// enforced at URL level (line ~81) and redirect level (below).
		proxyInUse := isProxyConfigured(transport, u)

		if !proxyInUse {
			origDial := transport.DialContext
			transport.DialContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, fmt.Errorf("invalid address: %w", err)
				}
				dialAddr, err := resolvePublicDialAddress(dialCtx, host, port, net.DefaultResolver.LookupIPAddr)
				if err != nil {
					return nil, err
				}
				return origDial(dialCtx, network, dialAddr)
			}
		}
		client.Transport = util.WrapTransport(transport)
	} else {
		client.Transport = util.WrapTransport(nil)
	}

	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !t.AllowPrivate && isPrivateHost(req.URL.Hostname()) {
			return fmt.Errorf("redirect to private/internal network address is not allowed")
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}

	resp, err := client.Do(req)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("fetch failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	// For non-200 responses, still read a truncated body — many APIs and
	// websites return useful error details (JSON messages, HTML hints) that
	// help the agent diagnose the problem and choose a fallback strategy.
	if resp.StatusCode != http.StatusOK {
		errBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 10000))
		if readErr != nil {
			return Result{IsError: true, Content: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)}, nil
		}
		errText := stripHTML(string(errBody))
		// Truncate non-200 response bodies to keep context usage minimal
		if len([]rune(errText)) > 2000 {
			errText = string([]rune(errText)[:2000]) + "\n... [error body truncated]"
		}
		finalURL := resp.Request.URL.String()
		msg := fmt.Sprintf("HTTP %d: %s\nFinal URL: %s", resp.StatusCode, resp.Status, finalURL)
		if strings.TrimSpace(errText) != "" {
			msg += "\n" + errText
		}
		return Result{IsError: true, Content: msg}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("read body failed: %v", err)}, nil
	}

	text := stripHTML(string(body))
	if len([]rune(text)) > 50000 {
		text = string([]rune(text)[:50000]) + "\n... [truncated]"
	}

	// Include final URL if redirected (helpful for shortened URLs, etc.)
	finalURL := resp.Request.URL.String()
	if finalURL != args.URL {
		text = fmt.Sprintf("[Redirected to: %s]\n\n%s", finalURL, text)
	}

	// When a prompt is provided, prepend it so the LLM can process accordingly
	if args.Prompt != "" {
		text = fmt.Sprintf("Prompt: %s\n\n---\n\n%s", args.Prompt, text)
	}

	return Result{Content: text}, nil
}

// isPrivateHost checks if a hostname resolves to a private IP or is a loopback hostname.
func isPrivateHost(host string) bool {
	lower := strings.ToLower(host)
	if lower == "localhost" || lower == "localhost.localdomain" ||
		lower == "ip6-localhost" || lower == "ip6-loopback" {
		return true
	}
	if strings.HasSuffix(lower, ".internal") || lower == "metadata.google.internal" {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return isPrivateIP(ip)
	}
	return false
}

// isPrivateIP returns true if the IP is in a private, loopback, or link-local range.
func isPrivateIP(ip net.IP) bool {
	networks, err := getPrivateNetworks()
	if err != nil {
		// Fail closed if the internal network list cannot be initialized.
		return true
	}
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// isProxyConfigured checks whether the transport's Proxy function (typically
// http.ProxyFromEnvironment) would route the given URL through a proxy.
func isProxyConfigured(transport *http.Transport, u *url.URL) bool {
	if transport.Proxy == nil {
		return false
	}
	proxyReq := &http.Request{URL: u, Header: make(http.Header)}
	pURL, err := transport.Proxy(proxyReq)
	return pURL != nil && err == nil
}

func resolvePublicDialAddress(ctx context.Context, host, port string, lookup func(context.Context, string) ([]net.IPAddr, error)) (string, error) {
	ips, err := lookup(ctx, host)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("no IP addresses found for host %q", host)
	}
	for _, ip := range ips {
		if isPrivateIP(ip.IP) {
			return "", fmt.Errorf("access to private/internal IP %s is not allowed", ip.IP)
		}
	}
	return net.JoinHostPort(ips[0].IP.String(), port), nil
}

func getPrivateNetworks() ([]*net.IPNet, error) {
	privateNetworksOnce.Do(func() {
		privateNetworks, privateNetworksErr = parsePrivateNetworks([]string{
			"127.0.0.0/8",
			"::1/128",
			"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
			"169.254.0.0/16",
			"fe80::/10",
			"::ffff:127.0.0.0/104",
		})
	})
	return privateNetworks, privateNetworksErr
}

func parsePrivateNetworks(cidrs []string) ([]*net.IPNet, error) {
	networks := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("parse private CIDR %q: %w", cidr, err)
		}
		networks = append(networks, network)
	}
	return networks, nil
}

// StripHTML removes HTML tags and decodes common entities.
func StripHTML(s string) string {
	return stripHTML(s)
}

// stripHTML removes HTML tags, extracts main content, and decodes entities.
// It prioritizes semantic content containers (<article>, <main>, role="main")
// and removes boilerplate elements (<nav>, <header>, <footer>, <aside>) to
// produce cleaner text that uses fewer tokens and is easier for the LLM to
// parse. Falls back to full-page extraction when no semantic container is found.
func stripHTML(s string) string {
	// Phase 1: Remove non-content elements entirely
	// Go's RE2 does not support backreferences (\1), so we enumerate each tag.
	for _, tag := range []string{"script", "style", "noscript", "template", "svg", "nav", "header", "footer", "aside", "form", "iframe"} {
		re := regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>.*?</` + tag + `\s*>`)
		s = re.ReplaceAllString(s, "")
	}
	// Also remove self-closing non-content tags
	s = regexp.MustCompile(`(?is)<(meta|link|input|button)\b[^>]*/?>`).ReplaceAllString(s, "")

	// Phase 2: Try to extract main content from semantic containers
	// Priority: <article> > <main> > [role="main"] > #content > .content > .post > .entry-content
	if extracted := extractMainContent(s); extracted != "" {
		s = extracted
	}

	// Phase 3: Convert block-level elements to newlines for readability
	// This preserves paragraph/heading structure that the LLM uses for comprehension
	blockRe := regexp.MustCompile(`(?i)</?(p|div|section|article|header|footer|h[1-6]|li|tr|blockquote|pre|ul|ol|table|figure|figcaption|dt|dd)\b[^>]*>`)
	s = blockRe.ReplaceAllString(s, "\n")
	// Convert <br> to newline
	s = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(s, "\n")

	// Phase 4: Strip remaining tags
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, " ")

	// Phase 5: Decode HTML entities
	s = html.UnescapeString(s)

	// Phase 6: Collapse whitespace but preserve line structure
	// Collapse spaces within lines, collapse 3+ newlines to 2
	lines := strings.Split(s, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

// extractMainContent tries to find the primary content container in an HTML page.
// Returns the inner HTML of the container, or empty string if none found.
func extractMainContent(htmlStr string) string {
	// Try <article> first — most reliable for blog posts and documentation
	if content := extractFirstTag(htmlStr, "article"); content != "" {
		return content
	}
	// Try <main>
	if content := extractFirstTag(htmlStr, "main"); content != "" {
		return content
	}
	// Try [role="main"]
	if content := extractAttrMatch(htmlStr, "role", "main"); content != "" {
		return content
	}
	// Try common content div patterns
	for _, idPattern := range []string{"content", "main-content", "post-content", "page-content", "body-content", "wiki-content"} {
		if content := extractByID(htmlStr, idPattern); content != "" {
			return content
		}
	}
	return ""
}

// extractFirstTag extracts the inner content of the first occurrence of a tag.
func extractFirstTag(s, tag string) string {
	openRe := regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>`)
	closeRe := regexp.MustCompile(`(?is)</` + tag + `\s*>`)

	openMatch := openRe.FindStringIndex(s)
	if openMatch == nil {
		return ""
	}
	rest := s[openMatch[1]:]
	closeMatch := closeRe.FindStringIndex(rest)
	if closeMatch == nil {
		return ""
	}
	inner := rest[:closeMatch[0]]
	// Only use if it contains meaningful text (>100 chars)
	if len(strings.TrimSpace(stripTagsOnly(inner))) < 100 {
		return ""
	}
	return inner
}

// extractAttrMatch extracts content from a tag with a specific attribute value.
func extractAttrMatch(s, attr, val string) string {
	re := regexp.MustCompile(`(?is)<\w+\s+[^>]*` + attr + `\s*=\s*["']` + val + `["'][^>]*>(.*?)</\w+>`)
	m := re.FindStringSubmatch(s)
	if m == nil || len(m) < 2 {
		return ""
	}
	if len(strings.TrimSpace(stripTagsOnly(m[1]))) < 100 {
		return ""
	}
	return m[1]
}

// extractByID extracts content from a tag with a specific id attribute.
func extractByID(s, id string) string {
	re := regexp.MustCompile(`(?is)<\w+\s+[^>]*id\s*=\s*["']` + regexp.QuoteMeta(id) + `["'][^>]*>(.*?)</\w+>`)
	m := re.FindStringSubmatch(s)
	if m == nil || len(m) < 2 {
		return ""
	}
	if len(strings.TrimSpace(stripTagsOnly(m[1]))) < 100 {
		return ""
	}
	return m[1]
}

// stripTagsOnly removes HTML tags without any entity decoding or whitespace handling.
func stripTagsOnly(s string) string {
	return regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, " ")
}
