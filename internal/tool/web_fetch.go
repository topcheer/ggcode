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
	"sync"
	"time"
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
	return "Fetch a URL and return its text content. Strips HTML tags and truncates to 50000 characters."
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
				"description": "A prompt to apply to the fetched content for extraction or analysis"
			}
		},
		"required": ["url"]
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
