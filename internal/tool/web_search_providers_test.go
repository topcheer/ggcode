package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var bingSERPFixture = `<!DOCTYPE html><html><body><ol id="b_results">
<li class="b_algo" data-id="1"><h2><a target="_blank" href="https://www.bing.com/ck/a?!&amp;&amp;p=abc&amp;ptn=3&amp;fclid=x&amp;u=a1` +
	// base64url("https://go.dev/blog/context") without padding
	base64.RawURLEncoding.EncodeToString([]byte("https://go.dev/blog/context")) +
	`&amp;ntb=1"><strong>Go Concurrency Patterns</strong>: Context</a></h2>
<div class="b_caption"><p class="b_lineclamp2">Go's <em>context</em> package makes it easy to pass request-scoped values.</p><div class="b_attribution"><cite>https://go.dev › blog › context</cite></div></div></li>
<li class="b_ad"><h2><a href="https://ads.example/x">Ad</a></h2></li>
<li class="b_algo" data-id="2"><h2><a href="https://pkg.go.dev/context" target="_blank">context package - pkg.go.dev</a></h2>
<div class="b_caption"><div class="b_attribution"><cite>https://pkg.go.dev › context</cite></div></div><p>Package context defines the Context type.</p></div></li>
<li class="b_algo" data-id="3"><h2><a href="https://www.bing.com/ck/a?u=a1NOTBASE64!!">Broken</a></h2><p>Should fall back to cite.</p><cite>https://go.dev › doc › install</cite></li>
</ol></body></html>`

func TestParseBingResults_DecodesRedirectsAndSkipsAds(t *testing.T) {
	results := parseBingResults(bingSERPFixture, 10)
	if len(results) != 3 {
		t.Fatalf("expected 3 organic results (ad skipped), got %d: %+v", len(results), results)
	}
	if results[0].Title != "Go Concurrency Patterns: Context" {
		t.Errorf("title should be tag-stripped, got %q", results[0].Title)
	}
	if results[0].URL != "https://go.dev/blog/context" {
		t.Errorf("ck/a redirect not decoded, got %q", results[0].URL)
	}
	if !strings.Contains(results[0].Snippet, "request-scoped values") {
		t.Errorf("snippet not extracted from b_caption p, got %q", results[0].Snippet)
	}
	if results[1].URL != "https://pkg.go.dev/context" {
		t.Errorf("direct href should pass through, got %q", results[1].URL)
	}
	if results[2].URL != "https://go.dev" {
		t.Errorf("undecodable redirect should fall back to <cite>, got %q", results[2].URL)
	}
}

func TestParseBingResults_RespectsMax(t *testing.T) {
	if got := len(parseBingResults(bingSERPFixture, 1)); got != 1 {
		t.Fatalf("expected max 1, got %d", got)
	}
}

