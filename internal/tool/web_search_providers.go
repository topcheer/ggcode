package tool

import (
	"context"
	"encoding/base64"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/rexcache"
	"github.com/topcheer/ggcode/internal/util"
)

// browserUserAgent is what the web tools send. DuckDuckGo serves its
// "anomaly" challenge (HTTP 202 + CAPTCHA) to non-browser agents and many
// sites 403 them outright, so the tools identify as a desktop Chrome.
const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// web_search backends, tried in order. DuckDuckGo's HTML endpoint is the
// primary (best snippets, no JS), but it rate-limits hard: after a burst of
// requests from one IP it answers 202 with a CAPTCHA page for a while. Bing
// is the fallback - it is reachable from networks where other engines are
// not and serves parseable organic results for both Latin and CJK queries.
const (
	ddgHTMLURL          = "https://html.duckduckgo.com/html/?q="
	bingSearchURL       = "https://www.bing.com/search?form=QBLH&q="
	searchFetchTimeout  = 12 * time.Second
	searchBodyByteLimit = 2 * 1024 * 1024

	searchCacheTTL        = 10 * time.Minute
	searchCacheMaxEntries = 64

	// Circuit breaker: a backend that challenged/errored is skipped for a
	// doubling cooldown (1m, 2m, 4m ... capped at 30m) so one blocked engine
	// does not add a wasted round-trip (and another rate-limit strike) to
	// every call. A success clears the breaker.
	searchBackendMinCooldown = time.Minute
	searchBackendMaxCooldown = 30 * time.Minute
)

type searchProvider struct {
	name    string
	url     func(query string) string
	parse   func(body string, max int) []searchResult
	blocked func(status int, body string) bool
}

var searchProviders = []searchProvider{
	{name: "duckduckgo", url: ddgSearchURL, parse: parseDDGResults, blocked: ddgChallenged},
	{name: "bing", url: bingSearchURLFor, parse: parseBingResults, blocked: bingBlocked},
	{name: "so360", url: so360SearchURL, parse: parseSo360Results, blocked: so360Blocked},
}

// so360SearchURL builds a 360 Search (so.com) query URL. 360 is reachable
// from mainland China without a proxy and returns organic results for
// technical queries where Bing, for cookie-less traffic from that region,
// serves an unrelated "decoy" SERP.
func so360SearchURL(query string) string {
	return so360SearchURLBase + url.QueryEscape(query)
}

const so360SearchURLBase = "https://www.so.com/s?ie=utf-8&src=srp&q="

// A result set whose best entry matches at least this share of the query
// terms is accepted without consulting the remaining backends; weaker sets
// are kept only if no later backend answers better. This is what filters
// out Bing's decoy SERPs (status 200, ten well-formed results, zero terms
// in common with the query).
const strongSearchRelevance = 40

// relevanceScore is the best query-term coverage among the top results
// (title hits weigh 3x snippet hits). Queries without Latin/digit terms
// (CJK) cannot be judged lexically and count as fully relevant.
func relevanceScore(query string, results []searchResult) int {
	terms := tokenizeQuery(query)
	if len(terms) == 0 {
		// CJK queries have no Latin terms: score Han bigrams instead, so a
		// decoy SERP ("Google home page" for 投资词典) still scores 0.
		terms = cjkBigrams(query)
		if len(terms) == 0 {
			if len(results) == 0 {
				return 0
			}
			return 100
		}
	}
	best := 0
	for i, r := range results {
		if i >= 5 {
			break
		}
		if s := scoreResult(terms, r); s > best {
			best = s
		}
	}
	return best
}

func ddgSearchURL(query string) string {
	return ddgHTMLURL + url.QueryEscape(query)
}

func bingSearchURLFor(query string) string {
	// CJK queries get the zh-CN market, otherwise Bing ranks by IP location.
	if containsCJK(query) {
		return bingSearchURL + url.QueryEscape(query) + "&mkt=zh-CN"
	}
	return bingSearchURL + url.QueryEscape(query)
}

