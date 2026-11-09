package tunnel

import "testing"

// 钉测契约(webui share 弹层"扫码连接后自动隐藏"依赖):
// ConnectedState 读 OnConnected 缓存态——未连过 nil 安全,缓存后返回副本。
func TestConnectedState(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	if cs := sess.ConnectedState(); cs != nil {
		t.Fatalf("fresh session ConnectedState should be nil, got %+v", cs)
	}

	// 模拟 relay 接入缓存(OnConnected 走 cachedConnState 通道)
	sess.mu.Lock()
	sess.cachedConnState = &RelayConnectedState{Role: "viewer", SessionID: "s1", ProtocolVersion: 1}
	sess.mu.Unlock()

	cs := sess.ConnectedState()
	if cs == nil || cs.Role != "viewer" || cs.SessionID != "s1" {
		t.Fatalf("ConnectedState = %+v, want viewer/s1", cs)
	}

	// 副本语义:改返回值不得污染内部缓存
	cs.Role = "mutated"
	if again := sess.ConnectedState(); again.Role != "viewer" {
		t.Fatalf("ConnectedState must return a copy, internal state mutated: %+v", again)
	}
}