func TestDecodeBingRedirect(t *testing.T) {
	enc := "a1" + base64.RawURLEncoding.EncodeToString([]byte("https://example.com/a?b=1&c=2"))
	cases := map[string]string{
		"https://www.bing.com/ck/a?!&p=x&u=" + enc + "&ntb=1": "https://example.com/a?b=1&c=2",
		"https://example.com/plain":                           "https://example.com/plain",
		"http://example.com/http":                             "http://example.com/http",
		"https://www.bing.com/ck/a?u=a1###":                   "",
		"https://www.bing.com/ck/a?u=a1" + base64.RawURLEncoding.EncodeToString([]byte("javascript:alert(1)")): "",
		"javascript:alert(1)": "",
		"":                    "",
	}
	for in, want := range cases {
		if got := decodeBingRedirect(in); got != want {
			t.Errorf("decodeBingRedirect(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBingSearchURL_MarketForCJK(t *testing.T) {
	if got := bingSearchURLFor("投资词典"); !strings.Contains(got, "mkt=zh-CN") {
		t.Errorf("CJK query should request the zh-CN market, got %s", got)
	}
	if got := bingSearchURLFor("go context"); strings.Contains(got, "mkt=") {
		t.Errorf("Latin query should not pin a market, got %s", got)
	}
	if searchAcceptLanguage("投资词典") == searchAcceptLanguage("go context") {
		t.Error("Accept-Language should follow the query script")
	}
}

func TestChallengeDetection(t *testing.T) {
	ddgChallenge := `<html><body><div class="anomaly-modal__box"></div>bots use DuckDuckGo too.</body></html>`
	if !ddgChallenged(http.StatusAccepted, ddgChallenge) {
		t.Error("DDG anomaly page (202) must be detected as a challenge")
	}
	if !ddgChallenged(http.StatusOK, ddgChallenge) {
		t.Error("DDG anomaly page (200) must be detected as a challenge")
	}
	if ddgChallenged(http.StatusOK, `<div class="result"><a class="result__a" href="https://x">t</a></div>`) {
		t.Error("normal DDG SERP must not be treated as a challenge")
	}
	if ddgChallenged(http.StatusInternalServerError, "") {
		t.Error("non-200/202 statuses are reported as HTTP errors, not challenges")
	}
	if !bingBlocked(http.StatusOK, "<div>Our systems have detected unusual traffic from your computer network</div>") {
		t.Error("Bing unusual-traffic page must be detected")
	}
	if bingBlocked(http.StatusOK, bingSERPFixture) {
		t.Error("normal Bing SERP must not be flagged")
	}
}

// providerFixture swaps the backend chain for two httptest servers and
// restores it afterwards.
func providerFixture(t *testing.T, ddgHandler, bingHandler http.HandlerFunc) (*httptest.Server, *httptest.Server) {
	t.Helper()
	ddg := httptest.NewServer(ddgHandler)
	bing := httptest.NewServer(bingHandler)
	t.Cleanup(ddg.Close)
	t.Cleanup(bing.Close)

	orig := searchProviders
	searchProviders = []searchProvider{
		{name: "duckduckgo", url: func(q string) string { return ddg.URL + "/?q=" + url.QueryEscape(q) }, parse: parseDDGResults, blocked: ddgChallenged},
		{name: "bing", url: func(q string) string { return bing.URL + "/?q=" + url.QueryEscape(q) }, parse: parseBingResults, blocked: bingBlocked},
	}
	t.Cleanup(func() { searchProviders = orig })
	resetSearchBackends()
	t.Cleanup(resetSearchBackends)
	return ddg, bing
}

func TestQuerySearchBackends_FallsBackToBingWhenDDGChallenges(t *testing.T) {
	var ddgHits, bingHits atomic.Int32
	ddg, bing := providerFixture(t,
		func(w http.ResponseWriter, r *http.Request) {
			ddgHits.Add(1)
			if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, "Mozilla/5.0") {
				t.Errorf("search request should carry a browser UA, got %q", ua)
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `<div class="anomaly-modal__box">bots use DuckDuckGo too.</div>`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			bingHits.Add(1)
			if got := r.URL.Query().Get("q"); got == "" {
				t.Errorf("bing received an empty query")
			}
			fmt.Fprint(w, bingSERPFixture)
		},
	)
	_ = ddg
	_ = bing

	results, source := querySearchBackends(context.Background(), "go context", 10)
	if source != "bing" {
		t.Fatalf("expected results from the bing fallback, got source %q", source)
	}
	if len(results) != 3 || results[0].URL != "https://go.dev/blog/context" {
		t.Fatalf("unexpected results: %+v", results)
	}
	if ddgHits.Load() != 1 || bingHits.Load() != 1 {
		t.Fatalf("expected one request per backend, got ddg=%d bing=%d", ddgHits.Load(), bingHits.Load())
	}

	// DDG is now circuit-broken: the next query goes straight to Bing.
	if _, cooling := searchBackends.cooldownFor("duckduckgo"); !cooling {
		t.Fatal("challenged backend must enter cooldown")
	}
	resetSearchBackendsCacheOnly()
	results, source = querySearchBackends(context.Background(), "another query", 10)
	if source != "bing" || len(results) != 3 {
		t.Fatalf("expected bing again, got source=%q results=%d", source, len(results))
	}
	if ddgHits.Load() != 1 {
		t.Fatalf("DDG must be skipped while in cooldown, hits=%d", ddgHits.Load())
	}
}

func resetSearchBackendsCacheOnly() {
	searchBackends.mu.Lock()
	defer searchBackends.mu.Unlock()
	searchBackends.cache = map[string]searchCacheEntry{}
	searchBackends.cacheKeys = nil
}

func TestQuerySearchBackends_CachesRepeatedQueries(t *testing.T) {
	var hits atomic.Int32
	_, _ = providerFixture(t,
		func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			fmt.Fprint(w, `<div class="result"><a class="result__a" href="https://example.com/a">cache me: A</a><a class="result__snippet">cache me sa</a></div><div class="result"><a class="result__a" href="https://example.com/b">cache me B</a><a class="result__snippet">cache me sb</a></div>`)
		},
		func(w http.ResponseWriter, r *http.Request) { t.Error("bing must not be queried while DDG answers") },
	)

	first, src1 := querySearchBackends(context.Background(), "cache me", 5)
	second, src2 := querySearchBackends(context.Background(), "cache me", 5)
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected 2 results both times, got %d/%d", len(first), len(second))
	}
	if src1 != "duckduckgo" || src2 != "cache:duckduckgo" {
		t.Fatalf("expected second call to be served from cache, got %q / %q", src1, src2)
	}
	if hits.Load() != 1 {
		t.Fatalf("backend should be hit once, got %d", hits.Load())
	}
	// A different fetchLimit is a different cache entry.
	querySearchBackends(context.Background(), "cache me", 30)
	if hits.Load() != 2 {
		t.Fatalf("different fetchLimit should miss the cache, hits=%d", hits.Load())
	}
}

func TestQuerySearchBackends_ZeroResultsIsNotAFailure(t *testing.T) {
	var ddgHits atomic.Int32
	_, _ = providerFixture(t,
		func(w http.ResponseWriter, r *http.Request) {
			ddgHits.Add(1)
			fmt.Fprint(w, "<html><body>no results</body></html>")
		},
		func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html><body>nothing</body></html>") },
	)
	results, source := querySearchBackends(context.Background(), "zzz nonexistent", 5)
	if len(results) != 0 || source != "" {
		t.Fatalf("expected no results, got %+v from %q", results, source)
	}
	if _, cooling := searchBackends.cooldownFor("duckduckgo"); cooling {
		t.Fatal("an empty SERP must not trip the circuit breaker")
	}
	// Not cached either: a later answer can still come in.
	if ddgHits.Load() != 1 {
		t.Fatalf("unexpected hit count %d", ddgHits.Load())
	}
}

func TestQuerySearchBackends_TransportErrorTripsBreaker(t *testing.T) {
	var dead atomic.Bool
	dead.Store(true)
	_, _ = providerFixture(t,
		func(w http.ResponseWriter, r *http.Request) {
			if dead.Load() {
				// drop the connection without a response
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			fmt.Fprint(w, `<div class="result"><a class="result__a" href="https://example.com/a">A</a></div>`)
		},
		func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, bingSERPFixture) },
	)
	if _, source := querySearchBackends(context.Background(), "q", 5); source != "bing" {
		t.Fatalf("expected fallback after transport error, got %q", source)
	}
	if _, cooling := searchBackends.cooldownFor("duckduckgo"); !cooling {
		t.Fatal("transport error must trip the breaker")
	}
}

