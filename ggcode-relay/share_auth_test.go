package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIssueShareSession(t *testing.T) {
	cfg := shareAuthConfig{
		Secret:     "relay-secret",
		ConnectTTL: time.Minute,
		RenewTTL:   time.Hour,
	}
	issued, err := issueShareSession(cfg)
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
	issued, err := issueShareSession(cfg)
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

func TestValidateShareHandshakeRejectsIssuedTicketScopeMismatch(t *testing.T) {
	cfg := shareAuthConfig{
		Secret:     "relay-secret",
		ConnectTTL: time.Minute,
		RenewTTL:   time.Hour,
	}
	issued, err := issueShareSession(cfg)
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
}