// searchAcceptLanguage picks the Accept-Language header from the query's
// script so engines rank results in the language the user is asking in.
func searchAcceptLanguage(query string) string {
	if containsCJK(query) {
		return "zh-CN,zh;q=0.9,en;q=0.8"
	}
	return "en-US,en;q=0.9"
}

func containsCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			return true
		}
	}
	return false
}

type searchCacheEntry struct {
	results []searchResult
	source  string
	at      time.Time
}

type searchBackendState struct {
	mu        sync.Mutex
	failures  map[string]int
	skipUntil map[string]time.Time
	cache     map[string]searchCacheEntry
	cacheKeys []string // insertion order for FIFO eviction
}

var searchBackends = &searchBackendState{
	failures:  map[string]int{},
	skipUntil: map[string]time.Time{},
	cache:     map[string]searchCacheEntry{},
}

func (s *searchBackendState) cooldownFor(name string) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.skipUntil[name]
	if !ok || !time.Now().Before(until) {
		return 0, false
	}
	return time.Until(until), true
}

func (s *searchBackendState) recordFailure(name, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[name]++
	cooldown := searchBackendMinCooldown << (s.failures[name] - 1)
	if cooldown > searchBackendMaxCooldown || cooldown <= 0 {
		cooldown = searchBackendMaxCooldown
	}
	s.skipUntil[name] = time.Now().Add(cooldown)
	debug.Log("websearch", "backend %s failed (%s) - skipping for %s (strike %d)", name, reason, cooldown.Round(time.Second), s.failures[name])
}

func (s *searchBackendState) recordSuccess(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, name)
	delete(s.skipUntil, name)
}

func (s *searchBackendState) cached(key string) (searchCacheEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[key]
	if !ok {
		return searchCacheEntry{}, false
	}
	if time.Since(e.at) > searchCacheTTL {
		s.deleteLocked(key)
		return searchCacheEntry{}, false
	}
	return e, true
}

func (s *searchBackendState) put(key string, e searchCacheEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.cache[key]; !exists {
		s.cacheKeys = append(s.cacheKeys, key)
		for len(s.cacheKeys) > searchCacheMaxEntries {
			s.deleteLocked(s.cacheKeys[0])
			s.cacheKeys = s.cacheKeys[1:]
		}
	}
	e.at = time.Now()
	s.cache[key] = e
}

func (s *searchBackendState) deleteLocked(key string) {
	if _, ok := s.cache[key]; ok {
		delete(s.cache, key)
		for i, k := range s.cacheKeys {
			if k == key {
				s.cacheKeys = append(s.cacheKeys[:i], s.cacheKeys[i+1:]...)
				break
			}
		}
	}
}

// resetSearchBackends clears the breaker and cache (tests).
func resetSearchBackends() {
	searchBackends.mu.Lock()
	defer searchBackends.mu.Unlock()
	searchBackends.failures = map[string]int{}
	searchBackends.skipUntil = map[string]time.Time{}
	searchBackends.cache = map[string]searchCacheEntry{}
	searchBackends.cacheKeys = nil
}

