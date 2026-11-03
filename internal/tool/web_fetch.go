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
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/util"
)

const maxResponseBodyBytes = 10 * 1024 * 1024

var (
	privateNetworksOnce sync.Once
	privateNetworks     []*net.IPNet
	privateNetworksErr  error
)

type WebFetch struct {
	AllowPrivate bool
}

func (t WebFetch) Name() string { return "web_fetch" }
func (t WebFetch) Description() string {
	return "Fetch a URL and return its text content. Strips HTML tags, truncates to 20000 chars (context-efficiency cap; #874: description previously promised 50000). Does not summarize — use the optional prompt to instruct the LLM. For interactive/login pages, use browser automation."
}

func (t WebFetch) ToolMeta() ToolMeta {
	return ToolMeta{
		CostEstimate: 0.0001,
		RateLimitRPS: 5.0,
		RetryPolicy:  "exponential",
		MaxRetries:   2,
	}
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
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
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
		var base *http.Transport
		if dt, ok := http.DefaultTransport.(*http.Transport); ok {
			base = dt
		} else {
			base = &http.Transport{}
		}
		transport := util.WrapTransport(base)

		defer transport.CloseIdleConnections()

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
		} else if !t.AllowPrivate {

			if err := rejectIfHostResolvesPrivate(ctx, u.Hostname(), net.DefaultResolver.LookupIPAddr); err != nil {
				return Result{IsError: true, Content: fmt.Sprintf("fetch failed: %v", err)}, nil
			}
		}
		client.Transport = transport
	} else {
		transport := util.WrapTransport(nil)
		defer transport.CloseIdleConnections()
		client.Transport = transport
	}

	var redirectGuardTransport *http.Transport
	if ht, ok := client.Transport.(*http.Transport); ok {
		redirectGuardTransport = ht
	}

	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !t.AllowPrivate && isPrivateHost(req.URL.Hostname()) {
			return fmt.Errorf("redirect to private/internal network address is not allowed")
		}

		if !t.AllowPrivate && redirectGuardTransport != nil && isProxyConfigured(redirectGuardTransport, req.URL) {
			if err := rejectIfHostResolvesPrivate(req.Context(), req.URL.Hostname(), net.DefaultResolver.LookupIPAddr); err != nil {
				return err
			}
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

	if resp.StatusCode != http.StatusOK {
		errBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 10000))
		if readErr != nil {
			return Result{IsError: true, Content: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)}, nil
		}
		errText := stripHTML(string(errBody))

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

	const maxWebFetchChars = 20000
	if utf8.RuneCountInString(text) > maxWebFetchChars {
		runes := []rune(text)

		cutoff := maxWebFetchChars
		for i := cutoff; i > cutoff-200 && i > 0; i-- {
			if runes[i] == '.' || runes[i] == '\n' {
				cutoff = i + 1
				break
			}
		}
		shown := cutoff
		if shown > len(runes) {
			shown = len(runes)
		}
		text = string(runes[:shown]) + "\n\n... [truncated: " + fmt.Sprintf("%d/%d chars", shown, len(runes)) + " shown]"
	}

	finalURL := resp.Request.URL.String()
	if finalURL != args.URL {
		text = fmt.Sprintf("[Redirected to: %s]\n\n%s", finalURL, text)
	}

	if args.Prompt != "" {
		text = fmt.Sprintf("Prompt: %s\n\n---\n\n%s", args.Prompt, text)
	}

	return Result{Content: text}, nil
}

func IsPrivateHost(host string) bool {
	return isPrivateHost(host)
}

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

