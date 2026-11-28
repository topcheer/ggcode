package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/rexcache"
	"github.com/topcheer/ggcode/internal/util"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
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
	return "Fetch a URL and return its text content. HTML pages are reduced to readable text (truncated to 20000 chars, context-efficiency cap); JSON / plain-text / XML bodies are returned verbatim. Detects the charset (UTF-8, GBK, ...), anti-bot challenges and JavaScript-only pages and says so instead of returning garbage. Does not summarize — use the optional prompt to instruct the LLM. For interactive/login pages, use browser automation."
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

// plainFetchUserAgent is the honest tool identity used when a site refuses
// the browser identity (see fetchUAMode).
const plainFetchUserAgent = "ggcode/1.0 (web fetch tool; +https://github.com/topcheer/ggcode)"

type fetchUAMode int

const (
	uaBrowser fetchUAMode = iota
	uaPlain
)

var (
	fetchUAPrefMu     sync.Mutex
	fetchUAPref       = map[string]fetchUAMode{}
	fetchUAPrefTTL    = 10 * time.Minute
	fetchUAPrefSeenAt = map[string]time.Time{}
)

func preferredFetchUAMode(host string) fetchUAMode {
	fetchUAPrefMu.Lock()
	defer fetchUAPrefMu.Unlock()
	if at, ok := fetchUAPrefSeenAt[host]; ok && time.Since(at) < fetchUAPrefTTL {
		return fetchUAPref[host]
	}
	delete(fetchUAPref, host)
	delete(fetchUAPrefSeenAt, host)
	return uaBrowser
}

func rememberFetchUAMode(host string, mode fetchUAMode) {
	fetchUAPrefMu.Lock()
	defer fetchUAPrefMu.Unlock()
	if len(fetchUAPref) > 256 {
		fetchUAPref = map[string]fetchUAMode{}
		fetchUAPrefSeenAt = map[string]time.Time{}
	}
	fetchUAPref[host] = mode
	fetchUAPrefSeenAt[host] = time.Now()
}

func resetFetchUAPreference() {
	fetchUAPrefMu.Lock()
	defer fetchUAPrefMu.Unlock()
	fetchUAPref = map[string]fetchUAMode{}
	fetchUAPrefSeenAt = map[string]time.Time{}
}