func TestWebSearchExecute_AllBackendsCoolingDownMessage(t *testing.T) {
	resetSearchHealth()
	t.Cleanup(resetSearchHealth)
	_, _ = providerFixture(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, "anomaly-modal")
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, "unusual traffic captcha")
		},
	)
	// Trip both breakers.
	querySearchBackends(context.Background(), "warm", 5)
	if !AllSearchBackendsCoolingDown() {
		t.Fatal("both backends should be cooling down")
	}
	res, err := WebSearch{}.Execute(context.Background(), json.RawMessage(`{"query":"warm again","description":"t"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "rate-limited") {
		t.Fatalf("expected an explicit rate-limit explanation, got %q", res.Content)
	}
}

const so360SERPFixture = `<ul class="result"><li class="res-list" data-result-type="related"><h3><a href="/s?q=louis+pion">louis pion</a></h3></li>
<li class="res-list" data-result-type="normal"><h3 class="res-title "> <a href="https://www.so.com/link?m=wXJZ" data-mdurl="https://blog.csdn.net/gitblog_00751/article/details/155417516" rel="noopener" target="_blank"><em>Pion</em> WebRTC:用Go语言构建实时音视频通信的终极方案-CSDN博客</a></h3>
<p class="res-desc"><span class="gray g-c-gray">2026年3月4日&nbsp;-&nbsp;</span><span data-result-summary>examples/<em>ice</em>-restart/:演示了 ICE 重启功能。文章浏览阅读1k次。</span></p><p class="g-linkinfo"><cite><a href="https://www.so.com/link?m=zRV">blog.csdn.net</a></cite></p></li>
<li class="res-list"><h3 class="res-title"><a href="https://direct.example/x">Direct link</a></h3><p class="res-desc">no summary span here</p></li>
</ul>`

func TestParseSo360Results(t *testing.T) {
	results := parseSo360Results(so360SERPFixture, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 organic results (related-search block skipped), got %d: %+v", len(results), results)
	}
	if results[0].URL != "https://blog.csdn.net/gitblog_00751/article/details/155417516" {
		t.Errorf("real URL should come from data-mdurl, got %q", results[0].URL)
	}
	if results[0].Title != "Pion WebRTC:用Go语言构建实时音视频通信的终极方案-CSDN博客" {
		t.Errorf("title mismatch: %q", results[0].Title)
	}
	if !strings.Contains(results[0].Snippet, "ICE 重启功能") || strings.Contains(results[0].Snippet, "2026年3月4日") {
		t.Errorf("snippet should come from data-result-summary without the date prefix, got %q", results[0].Snippet)
	}
	if results[1].URL != "https://direct.example/x" || results[1].Snippet != "no summary span here" {
		t.Errorf("direct href / plain desc fallback broken: %+v", results[1])
	}
	if !so360Blocked(http.StatusOK, "<html>360安全验证</html>") || so360Blocked(http.StatusOK, so360SERPFixture) {
		t.Error("360 verification wall detection is off")
	}
}

func TestRelevanceScore(t *testing.T) {
	junk := []searchResult{{Title: "Tracking | UPS", Snippet: "Where's my package"}, {Title: "Microsoft - Official Home Page"}}
	good := []searchResult{{Title: "Pion WebRTC: ICE restart examples", Snippet: "examples/ice-restart demonstrates ICE restart"}}
	if got := relevanceScore("pion ice restart", junk); got != 0 {
		t.Errorf("unrelated SERP should score 0, got %d", got)
	}
	if got := relevanceScore("pion ice restart", good); got < strongSearchRelevance {
		t.Errorf("on-topic SERP should be strong, got %d", got)
	}
	if got := relevanceScore("投资词典", good); got != 0 {
		t.Errorf("CJK query against an English decoy SERP should score 0, got %d", got)
	}
	cjk := []searchResult{{Title: "投资词典 · Invest Dictionary", Snippet: "投资术语中英文对照"}}
	if got := relevanceScore("投资词典", cjk); got < strongSearchRelevance {
		t.Errorf("on-topic CJK SERP should be strong, got %d", got)
	}
	if got := relevanceScore("the", good); got != 100 {
		t.Errorf("queries without judgeable terms must count as relevant, got %d", got)
	}
	if got := relevanceScore("投资词典", nil); got != 0 {
		t.Errorf("no results must score 0, got %d", got)
	}
	if !isEngineSelfURL("https://fanyi.so.com/?src=onebox") || isEngineSelfURL("https://blog.csdn.net/x") {
		t.Error("engine-self URL detection is off")
	}
}

// A backend that answers 200 with a well-formed but unrelated SERP (Bing's
// decoy page for cookie-less traffic) must lose to a later, relevant backend.
func TestQuerySearchBackends_PrefersRelevantBackendOverDecoy(t *testing.T) {
	_, _ = providerFixture(t,
		func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `<div class="result"><a class="result__a" href="https://ups.example/">Tracking | UPS</a><a class="result__snippet">Where's my package</a></div>`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `<li class="b_algo"><h2><a href="https://pkg.go.dev/context">context package</a></h2><div class="b_caption"><p>Package context defines the Context type for go.</p></div></li>`)
		},
	)
	results, source := querySearchBackends(context.Background(), "go context", 5)
	if source != "bing" || len(results) != 1 || results[0].URL != "https://pkg.go.dev/context" {
		t.Fatalf("expected the relevant fallback to win over the decoy, got source=%q results=%+v", source, results)
	}
}

