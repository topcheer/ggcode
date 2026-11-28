package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func fetchURL(t *testing.T, ts *httptest.Server, path string) Result {
	t.Helper()
	res, err := WebFetch{AllowPrivate: true}.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"url": %q, "description": "t"}`, ts.URL+path)))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

// JSON bodies must come back verbatim: the HTML stripper used to eat "<"
// in code and generic markup that legitimately contains angle brackets.
func TestWebFetch_JSONBodyIsNotHTMLStripped(t *testing.T) {
	payload := `{"query":"a < b && c > d","html":"<div>keep me</div>","n":[1,2]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprint(w, payload)
	}))
	defer ts.Close()

	res := fetchURL(t, ts, "/")
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a < b && c > d") || !strings.Contains(res.Content, "<div>keep me</div>") {
		t.Fatalf("JSON was mangled by HTML stripping: %q", res.Content)
	}
}

func TestWebFetch_PlainTextAndXMLAreVerbatim(t *testing.T) {
	md := "# Title\n\n\tindented <Tag> stays\n"
	xml := `<?xml version="1.0"?><feed><entry><title>A &amp; B</title></entry></feed>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/md":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, md)
		case "/atom":
			w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
			fmt.Fprint(w, xml)
		}
	}))
	defer ts.Close()

	if res := fetchURL(t, ts, "/md"); res.IsError || !strings.Contains(res.Content, "\tindented <Tag> stays") {
		t.Fatalf("text/plain lost verbatim content: %q", res.Content)
	}
	if res := fetchURL(t, ts, "/atom"); res.IsError || !strings.Contains(res.Content, "<entry><title>A &amp; B</title></entry>") {
		t.Fatalf("XML lost structure: %q", res.Content)
	}
}

// GBK is still the de-facto charset of many Chinese sites; without decoding
// the page came back as mojibake.
func TestWebFetch_DecodesDeclaredGBK(t *testing.T) {
	decoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("<html><body><p>人民网_网上的人民日报 要闻</p></body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=gbk")
		w.Write(decoded)
	}))
	defer ts.Close()

	res := fetchURL(t, ts, "/")
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "人民网_网上的人民日报 要闻") || !utf8.ValidString(res.Content) {
		t.Fatalf("GBK page not decoded to UTF-8: %q", res.Content)
	}
}

func TestWebFetch_DecodesUndeclaredGBK(t *testing.T) {
	decoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("<html><body><p>这是一段没有声明编码的简体中文正文，用于验证自动识别。</p><p>第二段中文内容继续出现汉字。</p></body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html") // no charset, no meta
		w.Write(decoded)
	}))
	defer ts.Close()

	res := fetchURL(t, ts, "/")
	if res.IsError || !strings.Contains(res.Content, "简体中文正文") {
		t.Fatalf("undeclared GBK should be sniffed, got: %q", res.Content)
	}
}

func TestWebFetch_BinaryContentIsRejectedWithHint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n1 0 obj\x00\x01 binary"))
	}))
	defer ts.Close()

	res := fetchURL(t, ts, "/")
	if !res.IsError {
		t.Fatalf("PDF must not be returned as text, got %q", res.Content)
	}
	if !strings.Contains(res.Content, "application/pdf") || !strings.Contains(res.Content, "run_command") {
		t.Fatalf("error should name the content type and the way out, got %q", res.Content)
	}
}

func TestWebFetch_AntiBotChallengeIsExplained(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `<html><title>Just a moment...</title><body>Checking your browser before accessing cf-chl challenge-platform</body></html>`)
	}))
	defer ts.Close()

	res := fetchURL(t, ts, "/")
	if !res.IsError {
		t.Fatal("403 must be an error")
	}
	if !strings.Contains(res.Content, "HTTP 403") || !strings.Contains(res.Content, "[blocked]") || !strings.Contains(res.Content, "browser tool") {
		t.Fatalf("expected status + anti-bot explanation, got %q", res.Content)
	}
}

func TestWebFetch_JavaScriptOnlyPageGetsHint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><html><head><title>Uniswap Interface</title><script src="/app.js"></script></head><body><div id="root"></div><noscript>You need JavaScript</noscript></body></html>`)
	}))
	defer ts.Close()

	res := fetchURL(t, ts, "/")
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "JavaScript") || !strings.Contains(res.Content, "browser tool") {
		t.Fatalf("SPA shell should carry the JS-rendered hint, got %q", res.Content)
	}
}