const (
	webFetchAccept        = "text/html,application/xhtml+xml,application/xml;q=0.9,application/json;q=0.8,text/plain;q=0.8,*/*;q=0.7"
	webFetchAcceptLang    = "en-US,en;q=0.9,zh-CN;q=0.8"
	maxWebFetchChars      = 20000
	webFetchNoDeadlineTTL = 45 * time.Second
	// A 200 HTML page whose extracted text is shorter than this is treated as
	// JavaScript-rendered when the markup carries SPA markers.
	jsRenderedMinText = 300
)

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
	if u.Scheme != "http" && u.Scheme != "https" {
		return Result{IsError: true, Content: fmt.Sprintf("unsupported URL scheme %q (http/https only)", u.Scheme)}, nil
	}

	if !t.AllowPrivate && isPrivateHost(u.Hostname()) {
		return Result{IsError: true, Content: "Error: access to private/internal network addresses is not allowed"}, nil
	}

	// Two identities, tried in that order: a desktop browser (most origins
	// 403 or challenge non-browser agents) and, when the browser identity is
	// itself refused, the plain tool identity (some WAFs - e.g. w3.org - 403
	// a "Chrome" UA that does not arrive with a browser TLS fingerprint but
	// let an honest tool UA through). Whichever worked is remembered per host.
	newRequest := func(mode fetchUAMode) (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
		if err != nil {
			return nil, err
		}
		if mode == uaBrowser {
			r.Header.Set("User-Agent", browserUserAgent)
			r.Header.Set("Accept", webFetchAccept)
			r.Header.Set("Accept-Language", webFetchAcceptLang)
		} else {
			r.Header.Set("User-Agent", plainFetchUserAgent)
		}
		return r, nil
	}
	mode := preferredFetchUAMode(u.Host)
	req, err := newRequest(mode)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid request: %v", err)}, nil
	}

	client := &http.Client{}
	// The agent wraps tool calls in an adaptive deadline (web: 120s default,
	// growing with observed latency). Honour it instead of a shorter fixed
	// client timeout, so slow-but-alive sites are not cut off at 30s; only
	// callers without a deadline get a fixed bound.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		client.Timeout = webFetchNoDeadlineTTL
	}

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

	start := time.Now()
	resp, err := doWithFetchRetry(ctx, client, req)
	if err == nil && mode == uaBrowser && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotAcceptable) {
		debug.Log("webfetch", "GET %s: browser identity refused with %d - retrying with the plain tool identity", u.Host, resp.StatusCode)
		_ = resp.Body.Close()
		mode = uaPlain
		req, err = newRequest(mode)
		if err == nil {
			resp, err = doWithFetchRetry(ctx, client, req)
		}
	}
	if err != nil {
		debug.Log("webfetch", "GET %s failed after %s: %v", u.Host, time.Since(start).Round(time.Millisecond), err)
		return Result{IsError: true, Content: fmt.Sprintf("fetch failed: %v", err)}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotAcceptable {
		rememberFetchUAMode(u.Host, mode)
	}

	contentType := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("read body failed: %v", err)}, nil
	}
	debug.Log("webfetch", "GET %s -> %d (%s, %d bytes, %s)", u.Host, resp.StatusCode, contentType, len(body), time.Since(start).Round(time.Millisecond))

	finalURL := resp.Request.URL.String()

	if resp.StatusCode != http.StatusOK {
		errText := stripHTML(decodeTextBody(body, charsetParam(contentType), contentType))
		if len([]rune(errText)) > 2000 {
			errText = string([]rune(errText)[:2000]) + "\n... [error body truncated]"
		}
		msg := fmt.Sprintf("HTTP %d: %s\nFinal URL: %s", resp.StatusCode, resp.Status, finalURL)
		if note := antiBotNote(resp.StatusCode, body); note != "" {
			msg += "\n" + note
		}
		if strings.TrimSpace(errText) != "" {
			msg += "\n" + errText
		}
		return Result{IsError: true, Content: msg}, nil
	}

	mediaType, params, _ := mime.ParseMediaType(contentType)
	if len(params) == 0 {
		// ParseMediaType fails on malformed headers; fall back to the bare
		// type so charset handling still works.
		if i := strings.IndexByte(contentType, ';'); i >= 0 {
			mediaType = strings.TrimSpace(strings.ToLower(contentType[:i]))
		} else {
			mediaType = strings.ToLower(strings.TrimSpace(contentType))
		}
	}
	charsetLabel := params["charset"]

	switch classifyBody(mediaType, body) {
	case bodyBinary:
		return Result{IsError: true, Content: fmt.Sprintf("unsupported content type %q (%d bytes). Download it with run_command (curl -L -o <file> %s) and inspect it locally; read_file can read PDF and Office documents.", mediaType, len(body), args.URL)}, nil
	case bodyText:
		// JSON / text / XML: never run the HTML stripper - it would eat
		// "<" in code, generics and markup and turn structured data into mush.
		text := truncateFetchText(decodeTextBody(body, charsetLabel, contentType), maxWebFetchChars)
		return finishFetch(text, finalURL, args), nil
	}

	rawHTML := decodeTextBody(body, charsetLabel, contentType)
	text := stripHTML(rawHTML)

	if n := utf8.RuneCountInString(text); n < jsRenderedMinText && looksJSRendered(rawHTML) {
		text += fmt.Sprintf("\n\n[web_fetch] Only %d characters of text could be extracted - this page is rendered client-side (JavaScript). Use the browser tool (navigate, then content/extract) to read it.", n)
	}

	text = truncateFetchText(text, maxWebFetchChars)
	return finishFetch(text, finalURL, args), nil
}

func finishFetch(text, finalURL string, args struct {
	URL    string `json:"url"`
	Prompt string `json:"prompt"`
}) Result {
	if finalURL != args.URL {
		text = fmt.Sprintf("[Redirected to: %s]\n\n%s", finalURL, text)
	}
	if args.Prompt != "" {
		text = fmt.Sprintf("Prompt: %s\n\n---\n\n%s", args.Prompt, text)
	}
	return Result{Content: text}
}