func TestQuerySearchBackends_StrongFirstBackendStopsTheChain(t *testing.T) {
	var bingHits atomic.Int32
	_, _ = providerFixture(t,
		func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `<div class="result"><a class="result__a" href="https://go.dev/blog/context">Go Concurrency Patterns: Context</a><a class="result__snippet">go context timeout</a></div>`)
		},
		func(w http.ResponseWriter, r *http.Request) { bingHits.Add(1) },
	)
	if _, source := querySearchBackends(context.Background(), "go context", 5); source != "duckduckgo" {
		t.Fatalf("strong primary answer should stop the chain, got %q", source)
	}
	if bingHits.Load() != 0 {
		t.Fatalf("fallback must not be queried after a strong answer, hits=%d", bingHits.Load())
	}
}

func TestSearchBackendCooldownBackoff(t *testing.T) {
	resetSearchBackends()
	t.Cleanup(resetSearchBackends)
	searchBackends.recordFailure("x", "boom")
	if cd, _ := searchBackends.cooldownFor("x"); cd < 50*time.Second || cd > searchBackendMinCooldown+time.Second {
		t.Fatalf("first strike cooldown should be ~1m, got %s", cd)
	}
	searchBackends.recordFailure("x", "boom")
	if cd, _ := searchBackends.cooldownFor("x"); cd <= searchBackendMinCooldown {
		t.Fatalf("second strike cooldown should double, got %s", cd)
	}
	searchBackends.recordSuccess("x")
	if _, cooling := searchBackends.cooldownFor("x"); cooling {
		t.Fatal("success must clear the breaker")
	}
}
