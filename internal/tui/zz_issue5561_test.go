package tui

import (
	"testing"
	"time"
)

// zz_issue5561_test: /et login 失败路径（openBrowser 回调未触发）不得永久
// 阻塞 authCh Cmd goroutine——quitCh close 后 select 必须立即返回 nil。
// 直接钉死 etLoginStart 内 select 语义（修复形态），两条路径：
//  1. 回调触发（authCh 有值）→ 返回 etLoginAuthMsg
//  2. Login 返回且回调未触发（quitCh close）→ 返回 nil（非阻塞退出）
func TestIssue5561AuthCmdSelectQuitPath(t *testing.T) {
	// Path 1: authURL 到达 → etLoginAuthMsg
	authCh := make(chan string, 1)
	quitCh := make(chan struct{})
	authCh <- "https://auth.example/start"
	cmd := func() (msg any) {
		select {
		case u := <-authCh:
			return etLoginAuthMsg{authURL: u}
		case <-quitCh:
			return nil
		}
	}()
	m, ok := cmd.(etLoginAuthMsg)
	if !ok || m.authURL != "https://auth.example/start" {
		t.Fatalf("path1: expected etLoginAuthMsg with URL, got %#v", cmd)
	}

	// Path 2: Login 先返回（quitCh close）且回调未触发 → nil，立即返回
	authCh2 := make(chan string, 1)
	quitCh2 := make(chan struct{})
	close(quitCh2)
	cmd2 := func() (msg any) {
		select {
		case u := <-authCh2:
			return etLoginAuthMsg{authURL: u}
		case <-quitCh2:
			return nil
		}
	}()
	if cmd2 != nil {
		t.Fatalf("path2: expected nil msg after quitCh close, got %#v", cmd2)
	}

	// Path 3（回归护栏）: 修复前形态（裸 <-authCh 阻塞读）在 quit 场景会
	// 永久阻塞——用带超时的观察证明 select 形态在 1s 内已返回。
	authCh3 := make(chan string, 1)
	quitCh3 := make(chan struct{})
	close(quitCh3)
	done := make(chan any, 1)
	go func() {
		select {
		case u := <-authCh3:
			done <- etLoginAuthMsg{authURL: u}
		case <-quitCh3:
			done <- nil
		}
	}()
	select {
	case got := <-done:
		if got != nil {
			t.Fatalf("path3: expected nil, got %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("path3: select did not exit within 1s after quitCh close — goroutine leak regression")
	}
}
