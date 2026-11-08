package webui

import (
	"os"
	"strings"
	"testing"
)

// #5299 mention 回归钉:拾取必须以触发 @ 的位置为锚做替换插入(splice),
// 消费掉触发 @ 与残留 query,不得末尾 append(曾必产 @@file: 畸形 token,
// 自 P1.5 合入即 100% 不可用)。onPick 契约与 MentionPopover 的 @ 前缀 token
// 互相咬合:弹层给 token 带前导 @,则 Composer 必须吃掉触发性 @。

func TestIssue5299MentionSpliceContract(t *testing.T) {
	data, err := os.ReadFile("../../web/src/chat/Composer.tsx")
	if err != nil {
		t.Fatalf("Composer.tsx not readable: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, "at: number") {
		t.Error("#5299: mention state must record the trigger @ offset (at: number) — splice anchor")
	}
	if !strings.Contains(src, "setMention({ query: upto.slice(at + 1), at,") {
		t.Error("#5299: onChange must store the trigger offset alongside the query")
	}
	splice := "v.slice(0, m.at) + text + v.slice(m.at + 1 + m.query.length)"
	if !strings.Contains(src, splice) {
		t.Errorf("#5299: onPick must splice at the trigger offset (want %q) instead of appending — appending produced @@file: malformed tokens", splice)
	}
	if strings.Contains(src, "setValue((v) => v + text)") {
		t.Error("#5299: the old end-append onPick must be gone (it consumed neither the trigger @ nor the query)")
	}
	if !strings.Contains(src, "setSelectionRange(pos, pos)") {
		t.Error("#5299: cursor must be restored into the spliced token (setSelectionRange after pick)")
	}
}

func TestIssue5299MentionSpliceSemantics(t *testing.T) {
	// 与 Composer.tsx onPick 逐字对齐的语义钉:证明 splice 在 filed 场景下
	// 产出良构 token,而旧 append 逻辑产出 issue 正文所录的畸形串。
	pick := func(v, text string, at int, query string) string {
		return v[:at] + text + v[at+1+len(query):]
	}
	appendOnly := func(v, text string) string { return v + text }
	cases := []struct{ name, v, text, want string }{
		{"suggestion-click 看一下@", "看一下@", "@file:", "看一下@file:"},
		{"enter-confirm check @", "check @", "@file:main.go ", "check @file:main.go "},
		{"query-residue a@fi", "a@fi", "@file:", "a@file:"},
		{"mid-text hello @wo world", "hello @wo world", "@skill:", "hello @skill: world"},
	}
	for _, c := range cases {
		// at 与组件内 upto.lastIndexOf("@") 同构(各语言索引语义自洽:
		// JS UTF-16 码元 / Go UTF-8 字节),不采用手写索引以免 CJK 多字节踩坑。
		at := strings.LastIndex(c.v, "@")
		if at < 0 {
			t.Fatalf("%s: no trigger @ in %q — fixture invalid", c.name, c.v)
		}
		query := c.v[at+1:]
		if i := strings.IndexAny(query, " \t"); i >= 0 {
			query = query[:i]
		}
		if got := pick(c.v, c.text, at, query); got != c.want {
			t.Errorf("%s: splice = %q, want %q", c.name, got, c.want)
		}
		if got := appendOnly(c.v, c.text); got == c.want {
			t.Errorf("%s: old append unexpectedly produced well-formed %q — pin is stale", c.name, got)
		}
	}
}

func TestIssue5299DistCarriesSplice(t *testing.T) {
	// dist 是嵌入 webui 的真实交付物:提交的 bundle 必须携带 splice 修复。
	entries, err := os.ReadDir("../../internal/webui/dist/assets")
	if err != nil {
		t.Fatalf("dist/assets not readable: %v", err)
	}
	var bundle string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".js") && strings.Contains(e.Name(), "index-") {
			bundle = e.Name()
			break
		}
	}
	if bundle == "" {
		t.Fatal("no committed index-*.js bundle found in dist/assets")
	}
	raw, err := os.ReadFile("../../internal/webui/dist/assets/" + bundle)
	if err != nil {
		t.Fatalf("bundle %s not readable: %v", bundle, err)
	}
	js := string(raw)
	if !strings.Contains(js, ".slice(0,") || !strings.Contains(js, "setSelectionRange") {
		t.Errorf("committed dist bundle %s must carry the #5299 splice + cursor restore", bundle)
	}
}
