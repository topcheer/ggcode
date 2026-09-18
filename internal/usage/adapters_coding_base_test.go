package usage

// Every coding-plan / anthropic-compatible base shape from
// ggcode.example.yaml must normalize to the correct probe endpoint path.
// These are regression pins for the 2026-09-18 "unsupported" panel
// reports (zhipu coding plan, kimi coding plan, double-/v1 bases).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newPathRecorder returns a server that answers every request with body
// and records the last request path.
func newPathRecorder(t *testing.T, body string) (*httptest.Server, *string) {
	t.Helper()
	path := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &path
}

// hostOf rewrites a fixed host onto the loopback test server so
// MatchesURL-based resolution is exercised with realistic base URLs.
// The probes take baseURL directly, so we call Fetch with the transformed
// base and assert the exact request path.

func TestZaiCodingPlanBase(t *testing.T) {
	srv, path := newPathRecorder(t, `{"code":200,"msg":"ok","data":{"limits":[{"type":"TIME_LIMIT","unit":5,"number":1,"usage":4000,"currentValue":500,"remaining":3500,"percentage":25,"nextResetTime":1789831644997},{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":60,"nextResetTime":1789738694871}]},"success":true}`)
	for _, base := range []string{
		srv.URL + "/api/coding/paas/v4",
		srv.URL + "/api/paas/v4",
		srv.URL + "/api/anthropic",
		srv.URL,
	} {
		zp := ZaiProbe{}
		if _, err := zp.Fetch(context.Background(), base, "k"); err != nil {
			t.Fatalf("base %s: %v", base, err)
		}
		if *path != "/api/monitor/usage/quota/limit" {
			t.Fatalf("base %s: got path %s", base, *path)
		}
	}
}

func TestZaiMatchesIntlHost(t *testing.T) {
	zai := ZaiProbe{}
	// Coding-plan entrances claim; the shared metered base does NOT
	// (open.bigmodel.cn hosts both /api/paas/v4 and /api/coding/paas/v4,
	// and only the plan carries window quotas).
	if !zai.MatchesURL("https://api.z.ai/api/coding/paas/v4") {
		t.Fatalf("api.z.ai coding path must resolve to the zai probe")
	}
	if !zai.MatchesURL("https://open.bigmodel.cn/api/coding/paas/v4") {
		t.Fatalf("open.bigmodel.cn coding path must resolve to the zai probe")
	}
	if zai.MatchesURL("https://open.bigmodel.cn/api/paas/v4") {
		t.Fatalf("metered /api/paas/v4 must NOT claim the coding-plan probe")
	}
}

func TestKimiCodingBaseUsesUsagesWithoutDoubleV1(t *testing.T) {
	srv, path := newPathRecorder(t, `{"usage":{"limit":"100","used":"7","remaining":"93","resetTime":"2026-09-22T12:53:21Z"},"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":"100","used":"1","remaining":"99","resetTime":"2026-09-18T13:53:21Z"}}],"usages":{"limit_5h":{"used_ratio":0.005,"reset_time":"2026-09-18T13:53:19Z"},"limit_7d":{"used_ratio":0.067,"reset_time":"2026-09-22T12:53:20Z"}}}`)
	kf := KimiProbe{}
	if _, err := kf.Fetch(context.Background(), srv.URL+"/coding/v1", "sk-kimi-x"); err != nil {
		t.Fatalf("coding base: %v", err)
	}
	if *path != "/coding/v1/usages" {
		t.Fatalf("coding base must hit the constant /coding/v1/usages path, got %s", *path)
	}
	// Bare host normalizes to the same constant path.
	if _, err := kf.Fetch(context.Background(), srv.URL, "k"); err != nil {
		t.Fatalf("bare base: %v", err)
	}
	if *path != "/coding/v1/usages" {
		t.Fatalf("bare base must hit /coding/v1/usages too, got %s", *path)
	}
}

func TestKimiMatchesCodingHost(t *testing.T) {
	kp := KimiProbe{}
	if !kp.MatchesURL("https://api.kimi.com/coding/v1") {
		t.Fatalf("api.kimi.com must resolve to the kimi probe (coding plan host)")
	}
	if kp.MatchesURL("api.moonshot.cn") {
		t.Fatalf("moonshot host must stay with the moonshot probe (single-owner)")
	}
}

func TestDeepSeekBaseStripsV1(t *testing.T) {
	srv, path := newPathRecorder(t, `{"balance_infos":[{"currency":"USD","total_balance":5}]}`)
	dp := DeepSeekProbe{}
	if _, err := dp.Fetch(context.Background(), srv.URL+"/v1", "k"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if *path != "/user/balance" {
		t.Fatalf("balance must be requested at root /user/balance, got %s", *path)
	}
}

func TestMoonshotBaseStripsV1(t *testing.T) {
	srv, path := newPathRecorder(t, `{"data":{"available_balance":5}}`)
	mp := MoonshotProbe{}
	if _, err := mp.Fetch(context.Background(), srv.URL+"/v1", "k"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if *path != "/v1/users/me/balance" {
		t.Fatalf("expected /v1/users/me/balance at root, got %s", *path)
	}
}

func TestMinimaxBaseStripsV1AndAnthropic(t *testing.T) {
	srv, path := newPathRecorder(t, `{"model_remains":[{"general":{"five_hour_remain_percent":25,"weekly_remain_percent":80}}]}`)
	for _, base := range []string{srv.URL + "/v1", srv.URL + "/anthropic", srv.URL} {
		mmp := MinimaxProbe{}
		if _, err := mmp.Fetch(context.Background(), base, "k"); err != nil {
			t.Fatalf("base %s: %v", base, err)
		}
		if *path != "/v1/api/openplatform/coding_plan/remains" {
			t.Fatalf("base %s: got path %s", base, *path)
		}
	}
}

func TestOpenrouterBaseStripsAPIV1(t *testing.T) {
	srv, path := newPathRecorder(t, `{"data":{"total_credits":10,"total_usage":2}}`)
	op := OpenrouterProbe{}
	if _, err := op.Fetch(context.Background(), srv.URL+"/api/v1", "k"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if *path != "/api/v1/credits" {
		t.Fatalf("expected /api/v1/credits at root, got %s", *path)
	}
}
