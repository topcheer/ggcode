package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveWebProbe exercises the real web_search / web_fetch network path
// against live backends. Opt-in: GGCODE_LIVE_WEB=1. It never asserts on
// third-party content (search engines rank differently per region); it
// reports what the tools returned so a human can judge backend health.
//
//	GGCODE_LIVE_WEB=1 go test -tags goolm -run TestLiveWebProbe -v ./internal/tool/
func TestLiveWebProbe(t *testing.T) {
	mode := os.Getenv("GGCODE_LIVE_WEB")
	if mode == "" {
		t.Skip("set GGCODE_LIVE_WEB=1 (or =fetch) to run the live web probe")
	}
	ctx := context.Background()

	queries := []string{
		"golang context with timeout example",
		"pion webrtc data channel keepalive",
		"投资 字典 达文",
	}
	if mode == "fetch" {
		queries = nil
	}
	for _, q := range queries {
		start := time.Now()
		res, err := WebSearch{}.Execute(ctx, json.RawMessage(fmt.Sprintf(`{"query": %q, "max_results": 5, "description": "probe"}`, q)))
		if err != nil {
			t.Errorf("web_search %q: %v", q, err)
			continue
		}
		first := strings.SplitN(res.Content, "\n", 2)[0]
		n := strings.Count(res.Content, "URL:")
		t.Logf("web_search %q: %.1fs isError=%v results=%d first=%q", q, time.Since(start).Seconds(), res.IsError, n, first)
		if res.IsError || n == 0 {
			t.Errorf("web_search %q returned no results: %s", q, res.Content)
		}
	}

	urls := []string{
		"https://docs.railway.com/reference",                                      // normal docs page
		"https://go.dev/blog/context",                                             // code blocks (<pre>)
		"https://go.dev/blog",                                                     // many <article> summaries
		"https://httpbin.org/json",                                                // application/json with no '<'
		"https://raw.githubusercontent.com/pion/webrtc/master/README.md",          // text/plain with '<' and code
		"https://go.dev/blog/feed.atom",                                           // application/xml
		"http://go.dev/blog/context",                                              // http -> https redirect
		"http://www.people.com.cn/",                                               // GB2312/GBK Chinese news site
		"https://www.runoob.com/go/go-tutorial.html",                              // Chinese page
		"https://example.com/",                                                    // tiny static page
		"https://app.uniswap.org/",                                                // client-rendered SPA
		"https://nowsecure.nl/",                                                   // Cloudflare "Just a moment" test site
		"https://www.w3.org/WAI/ER/tests/xhtml/testfiles/resources/pdf/dummy.pdf", // application/pdf
		"https://httpbin.org/status/503",                                          // server error
	}
	for _, u := range urls {
		start := time.Now()
		res, err := WebFetch{}.Execute(ctx, json.RawMessage(fmt.Sprintf(`{"url": %q, "description": "probe"}`, u)))
		if err != nil {
			t.Errorf("web_fetch %s: %v", u, err)
			continue
		}
		content := res.Content
		if len(content) > 160 {
			content = content[:160]
		}
		t.Logf("web_fetch %s: %.1fs isError=%v len=%d head=%q", u, time.Since(start).Seconds(), res.IsError, len(res.Content), strings.ReplaceAll(content, "\n", " "))
		if res.IsError {
			t.Errorf("web_fetch %s failed: %s", u, res.Content)
		}
	}
}