// querySearchBackends runs the query through the backend chain and returns
// the first non-empty raw result set (pre domain filtering) plus the backend
// it came from (prefixed with "cache:" on a cache hit). An empty result means
// every reachable backend returned nothing - either a genuinely empty query
// or all backends being rate-limited; the caller decides via the cooldown
// state exposed by AllSearchBackendsCoolingDown.
func querySearchBackends(ctx context.Context, query string, fetchLimit int) ([]searchResult, string) {
	cacheKey := query + "\x00" + strconv.Itoa(fetchLimit)
	if e, ok := searchBackends.cached(cacheKey); ok {
		debug.Log("websearch", "cache hit for %q (%d results from %s)", query, len(e.results), e.source)
		return e.results, "cache:" + e.source
	}

	lang := searchAcceptLanguage(query)
	var best []searchResult
	var bestSource string
	bestScore := -1
	for _, p := range searchProviders {
		if cd, cooling := searchBackends.cooldownFor(p.name); cooling {
			debug.Log("websearch", "backend %s in cooldown (%s left) - skipping", p.name, cd.Round(time.Second))
			continue
		}
		if ctx.Err() != nil {
			break
		}
		start := time.Now()
		body, status, err := fetchSearchPage(ctx, p.url(query), lang)
		if err != nil {
			searchBackends.recordFailure(p.name, err.Error())
			continue
		}
		if status != http.StatusOK {
			searchBackends.recordFailure(p.name, "http "+strconv.Itoa(status))
			continue
		}
		if p.blocked != nil && p.blocked(status, body) {
			searchBackends.recordFailure(p.name, "anti-bot challenge")
			continue
		}
		results := p.parse(body, fetchLimit)
		score := relevanceScore(query, results)
		debug.Log("websearch", "backend %s: %d results (relevance %d) for %q in %s", p.name, len(results), score, query, time.Since(start).Round(time.Millisecond))
		if len(results) == 0 {
			// Not counted as a failure: a rare query legitimately has no
			// hits, and the next backend may still answer it.
			continue
		}
		searchBackends.recordSuccess(p.name)
		if score > bestScore {
			best, bestSource, bestScore = results, p.name, score
		}
		if score >= strongSearchRelevance {
			break
		}
	}
	if best == nil {
		return nil, ""
	}
	searchBackends.put(cacheKey, searchCacheEntry{results: best, source: bestSource})
	return best, bestSource
}

// AllSearchBackendsCoolingDown reports whether every configured backend is
// currently circuit-broken, i.e. an empty result is a rate-limit artefact
// rather than "nothing found".
func AllSearchBackendsCoolingDown() bool {
	for _, p := range searchProviders {
		if _, cooling := searchBackends.cooldownFor(p.name); !cooling {
			return false
		}
	}
	return len(searchProviders) > 0
}

func fetchSearchPage(ctx context.Context, rawURL, lang string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", lang)

	// Per-backend timeout independent of the caller's (adaptive) deadline so
	// a hung engine still leaves time for the fallback. A sooner caller
	// deadline still wins.
	ctx, cancel := context.WithTimeout(ctx, searchFetchTimeout)
	defer cancel()

	resp, err := util.NewInsecureAwareClient(0).Do(req.WithContext(ctx))
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, err := util.ReadAll(resp.Body, searchBodyByteLimit)
	if err != nil {
		return "", resp.StatusCode, err
	}
	return string(body), resp.StatusCode, nil
}

// ddgChallenged recognises DuckDuckGo's bot check: served with HTTP 202
// (sometimes 200) and no results, containing the anomaly/CAPTCHA modal.
func ddgChallenged(status int, body string) bool {
	if status != http.StatusOK && status != http.StatusAccepted {
		return false
	}
	return strings.Contains(body, "anomaly-modal") || strings.Contains(body, "bots use DuckDuckGo too") || strings.Contains(body, "anomaly/images/challenge")
}

var bingBlockedRe = rexcache.Must(`(?i)unusual traffic|captcha-container|verify you are (?:human|not a robot)|b_captcha`)

func bingBlocked(status int, body string) bool {
	return bingBlockedRe.MatchString(body)
}

var (
	bingBlockRe   = rexcache.Must(`(?is)<li[^>]+class="[^"]*\bb_algo\b[^"]*"[^>]*>`)
	bingHeadingRe = rexcache.Must(`(?is)<h2[^>]*>\s*<a\b([^>]*)>(.*?)</a\s*>`)
	bingHrefRe    = rexcache.Must(`(?is)\bhref\s*=\s*"([^"]*)"`)
	bingCaptionRe = rexcache.Must(`(?is)<div[^>]+class="[^"]*\bb_caption\b[^"]*"[^>]*>(.*?)</div\s*>`)
	bingPRe       = rexcache.Must(`(?is)<p\b[^>]*>(.*?)</p\s*>`)
	bingCiteRe    = rexcache.Must(`(?is)<cite\b[^>]*>(.*?)</cite\s*>`)
)