// doWithFetchRetry sends an idempotent GET up to twice: the first attempt is
// retried only for transient transport faults (reset/EOF/TLS hiccup) and
// never once the caller's context is done.
func doWithFetchRetry(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error
	for attempt := 1; attempt <= 2; attempt++ {
		resp, err = client.Do(req)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil || attempt == 2 || !isTransientFetchError(err) {
			return nil, err
		}
		debug.Log("webfetch", "GET %s attempt %d failed: %v - retrying once", req.URL.Host, attempt, err)
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(400 * time.Millisecond):
		}
	}
	return nil, err
}

var transientFetchRe = rexcache.Must(`(?i)\bEOF\b|connection reset|broken pipe|TLS handshake timeout|server closed idle connection|connection refused|no route to host|network is unreachable`)

func isTransientFetchError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// A full client timeout will not improve on an immediate retry.
	if strings.Contains(msg, "Client.Timeout exceeded") {
		return false
	}
	return transientFetchRe.MatchString(msg)
}

// charsetParam extracts the charset parameter without failing on a
// malformed Content-Type header.
func charsetParam(contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return params["charset"]
}

type bodyKind int

const (
	bodyHTML bodyKind = iota
	bodyText
	bodyBinary
)

func classifyBody(mediaType string, body []byte) bodyKind {
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.ToLower(strings.TrimSpace(mediaType[:i]))
	}
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		return bodyHTML
	case isTextualMediaType(mediaType):
		return bodyText
	case mediaType == "" || mediaType == "application/octet-stream" || strings.HasPrefix(mediaType, "text/"):
		// Unknown or mislabelled: let Go sniff the first bytes.
		n := len(body)
		if n > 512 {
			n = 512
		}
		sniffed := http.DetectContentType(body[:n])
		switch {
		case strings.HasPrefix(sniffed, "text/html"):
			return bodyHTML
		case strings.HasPrefix(sniffed, "text/plain"):
			return bodyText
		default:
			return bodyBinary
		}
	default:
		return bodyBinary
	}
}

var textualMediaTypes = map[string]bool{
	"application/json":                  true,
	"application/xml":                   true,
	"application/javascript":            true,
	"application/ecmascript":            true,
	"application/typescript":            true,
	"application/yaml":                  true,
	"application/x-yaml":                true,
	"application/toml":                  true,
	"application/graphql":               true,
	"application/x-sh":                  true,
	"application/x-httpd-php":           true,
	"application/x-query":               true,
	"application/sql":                   true,
	"application/rtf":                   true,
	"application/x-www-form-urlencoded": true,
}

func isTextualMediaType(mediaType string) bool {
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	if textualMediaTypes[mediaType] {
		return true
	}
	return strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")
}

// decodeTextBody converts the response bytes to UTF-8. A declared charset
// wins; otherwise valid UTF-8 passes through; otherwise x/net sniffs the
// BOM / <meta charset>. x/net defaults to windows-1252 when it cannot tell,
// which turns an undeclared GBK page into mojibake - so for uncertain cases a
// GBK decode is tried first and kept only if it yields mostly CJK text.
func decodeTextBody(body []byte, charsetLabel, contentType string) string {
	text := decodeTextBodyRaw(body, charsetLabel, contentType)
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "\uFFFD")
	}
	return text
}

func decodeTextBodyRaw(body []byte, charsetLabel, contentType string) string {
	if len(body) == 0 {
		return ""
	}
	if charsetLabel != "" && !strings.EqualFold(charsetLabel, "utf-8") && !strings.EqualFold(charsetLabel, "utf8") {
		if r, err := charset.NewReaderLabel(charsetLabel, bytes.NewReader(body)); err == nil {
			if decoded, err := io.ReadAll(r); err == nil {
				return string(decoded)
			}
		}
		return string(body)
	}
	if utf8.Valid(body) {
		return string(body)
	}
	e, _, certain := charset.DetermineEncoding(body, contentType)
	if e == nil || e == encoding.Nop {
		return string(body)
	}
	if !certain {
		if gbk := tryGBKDecode(body); gbk != "" {
			return gbk
		}
	}
	decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(body), e.NewDecoder()))
	if err != nil {
		return string(body)
	}
	return string(decoded)
}