// Code indentation inside <pre> must survive: stripHTML trims every line.
func TestWebFetch_PreservesPreIndentation(t *testing.T) {
	page := `<html><body><article><p>Example:</p><pre><code>func main() {
    if true {
        println("hi")
    }
}</code></pre></article></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, page) }))
	defer ts.Close()

	res := fetchURL(t, ts, "/")
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "\n    if true {\n        println(\"hi\")") {
		t.Fatalf("pre indentation lost: %q", res.Content)
	}
}

func TestWebFetch_JoinsAllArticlesOnIndexPages(t *testing.T) {
	page := `<html><body><main>` +
		`<article><h2>First post</h2><p>` + strings.Repeat("alpha ", 20) + `</p></article>` +
		`<article><h2>Second post</h2><p>` + strings.Repeat("beta ", 20) + `</p></article>` +
		`<article><h2>Third post</h2><p>` + strings.Repeat("gamma ", 20) + `</p></article>` +
		`</main></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, page) }))
	defer ts.Close()

	res := fetchURL(t, ts, "/")
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	for _, want := range []string{"First post", "Second post", "Third post"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("expected %q in extracted text, got %q", want, res.Content)
		}
	}
}

func TestWebFetch_ErrorBodyDropsSVGIdeographs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `<html><body><h1>Not Found</h1><svg width="10" height="10"><path d="M729.872 487.327a50.86"/></svg><svg viewBox="0 0 1 1"/><p>back home</p></body></html>`)
	}))
	defer ts.Close()

	res := fetchURL(t, ts, "/")
	if !res.IsError || !strings.Contains(res.Content, "Not Found") || !strings.Contains(res.Content, "back home") {
		t.Fatalf("unexpected error result: %q", res.Content)
	}
	if strings.Contains(res.Content, "M729.872") || strings.Contains(res.Content, "<path") {
		t.Fatalf("SVG paths leaked into the error body: %q", res.Content)
	}
}

// A connection dropped before any response byte is retried once.
func TestWebFetch_RetriesTransientConnectionDrop(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		fmt.Fprint(w, "<html><body><p>second attempt works</p></body></html>")
	}))
	defer ts.Close()

	res := fetchURL(t, ts, "/")
	if res.IsError || !strings.Contains(res.Content, "second attempt works") {
		t.Fatalf("expected the retried request to succeed, got %q", res.Content)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected exactly 2 attempts, got %d", hits.Load())
	}
}

// Sites that 403 a "browser" UA without a browser TLS fingerprint (w3.org
// does) get one retry with the plain tool identity, which is remembered
// per host so later fetches skip the doomed first attempt.
func TestWebFetch_FallsBackToPlainUAWhenBrowserUAIssRefused(t *testing.T) {
	resetFetchUAPreference()
	t.Cleanup(resetFetchUAPreference)
	var uas []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uas = append(uas, r.Header.Get("User-Agent"))
		if strings.Contains(r.Header.Get("User-Agent"), "Mozilla/5.0") {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, "<html><body>Access denied</body></html>")
			return
		}
		fmt.Fprint(w, "<html><body><p>served to the tool identity</p></body></html>")
	}))
	defer ts.Close()

	res := fetchURL(t, ts, "/")
	if res.IsError || !strings.Contains(res.Content, "served to the tool identity") {
		t.Fatalf("expected the plain-UA retry to succeed, got %q", res.Content)
	}
	if len(uas) != 2 {
		t.Fatalf("expected browser attempt + plain retry, got %v", uas)
	}
	// Second fetch to the same host goes straight to the identity that worked.
	res = fetchURL(t, ts, "/again")
	if res.IsError || len(uas) != 3 || strings.Contains(uas[2], "Mozilla/5.0") {
		t.Fatalf("host preference not remembered, uas=%v res=%q", uas, res.Content)
	}
}

