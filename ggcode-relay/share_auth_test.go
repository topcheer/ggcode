package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestIssueShareSession(t *testing.T) {
	cfg := shareAuthConfig{
		Secret:     "relay-secret",
		ConnectTTL: time.Minute,
		RenewTTL:   time.Hour,
	}
	issued, err := issueShareSession(cfg, shareProtocolV3)
	if err != nil {
		t.Fatal(err)
	}
	if issued.ProtocolVersion != shareProtocolV3 || issued.ShareMode != shareModeV3 || issued.RoomID == "" {
		t.Fatalf("unexpected issued session: %+v", issued)
	}
	serverClaims, err := verifyShareTicket(cfg.Secret, issued.ServerAuthTicket)
	if err != nil {
		t.Fatal(err)
	}
	if serverClaims.Role != "server" || serverClaims.Kind != shareTicketKindConnect || serverClaims.RoomID != issued.RoomID {
		t.Fatalf("unexpected server claims: %+v", serverClaims)
	}
	clientClaims, err := verifyShareTicket(cfg.Secret, issued.ClientAuthTicket)
	if err != nil {
		t.Fatal(err)
	}
	if clientClaims.Role != "client" || clientClaims.Kind != shareTicketKindConnect || clientClaims.RoomID != issued.RoomID {
		t.Fatalf("unexpected client claims: %+v", clientClaims)
	}
	renewClaims, err := verifyShareTicket(cfg.Secret, issued.ServerRenewToken)
	if err != nil {
		t.Fatal(err)
	}
	if renewClaims.Role != "server" || renewClaims.Kind != shareTicketKindRenew || renewClaims.RoomID != issued.RoomID {
		t.Fatalf("unexpected renew claims: %+v", renewClaims)
	}
}

func TestValidateShareHandshakeAcceptsIssuedTickets(t *testing.T) {
	cfg := shareAuthConfig{
		Secret:     "relay-secret",
		ConnectTTL: time.Minute,
		RenewTTL:   time.Hour,
	}
	issued, err := issueShareSession(cfg, shareProtocolV3)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ws?role=server&proto=3&room_id="+issued.RoomID+"&auth_ticket="+issued.ServerAuthTicket+"&caps="+requiredTunnelCapability+"&crypto_key=abc123&kx_pub=server-pub", nil)
	handshake, status, reason := validateShareHandshake(req, cfg)
	if handshake == nil || status != http.StatusSwitchingProtocols || reason != "" {
		t.Fatalf("unexpected handshake: handshake=%+v status=%d reason=%q", handshake, status, reason)
	}
	if handshake.roomKey != issued.RoomID || handshake.shareMode != shareModeV3 || handshake.renewToken == "" {
		t.Fatalf("unexpected handshake contents: %+v", handshake)
	}
}

func TestConnectedShareMetadataIncludesV3ServerPublicKey(t *testing.T) {
	metadata := connectedShareMetadata(&shareHandshake{
		protocolVersion: shareProtocolV3,
		shareMode:       shareModeV3,
		connectMode:     shareTicketKindConnect,
		roomKey:         "room-3",
		serverPublicKey: "server-pub",
	})
	if metadata["kx_pub"] != "server-pub" {
		t.Fatalf("expected kx_pub in connected metadata, got %+v", metadata)
	}
}

func TestValidateShareHandshakeRejectsIssuedTicketScopeMismatch(t *testing.T) {
	cfg := shareAuthConfig{
		Secret:     "relay-secret",
		ConnectTTL: time.Minute,
		RenewTTL:   time.Hour,
	}
	issued, err := issueShareSession(cfg, shareProtocolV3)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ws?role=client&proto=3&room_id="+issued.RoomID+"&auth_ticket="+issued.ServerAuthTicket+"&caps="+requiredTunnelCapability, nil)
	handshake, status, reason := validateShareHandshake(req, cfg)
	if handshake != nil || status != http.StatusUnauthorized || reason != "ticket scope mismatch" {
		t.Fatalf("unexpected mismatch result: handshake=%+v status=%d reason=%q", handshake, status, reason)
	}
}

func TestValidateShareHandshakeRejectsLegacyProtocol(t *testing.T) {
	cfg := shareAuthConfig{
		Secret:     "relay-secret",
		ConnectTTL: time.Minute,
		RenewTTL:   time.Hour,
	}
	req := httptest.NewRequest(http.MethodGet, "/ws?role=client&token=legacy-token", nil)
	handshake, status, reason := validateShareHandshake(req, cfg)
	if handshake != nil || status != http.StatusGone || reason != shareUpgradeRequiredMessage {
		t.Fatalf("unexpected legacy result: handshake=%+v status=%d reason=%q", handshake, status, reason)
	}
}

