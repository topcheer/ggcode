package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// maxResponseBodyBytes limits HTTP response body reads to prevent memory exhaustion.
const maxResponseBodyBytes = 10 * 1024 * 1024 // 10 MB

// WebFetch implements the web_fetch tool — fetches a URL and returns text content.
type WebFetch struct {
	// AllowPrivate disables SSRF protection. Only use for testing.
	AllowPrivate bool
}

func (t WebFetch) Name() string       { return "web_fetch" }
func (t WebFetch) Description() string { return "Fetch a URL and return its text content. Strips HTML tags and truncates to 50000 characters." }

func (t WebFetch) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {
				"type": "string",
				"description": "The URL to fetch"
			}
		},
		"required": ["url"]
	}`)
}

func (t WebFetch) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		URL string `json:"url"`
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
		transport := http.DefaultTransport.(*http.Transport).Clone()
		origDial := transport.DialContext
		transport.DialContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid address: %w", err)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(dialCtx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if isPrivateIP(ip.IP) {
					return nil, fmt.Errorf("access to private/internal IP %s is not allowed", ip.IP)
				}
			}
			return origDial(dialCtx, network, net.JoinHostPort(ips[0].IP.String(), port))
		}
		client.Transport = transport
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

	if resp.StatusCode != http.StatusOK {
		return Result{IsError: true, Content: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("read body failed: %v", err)}, nil
	}

	text := stripHTML(string(body))
	if len(text) > 50000 {
		text = text[:50000] + "\n... [truncated]"
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
	privateRanges := []struct {
		network *net.IPNet
	}{
		{mustParseCIDR("127.0.0.0/8")},
		{mustParseCIDR("::1/128")},
		{mustParseCIDR("10.0.0.0/8")},
		{mustParseCIDR("172.16.0.0/12")},
		{mustParseCIDR("192.168.0.0/16")},
		{mustParseCIDR("169.254.0.0/16")},
		{mustParseCIDR("fe80::/10")},
		{mustParseCIDR("::ffff:127.0.0.0/104")},
	}
	for _, r := range privateRanges {
		if r.network.Contains(ip) {
			return true
		}
	}
	return false
}

func mustParseCIDR(s string) *net.IPNet {
	_, network, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return network
}

// StripHTML removes HTML tags and decodes common entities.
func StripHTML(s string) string {
	return stripHTML(s)
}

// stripHTML removes HTML tags and decodes common entities.
func stripHTML(s string) string {
	s = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