func TestWebFetch_BothIdentitiesRefusedKeepsAntiBotExplanation(t *testing.T) {
	resetFetchUAPreference()
	t.Cleanup(resetFetchUAPreference)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "<html><body>captcha required</body></html>")
	}))
	defer ts.Close()
	res := fetchURL(t, ts, "/")
	if !res.IsError || !strings.Contains(res.Content, "[blocked]") {
		t.Fatalf("expected a blocked explanation when both identities fail, got %q", res.Content)
	}
	if preferredFetchUAMode(strings.TrimPrefix(ts.URL, "http://")) != uaBrowser {
		t.Fatal("no identity should be remembered when both are refused")
	}
}

func TestWebFetch_SendsBrowserHeaders(t *testing.T) {
	var gotUA, gotAccept, gotLang string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept, gotLang = r.Header.Get("User-Agent"), r.Header.Get("Accept"), r.Header.Get("Accept-Language")
		fmt.Fprint(w, "<html><body><p>ok</p></body></html>")
	}))
	defer ts.Close()

	fetchURL(t, ts, "/")
	if !strings.Contains(gotUA, "Mozilla/5.0") {
		t.Errorf("expected browser UA, got %q", gotUA)
	}
	if !strings.Contains(gotAccept, "text/html") {
		t.Errorf("expected an Accept header, got %q", gotAccept)
	}
	if gotLang == "" {
		t.Error("expected an Accept-Language header")
	}
}

func TestWebFetch_RejectsNonHTTPScheme(t *testing.T) {
	res, _ := WebFetch{}.Execute(context.Background(), json.RawMessage(`{"url":"file:///etc/passwd","description":"t"}`))
	if !res.IsError || !strings.Contains(res.Content, "scheme") {
		t.Fatalf("file:// must be rejected up front, got %+v", res)
	}
}

func TestClassifyBody(t *testing.T) {
	cases := []struct {
		ct   string
		body string
		want bodyKind
	}{
		{"text/html; charset=utf-8", "<p>x</p>", bodyHTML},
		{"application/xhtml+xml", "<p>x</p>", bodyHTML},
		{"application/json", "{}", bodyText},
		{"application/vnd.api+json", "{}", bodyText},
		{"application/rss+xml", "<rss/>", bodyText},
		{"text/markdown", "# h", bodyText},
		{"application/pdf", "%PDF", bodyBinary},
		{"image/png", "\x89PNG", bodyBinary},
		{"", "<html><body>sniffed html</body></html>", bodyHTML},
		{"application/octet-stream", "plain text payload", bodyText},
		{"application/octet-stream", "\x00\x01\x02binary", bodyBinary},
	}
	for _, c := range cases {
		if got := classifyBody(c.ct, []byte(c.body)); got != c.want {
			t.Errorf("classifyBody(%q) = %v, want %v", c.ct, got, c.want)
		}
	}
}

func TestTruncateFetchText_RuneSafe(t *testing.T) {
	text := strings.Repeat("汉", 25000)
	out := truncateFetchText(text, maxWebFetchChars)
	if !utf8.ValidString(out) {
		t.Fatal("truncated output is not valid UTF-8")
	}
	shown := utf8.RuneCountInString(strings.Split(out, "\n\n... [truncated:")[0])
	if shown != maxWebFetchChars {
		t.Fatalf("expected exactly %d runes shown, got %d", maxWebFetchChars, shown)
	}
	if got := truncateFetchText("short", 10); got != "short" {
		t.Fatalf("short text must pass through, got %q", got)
	}
}