// parseBingResults extracts the organic `li.b_algo` results from a Bing SERP.
// Titles sit in `<h2><a href>`, snippets in `.b_caption p`, and result links
// are wrapped in `bing.com/ck/a?...&u=a1<base64url>` redirects that
// decodeBingRedirect unwraps.
func parseBingResults(body string, max int) []searchResult {
	indices := bingBlockRe.FindAllStringIndex(body, max)
	var results []searchResult
	for i, idx := range indices {
		end := len(body)
		if i+1 < len(indices) {
			end = indices[i+1][0]
		}
		block := body[idx[1]:end]

		heading := bingHeadingRe.FindStringSubmatch(block)
		if heading == nil {
			continue
		}
		title := normalizeResultTitle(StripHTML(heading[2]))
		if title == "" {
			continue
		}
		resultURL := ""
		if href := bingHrefRe.FindStringSubmatch(heading[1]); href != nil {
			resultURL = decodeBingRedirect(href[1])
		}
		if resultURL == "" {
			if cite := bingCiteRe.FindStringSubmatch(block); cite != nil {
				resultURL = bingCiteToURL(StripHTML(cite[1]))
			}
		}
		if resultURL == "" || isEngineSelfURL(resultURL) {
			continue
		}

		snippet := ""
		if cap := bingCaptionRe.FindStringSubmatch(block); cap != nil {
			if p := bingPRe.FindStringSubmatch(cap[1]); p != nil {
				snippet = cleanSearchText(StripHTML(p[1]))
			}
		}
		if snippet == "" {
			if p := bingPRe.FindStringSubmatch(block); p != nil {
				snippet = cleanSearchText(StripHTML(p[1]))
			}
		}
		results = append(results, searchResult{Title: title, URL: resultURL, Snippet: snippet})
		if len(results) >= max {
			break
		}
	}
	return results
}

// cleanSearchText drops invalid UTF-8 (SERP snippets occasionally carry a
// truncated multi-byte character) and collapses whitespace.
func cleanSearchText(s string) string {
	return strings.Join(strings.Fields(strings.ToValidUTF8(s, "")), " ")
}

// normalizeResultTitle collapses the whitespace StripHTML leaves behind
// where inline tags (<strong>, <b>, <em>) sat inside a SERP title, e.g.
// "Go Concurrency Patterns : Context" -> "Go Concurrency Patterns: Context".
func normalizeResultTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	for _, punct := range []string{":", ",", ";", ".", ")", "]", "?"} {
		title = strings.ReplaceAll(title, " "+punct, punct)
	}
	title = strings.ReplaceAll(title, "( ", "(")
	return strings.TrimSpace(title)
}

// decodeBingRedirect unwraps Bing's click-tracking links
// (https://www.bing.com/ck/a?...&u=a1<base64url>) to the target URL. Plain
// http(s) links pass through; anything else yields "".
func decodeBingRedirect(raw string) string {
	raw = strings.TrimSpace(html.UnescapeString(raw))
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if (host == "bing.com" || strings.HasSuffix(host, ".bing.com")) && u.Path == "/ck/a" {
		enc := u.Query().Get("u")
		if !strings.HasPrefix(enc, "a1") {
			return ""
		}
		dec, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(enc[2:], "="))
		if err != nil {
			return ""
		}
		target := strings.TrimSpace(string(dec))
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
			return target
		}
		return ""
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u.String()
	}
	return ""
}

// bingCiteToURL turns the display URL shown in `<cite>` ("https://go.dev › doc")
// into a best-effort URL when the real href could not be decoded.
func bingCiteToURL(cite string) string {
	cite = strings.TrimSpace(strings.Split(cite, "›")[0])
	cite = strings.TrimSpace(strings.Split(cite, " ")[0])
	if cite == "" {
		return ""
	}
	if !strings.HasPrefix(cite, "http://") && !strings.HasPrefix(cite, "https://") {
		cite = "https://" + cite
	}
	if u, err := url.Parse(cite); err == nil && u.Hostname() != "" {
		return u.String()
	}
	return ""
}