func isPrivateIP(ip net.IP) bool {
	networks, err := getPrivateNetworks()
	if err != nil {

		return true
	}
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

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

func rejectIfHostResolvesPrivate(ctx context.Context, host string, lookup func(context.Context, string) ([]net.IPAddr, error)) error {
	if host == "" || net.ParseIP(host) != nil {
		return nil
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil
	}
	for _, ip := range ips {
		if isPrivateIP(ip.IP) {
			return fmt.Errorf("access to private/internal IP %s is not allowed", ip.IP)
		}
	}
	return nil
}

func getPrivateNetworks() ([]*net.IPNet, error) {
	privateNetworksOnce.Do(func() {
		privateNetworks, privateNetworksErr = parsePrivateNetworks([]string{
			"127.0.0.0/8",
			"::1/128",

			"0.0.0.0/8",
			"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
			"169.254.0.0/16",

			"fc00::/7",
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

func StripHTML(s string) string {
	return stripHTML(s)
}

func stripHTML(s string) string {

	for _, tag := range []string{"script", "style", "noscript", "template", "svg", "nav", "header", "footer", "aside", "form", "iframe"} {
		re := regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>.*?</` + tag + `\s*>`)
		s = re.ReplaceAllString(s, "")
	}

	s = regexp.MustCompile(`(?is)<(meta|link|input|button)\b[^>]*/?>`).ReplaceAllString(s, "")

	if extracted := extractMainContent(s); extracted != "" {
		s = extracted
	}

	blockRe := regexp.MustCompile(`(?i)</?(p|div|section|article|header|footer|h[1-6]|li|tr|blockquote|pre|ul|ol|table|figure|figcaption|dt|dd)\b[^>]*>`)
	s = blockRe.ReplaceAllString(s, "\n")

	s = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(s, "\n")

	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, " ")

	s = html.UnescapeString(s)

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

func extractMainContent(htmlStr string) string {

	if content := extractFirstTag(htmlStr, "article"); content != "" {
		return content
	}

	if content := extractFirstTag(htmlStr, "main"); content != "" {
		return content
	}

	if content := extractAttrMatch(htmlStr, "role", "main"); content != "" {
		return content
	}

	for _, idPattern := range []string{"content", "main-content", "post-content", "page-content", "body-content", "wiki-content"} {
		if content := extractByID(htmlStr, idPattern); content != "" {
			return content
		}
	}
	return ""
}

func extractFirstTag(s, tag string) string {
	openRe := regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>`)
	closeRe := regexp.MustCompile(`(?is)</` + tag + `\s*>`)

	openMatch := openRe.FindStringIndex(s)
	if openMatch == nil {
		return ""
	}

	start := s[openMatch[1]:]
	rest := start
	consumed := 0
	depth := 1
	var inner string
	for {
		closeMatch := closeRe.FindStringIndex(rest)
		if closeMatch == nil {
			return ""
		}

		seg := rest[:closeMatch[0]]
		nested := len(openRe.FindAllStringIndex(seg, -1))
		depth += nested
		depth--
		if depth == 0 {
			inner = start[:consumed+closeMatch[0]]
			break
		}
		consumed += closeMatch[1]
		rest = rest[closeMatch[1]:]
	}

	if len(strings.TrimSpace(stripTagsOnly(inner))) < 100 {
		return ""
	}
	return inner
}

func extractTaggedBlock(s, attrTailRE string) string {
	re := regexp.MustCompile(`(?is)<(\w+)[^>]*` + attrTailRE + `[^>]*>`)
	loc := re.FindStringSubmatchIndex(s)
	if loc == nil || len(loc) < 4 {
		return ""
	}
	name := s[loc[2]:loc[3]]
	start := s[loc[1]:]
	rest := start
	consumed := 0
	boundary := regexp.MustCompile(`(?i)<(/?)` + name + `(\s|>|/)`)
	depth := 1
	for {
		m := boundary.FindStringSubmatchIndex(rest)
		if m == nil {
			return ""
		}
		if rest[m[2]:m[3]] == "/" {
			depth--
			if depth == 0 {

				content := start[:consumed+m[0]]
				if len(strings.TrimSpace(stripTagsOnly(content))) < 100 {
					return ""
				}
				return content
			}
		} else {
			depth++
		}
		consumed += m[1]
		rest = rest[m[1]:]
	}
}

func extractAttrMatch(s, attr, val string) string {

	return extractTaggedBlock(s, `[\s"']`+regexp.QuoteMeta(attr)+`\s*=\s*["']`+regexp.QuoteMeta(val)+`["']`)
}

func extractByID(s, id string) string {

	return extractTaggedBlock(s, `[^>]*[\s"']id\s*=\s*["']`+regexp.QuoteMeta(id)+`["']`)
}

func stripTagsOnly(s string) string {
	return regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, " ")
}
