package usage

// Owner ruling 2026-09-15: adapter selection is BY URL. Resolve maps a
// base URL host to the owning probe; unknown hosts resolve empty.

import "testing"

func TestResolveByURL(t *testing.T) {
	svc := DefaultService()
	cases := map[string]string{
		"https://open.bigmodel.cn/api/paas/v4": "zai",
		"https://api.deepseek.com":             "deepseek",
		"https://api.moonshot.cn/v1":           "moonshot",
		"https://api.moonshot.ai/v1":           "moonshot",
		"https://openrouter.ai/api/v1":         "openrouter",
		"https://api.anthropic.com":            "anthropic-oauth",
		"https://api.siliconflow.cn/v1":        "siliconflow",
		"https://api.minimax.io/v1":            "minimax",
		"https://gw.unknown.example/v1":        "",
		"":                                     "",
	}
	for url, want := range cases {
		if got := svc.Resolve(url); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", url, got, want)
		}
	}
}