func TestValidateShareHandshakeMissingTunnelCapabilityRequiresUpgrade(t *testing.T) {
	cfg := shareAuthConfig{
		Secret:     "relay-secret",
		ConnectTTL: time.Minute,
		RenewTTL:   time.Hour,
	}
	issued, err := issueShareSession(cfg, shareProtocolV3)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ws?role=client&proto=3&room_id="+issued.RoomID+"&auth_ticket="+issued.ClientAuthTicket+"&caps=share_v2,share_v3", nil)
	handshake, status, reason := validateShareHandshake(req, cfg)
	if handshake == nil || status != http.StatusSwitchingProtocols || reason != "" {
		t.Fatalf("unexpected capability gate result: handshake=%+v status=%d reason=%q", handshake, status, reason)
	}
	if handshake.postConnectErr != shareUpgradeRequiredMessage {
		t.Fatalf("postConnectErr = %q, want %q", handshake.postConnectErr, shareUpgradeRequiredMessage)
	}
	if handshake.notice != shareUpgradeRequiredMessage {
		t.Fatalf("notice = %q, want %q", handshake.notice, shareUpgradeRequiredMessage)
	}
}

func TestHandleShareSession(t *testing.T) {
	t.Setenv(shareSecretEnv, "relay-secret")
	h := newHub(nil)
	req := httptest.NewRequest(http.MethodPost, "/share/session", nil)
	rec := httptest.NewRecorder()

	h.handleShareSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var issued issuedShareSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if issued.RoomID == "" || issued.ServerAuthTicket == "" || issued.ClientAuthTicket == "" {
		t.Fatalf("unexpected issued response: %+v", issued)
	}
	h.mu.RLock()
	room := h.rooms[issued.RoomID]
	h.mu.RUnlock()
	if room == nil {
		t.Fatal("issued share session should reserve a pending room")
	}
	room.mu.RLock()
	retained := room.offlineTimer != nil
	room.mu.RUnlock()
	if !retained {
		t.Fatal("issued share session should arm a pending room timer")
	}
}

func TestHandleShareSessionV3(t *testing.T) {
	t.Setenv(shareSecretEnv, "relay-secret")
	h := newHub(nil)
	req := httptest.NewRequest(http.MethodPost, "/share/session?proto=3", nil)
	rec := httptest.NewRecorder()

	h.handleShareSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var issued issuedShareSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if issued.ProtocolVersion != shareProtocolV3 || issued.ShareMode != shareModeV3 {
		t.Fatalf("unexpected issued v3 response: %+v", issued)
	}
}

func TestRefreshShareSession(t *testing.T) {
	cfg := shareAuthConfig{
		Secret:     "relay-secret",
		ConnectTTL: time.Minute,
		RenewTTL:   time.Hour,
	}
	issued, err := issueShareSession(cfg, shareProtocolV3)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := refreshShareSession(cfg, refreshShareSessionRequest{
		RoomID:           issued.RoomID,
		ServerRenewToken: issued.ServerRenewToken,
	}, shareProtocolV3, shareModeV3)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.RoomID != issued.RoomID || refreshed.ClientAuthTicket == "" || refreshed.ServerRenewToken == "" {
		t.Fatalf("unexpected refreshed response: %+v", refreshed)
	}
	clientClaims, err := verifyShareTicket(cfg.Secret, refreshed.ClientAuthTicket)
	if err != nil {
		t.Fatal(err)
	}
	if clientClaims.Role != "client" || clientClaims.Kind != shareTicketKindConnect || clientClaims.RoomID != issued.RoomID {
		t.Fatalf("unexpected refreshed client claims: %+v", clientClaims)
	}
	renewClaims, err := verifyShareTicket(cfg.Secret, refreshed.ServerRenewToken)
	if err != nil {
		t.Fatal(err)
	}
	if renewClaims.Role != "server" || renewClaims.Kind != shareTicketKindRenew || renewClaims.RoomID != issued.RoomID {
		t.Fatalf("unexpected refreshed renew claims: %+v", renewClaims)
	}
}

