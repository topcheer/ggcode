package im

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// connectAndServe dials the Discord Gateway, registers the connection and
// serves gateway events until the context is cancelled, the connection
// breaks, or the gateway requests a reconnect / invalid-session restart.
//
// The lifecycle mirrors the Gateway v10 phases: resolve+dial, register
// (install conn, keep sequence for RESUME), then the per-op event loop.
func (a *discordAdapter) connectAndServe(ctx context.Context) error {
	conn, err := a.dialGateway(ctx)
	if err != nil {
		return err
	}
	defer func() {
		a.mu.Lock()
		a.connected = false
		a.ws = nil
		a.mu.Unlock()
		conn.Close()
	}()

	a.registerConn(conn)

	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	defer heartbeatCancel()

	for {
		if ctx.Err() != nil {
			return nil
		}
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read websocket: %w", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(msgBytes, &payload); err != nil {
			continue
		}
		if err := a.handleGatewayEvent(ctx, heartbeatCtx, conn, payload); err != nil {
			return err
		}
	}
}

// dialGateway resolves the gateway URL via REST and dials the websocket.
// 30s dial timeout prevents indefinite hang on unreachable Discord gateway.
func (a *discordAdapter) dialGateway(ctx context.Context) (*websocket.Conn, error) {
	gatewayURL, err := a.getGatewayBotURL(ctx)
	if err != nil {
		return nil, fmt.Errorf("get gateway URL: %w", err)
	}
	debug.Log("discord", "adapter=%s gateway URL=%s", a.name, gatewayURL)

	wsURL := gatewayURL + "?v=10&encoding=json"
	dialCtx, dialCancel := context.WithTimeout(ctx, 30*time.Second)
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, wsURL, nil)
	dialCancel()
	if err != nil {
		return nil, fmt.Errorf("dial gateway: %w", err)
	}
	return conn, nil
}

// registerConn installs the connection on the adapter. The sequence is kept
// across reconnects so RESUME (op 6) can replay from the last processed
// event; it is only cleared for a brand-new session with no stored session
// ID (#947).
func (a *discordAdapter) registerConn(conn *websocket.Conn) {
	a.mu.Lock()
	a.ws = conn
	if a.sessionID == "" {
		a.sequence = 0
	}
	a.mu.Unlock()
}

// handleGatewayEvent routes one decoded gateway payload by opcode. It
// returns a non-nil error when the serve loop must terminate (op 7
// reconnect / op 9 invalid session); every other op keeps the loop running.
func (a *discordAdapter) handleGatewayEvent(ctx context.Context, heartbeatCtx context.Context, conn *websocket.Conn, payload map[string]any) error {
	if s := jsonInt(payload["s"]); s > 0 {
		a.mu.Lock()
		a.sequence = s
		a.mu.Unlock()
	}

	switch jsonInt(payload["op"]) {
	case discordOpHello:
		d, _ := payload["d"].(map[string]any)
		a.handleHello(heartbeatCtx, conn, d)

	case discordOpHeartbeatACK:
		// acknowledged

	case discordOpDispatch:
		t, _ := payload["t"].(string)
		d, _ := payload["d"].(map[string]any)
		a.handleGatewayDispatch(ctx, t, d)

	case discordOpReconnect:
		debug.Log("discord", "adapter=%s gateway requested reconnect", a.name)
		return fmt.Errorf("gateway requested reconnect")

	case discordOpInvalidSession:
		return a.handleInvalidSession(payload)
	}
	return nil
}

// handleHello processes op 10 HELLO: adopts the advertised heartbeat
// interval, starts the heartbeat loop, then resumes or identifies below.
func (a *discordAdapter) handleHello(heartbeatCtx context.Context, conn *websocket.Conn, d map[string]any) {
	interval := extractHeartbeatInterval(d)
	safego.Go("im.discord.heartbeat", func() { a.heartbeatLoop(heartbeatCtx, conn, interval) })
	a.resumeOrIdentify(conn)
}

// extractHeartbeatInterval reads heartbeat_interval from the HELLO payload,
// falling back to the Gateway v10 default of 41250ms when absent or
// non-positive.
func extractHeartbeatInterval(d map[string]any) int {
	interval := 41250
	if d != nil {
		if iv, ok := intValue(d["heartbeat_interval"]); ok && iv > 0 {
			interval = iv
		}
	}
	return interval
}

// resumeOrIdentify resumes the previous session when possible so Discord
// replays events missed during the disconnect window; otherwise IDENTIFYs a
// new session (#947, Gateway v10 semantics).
func (a *discordAdapter) resumeOrIdentify(conn *websocket.Conn) {
	a.mu.RLock()
	sid, seq := a.sessionID, a.sequence
	a.mu.RUnlock()
	if sid != "" {
		debug.Log("discord", "adapter=%s sending RESUME session=%s seq=%d", a.name, sid, seq)
		a.sendResume(conn, sid, seq)
		return
	}
	a.sendIdentify(conn)
}

// handleInvalidSession processes op 9: d is a bool — true → the session is
// still resumable, keep sessionID/sequence and retry RESUME on the next
// connection; false → the session is dead, drop them so the next connection
// falls back to a fresh IDENTIFY (#947).
func (a *discordAdapter) handleInvalidSession(payload map[string]any) error {
	resumable, _ := payload["d"].(bool)
	a.mu.Lock()
	if !resumable {
		a.sessionID = ""
		a.sequence = 0
	}
	a.mu.Unlock()
	debug.Log("discord", "adapter=%s invalid session (resumable=%v)", a.name, resumable)
	return fmt.Errorf("invalid session (resumable=%v)", resumable)
}
