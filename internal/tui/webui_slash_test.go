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
