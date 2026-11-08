package tui

import (
	"sync"
	"testing"
)

// 钉测(派单件④):/webui 在 completion 清单(SlashCommands)中
func TestWebuiSlashInCompletionList(t *testing.T) {
	found := false
	for _, c := range SlashCommands {
		if c == "/webui" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("/webui missing from SlashCommands completion list")
	}
}

// 钉测:busy 期可执行(shouldExecuteWhileBusy 认可)
func TestWebuiSlashExecutableWhileBusy(t *testing.T) {
	if !shouldExecuteWhileBusy("/webui") {
		t.Fatal("/webui should be executable while busy")
	}
}

// 钉测:就绪消息写入 webuiURL(handleWebuiReadyMsg → /webui 消费链)
func TestWebuiURLStoredOnReady(t *testing.T) {
	m := Model{sessionMu: &sync.Mutex{}}
	got, _ := m.handleWebuiReadyMsg(webuiReadyMsg{Addr: "127.0.0.1:9999", Token: "tok"})
	nm, ok := got.(Model)
	if !ok {
		t.Fatalf("handleWebuiReadyMsg returned %T", got)
	}
	if nm.webuiURL != "http://127.0.0.1:9999#token=tok" {
		t.Fatalf("webuiURL = %q", nm.webuiURL)
	}
}

// 钉测(WS token 修复回归):/webui 唤起必须带完整 URL 含 #token——
// 新开标签页没有 sessionStorage,剥掉 hash 会导致 token 拿不到→WS 握手 401 循环
func TestWebuiSlashOpensFullURLWithToken(t *testing.T) {
	var opened string
	m := Model{
		sessionMu: &sync.Mutex{},
		webuiURL:  "http://127.0.0.1:9999#token=tok",
		urlOpener: func(u string) error { opened = u; return nil },
	}
	m.handleCommandWithDisplay("/webui", true)
	if opened != "http://127.0.0.1:9999#token=tok" {
		t.Fatalf("urlOpener got %q, want full URL including #token fragment", opened)
	}
}