// tryGBKDecode decodes as GBK and returns the text only when the non-ASCII
// part is overwhelmingly Han characters with no decoding errors.
func tryGBKDecode(body []byte) string {
	decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(body), simplifiedchinese.GBK.NewDecoder()))
	if err != nil {
		return ""
	}
	s := string(decoded)
	if !utf8.ValidString(s) {
		return ""
	}
	nonASCII, han := 0, 0
	for _, r := range s {
		if r <= 0x7f {
			continue
		}
		if r == utf8.RuneError {
			return ""
		}
		nonASCII++
		if unicode.Is(unicode.Han, r) {
			han++
		}
	}
	if nonASCII == 0 || han*4 < nonASCII*3 {
		return ""
	}
	return s
}

// truncateFetchText caps the text at max runes, backing off to the last
// sentence/newline boundary within 200 runes.
func truncateFetchText(text string, max int) string {
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	runes := []rune(text)
	cutoff := max
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
	return string(runes[:shown]) + "\n\n... [truncated: " + fmt.Sprintf("%d/%d chars", shown, len(runes)) + " shown]"
}

var antiBotRe = rexcache.Must(`(?i)just a moment|cf-chl|challenge-platform|attention required|cloudflare|anomaly-modal|verify you are (?:human|not a robot)|unusual traffic|captcha|百度安全验证`)

// antiBotNote recognises challenge walls (Cloudflare "Just a moment",
// DuckDuckGo anomaly, login captchas) served with a blocking status, so the
// model switches to the browser tool instead of reading the challenge text.
func antiBotNote(status int, body []byte) string {
	switch status {
	case http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusAccepted:
	default:
		return ""
	}
	if len(body) > 65536 {
		body = body[:65536]
	}
	if !antiBotRe.Match(body) {
		return ""
	}
	return "[blocked] The site answered with an anti-bot / rate-limit challenge rather than page content. Fetch it with the browser tool (navigate, then content/extract) instead of retrying web_fetch."
}

var (
	spaRootRe = rexcache.Must(`(?i)\bid=["'](?:root|app|__next|__nuxt|q-app|__docusaurus|gatsby-focus-wrapper)["']`)
	needsJSRe = rexcache.Must(`(?i)(?:enable|requires?)\s+javascript|javascript is (?:required|disabled)|<noscript\b`)
)