func TestHandleRefreshShareSession(t *testing.T) {
	t.Setenv(shareSecretEnv, "relay-secret")
	h := newHub(nil)
	issued, err := issueShareSession(loadShareAuthConfig(), shareProtocolV3)
	if err != nil {
		t.Fatal(err)
	}
	room := h.reserveIssuedRoom(issued.RoomID, defaultPendingRoomTTL)
	room.mu.Lock()
	room.server = &peer{role: "server", room: room}
	room.protocolVersion = shareProtocolV3
	room.mu.Unlock()

	body, err := json.Marshal(refreshShareSessionRequest{
		RoomID:           issued.RoomID,
		ServerRenewToken: issued.ServerRenewToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/share/session/refresh", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.handleRefreshShareSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var refreshed refreshedShareSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.RoomID != issued.RoomID || refreshed.ClientAuthTicket == "" || refreshed.ServerRenewToken == "" {
		t.Fatalf("unexpected refreshed response: %+v", refreshed)
	}
	if refreshed.ProtocolVersion != shareProtocolV3 || refreshed.ShareMode != shareModeV3 {
		t.Fatalf("unexpected refreshed metadata: %+v", refreshed)
	}
}

func TestHandleRefreshShareSessionRejectsMissingRoom(t *testing.T) {
	t.Setenv(shareSecretEnv, "relay-secret")
	cfg := loadShareAuthConfig()
	issued, err := issueShareSession(cfg, shareProtocolV3)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(refreshShareSessionRequest{
		RoomID:           issued.RoomID,
		ServerRenewToken: issued.ServerRenewToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/share/session/refresh", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	newHub(nil).handleRefreshShareSession(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusGone)
	}
}

func TestHandleWSPendingIssuedRoomReturnsServerOffline(t *testing.T) {
	t.Setenv(shareSecretEnv, "relay-secret")
	h := newHub(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/share/session", h.handleShareSession)
	mux.HandleFunc("/ws", h.handleWS)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/share/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share session status = %d", resp.StatusCode)
	}
	var issued issuedShareSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) +
		"/ws?role=client&proto=3&room_id=" + issued.RoomID +
		"&auth_ticket=" + issued.ClientAuthTicket +
		"&caps=" + requiredTunnelCapability
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var msg relayMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "server_offline" {
		t.Fatalf("expected server_offline, got %+v", msg)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["state"] != "pending" {
		t.Fatalf("expected pending state, got %+v", data)
	}
	if data["retry_after_ms"] == nil {
		t.Fatalf("expected retry_after_ms in server_offline data, got %+v", data)
	}
	h.mu.RLock()
	room := h.rooms[issued.RoomID]
	h.mu.RUnlock()
	if room == nil {
		t.Fatal("pending room should remain reserved after client wait notice")
	}
}

func TestHandleWSMissingTunnelCapabilityClientBehavesLikeNormalV3Client(t *testing.T) {
	t.Setenv(shareSecretEnv, "relay-secret")
	h := newHub(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/share/session", h.handleShareSession)
	mux.HandleFunc("/ws", h.handleWS)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/share/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share session status = %d", resp.StatusCode)
	}
	var issued issuedShareSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}

	serverURL := strings.Replace(server.URL, "http://", "ws://", 1) +
		"/ws?role=server&proto=3&room_id=" + issued.RoomID +
		"&auth_ticket=" + issued.ServerAuthTicket +
		"&caps=" + requiredTunnelCapability +
		"&crypto_key=test-crypto"
	serverConn, _, err := websocket.DefaultDialer.Dial(serverURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()
	if err := serverConn.ReadJSON(&relayMessage{}); err != nil {
		t.Fatal(err)
	}

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) +
		"/ws?role=client&proto=3&room_id=" + issued.RoomID +
		"&auth_ticket=" + issued.ClientAuthTicket +
		"&caps=share_v2,share_v3"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var msg relayMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "error" || msg.Reason != shareUpgradeRequiredMessage {
		t.Fatalf("expected upgrade error, got %+v", msg)
	}
}

func TestHandleWSMissingTunnelCapabilityServerDoesNotPoisonRoom(t *testing.T) {
	t.Setenv(shareSecretEnv, "relay-secret")
	h := newHub(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/share/session", h.handleShareSession)
	mux.HandleFunc("/ws", h.handleWS)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/share/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share session status = %d", resp.StatusCode)
	}
	var issued issuedShareSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}

	serverURL := strings.Replace(server.URL, "http://", "ws://", 1) +
		"/ws?role=server&proto=3&room_id=" + issued.RoomID +
		"&auth_ticket=" + issued.ServerAuthTicket +
		"&caps=share_v2,share_v3" +
		"&crypto_key=test-crypto"
	serverConn, _, err := websocket.DefaultDialer.Dial(serverURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()
	var msg relayMessage
	if err := serverConn.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "connected" {
		t.Fatalf("expected connected, got %+v", msg)
	}

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) +
		"/ws?role=client&proto=3&room_id=" + issued.RoomID +
		"&auth_ticket=" + issued.ClientAuthTicket +
		"&caps=" + requiredTunnelCapability
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "error" || msg.Reason != shareUpgradeRequiredMessage {
		t.Fatalf("expected upgrade error, got %+v", msg)
	}
}