var so360BlockedRe = rexcache.Must(`(?i)360安全验证|百度安全验证|so\.com/antivirus|请输入验证码`)

func so360Blocked(status int, body string) bool {
	return so360BlockedRe.MatchString(body)
}

var (
	so360BlockRe = rexcache.Must(`(?is)<li[^>]+class="[^"]*\bres-list\b[^"]*"[^>]*>`)
	so360TitleRe = rexcache.Must(`(?is)<h3[^>]*>\s*<a\b([^>]*)>(.*?)</a\s*>`)
	so360DescRe  = rexcache.Must(`(?is)<p\b[^>]*class="[^"]*\bres-desc\b[^"]*"[^>]*>(.*?)</p\s*>`)
	so360SumRe   = rexcache.Must(`(?is)<span\b[^>]*data-result-summary[^>]*>(.*?)</span\s*>`)
	so360MdurlRe = rexcache.Must(`(?is)\bdata-mdurl\s*=\s*"([^"]*)"`)
)

// parseSo360Results extracts organic results from a 360 Search (so.com) SERP:
// `li.res-list` blocks whose `h3.res-title > a` carries the real target in
// `data-mdurl` (the href is a so.com/link redirect) and the snippet in
// `p.res-desc`. Related-search blocks have no res-title anchor and drop out.
func parseSo360Results(body string, max int) []searchResult {
	indices := so360BlockRe.FindAllStringIndex(body, max*3)
	var results []searchResult
	for i, idx := range indices {
		end := len(body)
		if i+1 < len(indices) {
			end = indices[i+1][0]
		}
		block := body[idx[1]:end]
		heading := so360TitleRe.FindStringSubmatch(block)
		if heading == nil {
			continue
		}
		title := normalizeResultTitle(StripHTML(heading[2]))
		resultURL := ""
		if md := so360MdurlRe.FindStringSubmatch(heading[1]); md != nil {
			resultURL = strings.TrimSpace(html.UnescapeString(md[1]))
		}
		if resultURL == "" {
			if href := bingHrefRe.FindStringSubmatch(heading[1]); href != nil {
				resultURL = decodeBingRedirect(href[1])
			}
		}
		if title == "" || resultURL == "" || isEngineSelfURL(resultURL) {
			continue
		}
		snippet := ""
		if desc := so360DescRe.FindStringSubmatch(block); desc != nil {
			snippet = cleanSearchText(StripHTML(desc[1]))
			if sum := so360SumRe.FindStringSubmatch(desc[1]); sum != nil {
				snippet = cleanSearchText(StripHTML(sum[1]))
			}
		}
		results = append(results, searchResult{Title: title, URL: resultURL, Snippet: strings.TrimSpace(snippet)})
		if len(results) >= max {
			break
		}
	}
	return results
}

// cjkBigrams returns the de-duplicated 2-character windows over every Han
// run in s ("投资 字典" -> 投资, 字典) as a stand-in for word segmentation.
func cjkBigrams(s string) []string {
	var out []string
	seen := map[string]bool{}
	run := make([]rune, 0, 8)
	flush := func() {
		for i := 0; i+1 < len(run); i++ {
			g := string(run[i : i+2])
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
		run = run[:0]
	}
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			run = append(run, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// engineSelfHosts are the search engines' own properties. Links into them
// are oneboxes (360图片/翻译, Bing images/maps, DDG ad redirects), not web
// results, so the parsers drop them.
var engineSelfHosts = []string{"so.com", "bing.com", "duckduckgo.com", "baidu.com"}

func isEngineSelfURL(rawURL string) bool {
	host := strings.ToLower(domainFromURL(rawURL))
	for _, h := range engineSelfHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}