// looksJSRendered reports markup typical of a client-rendered SPA shell.
func looksJSRendered(rawHTML string) bool {
	return spaRootRe.MatchString(rawHTML) || needsJSRe.MatchString(rawHTML)
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

var (
	preBlockRe = rexcache.Must(`(?is)<pre\b[^>]*>(.*?)</pre\s*>`)
	anyTagRe   = rexcache.Must(`<[^>]+>`)
)

// stripHTML reduces an HTML document to readable text. <pre> blocks are set
// aside first: the generic pipeline trims every line, which would destroy
// code indentation, so their inner text is restored verbatim afterwards.
func stripHTML(s string) string {
	var pres []string
	if idx := preBlockRe.FindAllStringSubmatchIndex(s, 100); len(idx) > 0 {
		var sb strings.Builder
		last := 0
		for i, m := range idx {
			sb.WriteString(s[last:m[0]])
			pres = append(pres, strings.TrimSpace(html.UnescapeString(anyTagRe.ReplaceAllString(s[m[2]:m[3]], ""))))
			fmt.Fprintf(&sb, "\x00PRE%d\x00", i)
			last = m[1]
		}
		sb.WriteString(s[last:])
		s = sb.String()
	}

	for _, tag := range []string{"script", "style", "noscript", "template", "nav", "header", "footer", "aside", "form", "iframe"} {
		re := rexcache.Must(`(?is)<` + tag + `\b[^>]*>.*?</` + tag + `\s*>`)
		s = re.ReplaceAllString(s, "")
	}
	// SVG icons leak <path d="..."> into error pages when the <svg> is
	// self-closing or never closed; drop the element either way.
	s = rexcache.Must(`(?is)<svg\b[^>]*/>`).ReplaceAllString(s, "")
	s = rexcache.Must(`(?is)<svg\b[^>]*>.*?(?:</svg\s*>|$)`).ReplaceAllString(s, "")

	s = rexcache.Must(`(?is)<(meta|link|input|button)\b[^>]*/?>`).ReplaceAllString(s, "")

	if extracted := extractMainContent(s); extracted != "" {
		s = extracted
	}

	blockRe := rexcache.Must(`(?i)</?(p|div|section|article|header|footer|h[1-6]|li|tr|blockquote|pre|ul|ol|table|figure|figcaption|dt|dd)\b[^>]*>`)
	s = blockRe.ReplaceAllString(s, "\n")

	s = rexcache.Must(`(?i)<br\s*/?>`).ReplaceAllString(s, "\n")

	s = rexcache.Must(`<[^>]+>`).ReplaceAllString(s, " ")

	s = html.UnescapeString(s)

	lines := strings.Split(s, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	out := strings.Join(cleaned, "\n")
	for i, pre := range pres {
		out = strings.ReplaceAll(out, fmt.Sprintf("\x00PRE%d\x00", i), pre)
	}
	return out
}

func extractMainContent(htmlStr string) string {

	// Index pages (blogs, docs landing pages) hold many short <article>
	// blocks; taking only the first one dropped the rest of the page.
	if articles := extractTagInstances(htmlStr, "article"); len(articles) > 0 {
		joined := strings.Join(articles, "\n")
		if len(strings.TrimSpace(stripTagsOnly(joined))) >= 100 {
			return joined
		}
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
	instances := extractTagInstances(s, tag)
	if len(instances) == 0 {
		return ""
	}
	if len(strings.TrimSpace(stripTagsOnly(instances[0]))) < 100 {
		return ""
	}
	return instances[0]
}

// extractTagInstances returns the inner HTML of every <tag>...</tag> pair in
// s, honouring nesting (a tag opened inside an instance is matched to its
// own close before the instance closes). Unclosed opens yield nothing.
func extractTagInstances(s, tag string) []string {
	openRe := rexcache.Must(`(?is)<` + tag + `\b[^>]*>`)
	closeRe := rexcache.Must(`(?is)</` + tag + `\s*>`)

	opens := openRe.FindAllStringIndex(s, -1)
	var out []string
	pos := 0
	for {
		var open []int
		for _, o := range opens {
			if o[0] >= pos {
				open = o
				break
			}
		}
		if open == nil {
			return out
		}
		rest := s[open[1]:]
		consumed := 0
		depth := 1
		matched := false
		for {
			closeMatch := closeRe.FindStringIndex(rest)
			if closeMatch == nil {
				break
			}
			seg := rest[:closeMatch[0]]
			depth += len(openRe.FindAllStringIndex(seg, -1))
			depth--
			if depth == 0 {
				end := open[1] + consumed + closeMatch[0]
				out = append(out, s[open[1]:end])
				pos = open[1] + consumed + closeMatch[1]
				matched = true
				break
			}
			consumed += closeMatch[1]
			rest = rest[closeMatch[1]:]
		}
		if !matched {
			return out
		}
	}
}

func extractTaggedBlock(s, attrTailRE string) string {
	re := rexcache.Must(`(?is)<(\w+)[^>]*` + attrTailRE + `[^>]*>`)
	loc := re.FindStringSubmatchIndex(s)
	if loc == nil || len(loc) < 4 {
		return ""
	}
	name := s[loc[2]:loc[3]]
	start := s[loc[1]:]
	rest := start
	consumed := 0
	boundary := rexcache.Must(`(?i)<(/?)` + name + `(\s|>|/)`)
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
	return rexcache.Must(`<[^>]+>`).ReplaceAllString(s, " ")
}
