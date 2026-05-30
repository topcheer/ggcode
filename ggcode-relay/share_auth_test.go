package main

import (
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
	issued, err := issueShareSession(cfg, shareProtocolV2)
	if err != nil {
		t.Fatal(err)
	}
	if issued.ProtocolVersion != shareProtocolV2 || issued.ShareMode != shareModeV2 || issued.RoomID == "" {
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
	issued, err := issueShareSession(cfg, shareProtocolV2)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ws?role=server&proto=2&room_id="+issued.RoomID+"&auth_ticket="+issued.ServerAuthTicket+"&crypto_key=abc123", nil)
	handshake, status, reason := validateShareHandshake(req, cfg)
	if handshake == nil || status != http.StatusSwitchingProtocols || reason != "" {
		t.Fatalf("unexpected handshake: handshake=%+v status=%d reason=%q", handshake, status, reason)
	}
	if handshake.roomKey != issued.RoomID || handshake.shareMode != shareModeV2 || handshake.renewToken == "" {
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
	issued, err := issueShareSession(cfg, shareProtocolV2)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ws?role=client&proto=2&room_id="+issued.RoomID+"&auth_ticket="+issued.ServerAuthTicket, nil)
	handshake, status, reason := validateShareHandshake(req, cfg)
	if handshake != nil || status != http.StatusUnauthorized || reason != "ticket scope mismatch" {
		t.Fatalf("unexpected mismatch result: handshake=%+v status=%d reason=%q", handshake, status, reason)
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
		"/ws?role=client&proto=2&room_id=" + issued.RoomID +
		"&auth_ticket=" + issued.ClientAuthTicket
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
