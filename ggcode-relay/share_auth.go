package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	shareProtocolLegacy          = 1
	shareProtocolV2              = 2
	shareProtocolV3              = 3
	requiredShareProtocolVersion = shareProtocolV3
	requiredTunnelCapability     = "tunnel_messages_v1"

	shareModeLegacy = "legacy"
	shareModeV2     = "v2"
	shareModeV3     = "v3"
	shareModeCompat = "compat"

	shareTicketKindConnect = "connect"
	shareTicketKindRenew   = "renew"

	shareProtocolEnv   = "GGCODE_SHARE_PROTOCOL"
	shareSecretEnv     = "GGCODE_SHARE_V2_SECRET"
	shareConnectTTLEnv = "GGCODE_SHARE_V2_CONNECT_TTL"
	shareRenewTTLEnv   = "GGCODE_SHARE_V2_RENEW_TTL"
)

const (
	defaultShareConnectTTL = 15 * time.Minute
	defaultShareRenewTTL   = 30 * 24 * time.Hour
)

const shareUpgradeRequiredMessage = "GGCode share v3 is required. Please upgrade GGCode TUI/GUI/Mobile to the latest version."

var errShareUpgradeRequired = errors.New(shareUpgradeRequiredMessage)

type shareAuthConfig struct {
	Secret     string
	ConnectTTL time.Duration
	RenewTTL   time.Duration
}

type shareTicketClaims struct {
	RoomID string `json:"room_id"`
	Role   string `json:"role"`
	Kind   string `json:"kind"`
	Exp    int64  `json:"exp"`
	V      int    `json:"v"`
}

type issuedShareSessionResponse struct {
	ProtocolVersion  int    `json:"protocol_version"`
	ShareMode        string `json:"share_mode"`
	RoomID           string `json:"room_id"`
	ServerAuthTicket string `json:"server_auth_ticket"`
	ClientAuthTicket string `json:"client_auth_ticket"`
	ServerRenewToken string `json:"server_renew_token,omitempty"`
	AuthExpiresAt    string `json:"auth_expires_at,omitempty"`
	RenewExpiresAt   string `json:"renew_expires_at,omitempty"`
	Notice           string `json:"notice,omitempty"`
}

type shareHandshake struct {
	role            string
	roomKey         string
	protocolVersion int
	shareMode       string
	connectMode     string
	postConnectErr  string
	authExpiresAt   time.Time
	renewToken      string
	renewExpiresAt  time.Time
	notice          string
	clientKind      string
	clientVersion   string
	capabilities    []string
	cryptoKey       string
	serverPublicKey string
}

func loadShareAuthConfig() shareAuthConfig {
	cfg := shareAuthConfig{
		Secret:     strings.TrimSpace(os.Getenv(shareSecretEnv)),
		ConnectTTL: defaultShareConnectTTL,
		RenewTTL:   defaultShareRenewTTL,
	}
	if ttl := strings.TrimSpace(os.Getenv(shareConnectTTLEnv)); ttl != "" {
		if parsed, err := time.ParseDuration(ttl); err == nil && parsed > 0 {
			cfg.ConnectTTL = parsed
		}
	}
	if ttl := strings.TrimSpace(os.Getenv(shareRenewTTLEnv)); ttl != "" {
		if parsed, err := time.ParseDuration(ttl); err == nil && parsed > 0 {
			cfg.RenewTTL = parsed
		}
	}
	return cfg
}

func validateShareHandshake(r *http.Request, cfg shareAuthConfig) (*shareHandshake, int, string) {
	q := r.URL.Query()
	role := strings.TrimSpace(q.Get("role"))
	if role != "server" && role != "client" {
		return nil, http.StatusBadRequest, "invalid role"
	}
	rawToken := strings.TrimSpace(q.Get("token"))
	roomID := strings.TrimSpace(q.Get("room_id"))
	authTicket := strings.TrimSpace(q.Get("auth_ticket"))
	renewToken := strings.TrimSpace(q.Get("renew_token"))
	clientKind := strings.TrimSpace(q.Get("client"))
	clientVersion := strings.TrimSpace(q.Get("client_version"))
	capabilities := splitCaps(q.Get("caps"))
	proto := strings.TrimSpace(q.Get("proto"))
	protocolVersion := 0
	if proto != "" {
		value, err := strconv.Atoi(proto)
		if err != nil {
			return nil, http.StatusBadRequest, "invalid proto"
		}
		protocolVersion = value
	}

	hasV2Params := roomID != "" || authTicket != "" || renewToken != "" || protocolVersion >= shareProtocolV2
	if !hasV2Params {
		if rawToken == "" {
			return nil, http.StatusBadRequest, "missing token"
		}
		return nil, http.StatusGone, shareUpgradeRequiredMessage
	}

	if protocolVersion < requiredShareProtocolVersion {
		return nil, http.StatusGone, shareUpgradeRequiredMessage
	}
	if protocolVersion > requiredShareProtocolVersion {
		return nil, http.StatusBadRequest, fmt.Sprintf("unsupported share protocol %d", protocolVersion)
	}
	if rawToken != "" {
		return nil, http.StatusGone, shareUpgradeRequiredMessage
	}
	if roomID == "" {
		return nil, http.StatusBadRequest, "missing room_id"
	}
	if authTicket == "" && renewToken == "" {
		return nil, http.StatusUnauthorized, "missing auth ticket"
	}
	if authTicket != "" && renewToken != "" {
		return nil, http.StatusBadRequest, "auth_ticket and renew_token are mutually exclusive"
	}
	if cfg.Secret == "" {
		return nil, http.StatusServiceUnavailable, "share v2 unavailable"
	}

	kind := shareTicketKindConnect
	tokenToVerify := authTicket
	connectMode := "auth_ticket"
	if renewToken != "" {
		kind = shareTicketKindRenew
		tokenToVerify = renewToken
		connectMode = "renew_token"
	}
	claims, err := verifyShareTicket(cfg.Secret, tokenToVerify)
	if err != nil {
		return nil, http.StatusUnauthorized, "invalid auth ticket"
	}
	if claims.V < requiredShareProtocolVersion {
		return nil, http.StatusUnauthorized, "unsupported ticket version"
	}
	if claims.Role != role || claims.Kind != kind || claims.RoomID != roomID {
		return nil, http.StatusUnauthorized, "ticket scope mismatch"
	}
	exp := time.Unix(claims.Exp, 0).UTC()
	if time.Now().After(exp) {
		return nil, http.StatusUnauthorized, "ticket expired"
	}
	nextRenewToken, renewExp, err := mintShareRenewToken(cfg.Secret, roomID, role, cfg.RenewTTL)
	if err != nil {
		return nil, http.StatusInternalServerError, "mint renew token"
	}
	postConnectErr := ""
	if !hasCapability(capabilities, requiredTunnelCapability) {
		postConnectErr = shareUpgradeRequiredMessage
	}
	return &shareHandshake{
		role:            role,
		roomKey:         roomID,
		protocolVersion: protocolVersion,
		shareMode:       shareModeV3,
		connectMode:     connectMode,
		postConnectErr:  postConnectErr,
		authExpiresAt:   exp,
		renewToken:      nextRenewToken,
		renewExpiresAt:  renewExp,
		notice:          "",
		clientKind:      clientKind,
		clientVersion:   clientVersion,
		capabilities:    capabilities,
		cryptoKey:       strings.TrimSpace(q.Get("crypto_key")),
		serverPublicKey: strings.TrimSpace(q.Get("kx_pub")),
	}, http.StatusSwitchingProtocols, ""
}

func connectedShareMetadata(handshake *shareHandshake) map[string]any {
	if handshake == nil {
		return nil
	}
	data := map[string]any{
		"protocol_version": handshake.protocolVersion,
		"share_mode":       handshake.shareMode,
		"connect_mode":     handshake.connectMode,
	}
	if handshake.roomKey != "" {
		data["room_id"] = handshake.roomKey
	}
	if handshake.notice != "" {
		data["notice"] = handshake.notice
	}
	if handshake.serverPublicKey != "" {
		data["kx_pub"] = handshake.serverPublicKey
	}
	if !handshake.authExpiresAt.IsZero() {
		data["auth_expires_at"] = handshake.authExpiresAt.Format(time.RFC3339)
	}
	if handshake.renewToken != "" {
		data["renew_token"] = handshake.renewToken
		data["renew_expires_at"] = handshake.renewExpiresAt.Format(time.RFC3339)
	}
	return data
}

func hasCapability(caps []string, required string) bool {
	required = strings.TrimSpace(required)
	if required == "" {
		return true
	}
	for _, capability := range caps {
		if strings.TrimSpace(capability) == required {
			return true
		}
	}
	return false
}

func requestedShareProtocolVersion(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("proto"))
	if raw == "" {
		return requiredShareProtocolVersion, nil
	}
	protocolVersion, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return protocolVersion, nil
}

func splitCaps(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		capability := strings.TrimSpace(part)
		if capability == "" {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	return out
}

func hasShareV2Capability(caps []string, clientVersion string) bool {
	for _, capability := range caps {
		if capability == "share_v2" || capability == "share_renew" || capability == "share_notice" {
			return true
		}
	}
	return strings.TrimSpace(clientVersion) != ""
}

func mintShareRenewToken(secret, roomID, role string, ttl time.Duration) (string, time.Time, error) {
	exp := time.Now().UTC().Add(ttl)
	token, err := mintShareTicket(secret, roomID, role, shareTicketKindRenew, exp)
	return token, exp, err
}

func mintShareConnectTicket(secret, roomID, role string, ttl time.Duration) (string, time.Time, error) {
	exp := time.Now().UTC().Add(ttl)
	token, err := mintShareTicket(secret, roomID, role, shareTicketKindConnect, exp)
	return token, exp, err
}

func mintShareTicket(secret, roomID, role, kind string, exp time.Time) (string, error) {
	return signShareTicket(secret, shareTicketClaims{
		RoomID: roomID,
		Role:   role,
		Kind:   kind,
		Exp:    exp.Unix(),
		V:      requiredShareProtocolVersion,
	})
}

func issueShareSession(cfg shareAuthConfig, requestedProtocol int) (issuedShareSessionResponse, error) {
	if strings.TrimSpace(cfg.Secret) == "" {
		return issuedShareSessionResponse{}, errors.New("share v3 unavailable")
	}
	if requestedProtocol == 0 {
		requestedProtocol = requiredShareProtocolVersion
	}
	if requestedProtocol < requiredShareProtocolVersion {
		return issuedShareSessionResponse{}, errShareUpgradeRequired
	}
	if requestedProtocol > requiredShareProtocolVersion {
		return issuedShareSessionResponse{}, fmt.Errorf("unsupported share protocol %d", requestedProtocol)
	}
	roomID, err := randomHex(16)
	if err != nil {
		return issuedShareSessionResponse{}, err
	}
	serverConnect, authExp, err := mintShareConnectTicket(cfg.Secret, roomID, "server", cfg.ConnectTTL)
	if err != nil {
		return issuedShareSessionResponse{}, err
	}
	clientConnect, _, err := mintShareConnectTicket(cfg.Secret, roomID, "client", cfg.ConnectTTL)
	if err != nil {
		return issuedShareSessionResponse{}, err
	}
	serverRenew, renewExp, err := mintShareRenewToken(cfg.Secret, roomID, "server", cfg.RenewTTL)
	if err != nil {
		return issuedShareSessionResponse{}, err
	}
	return issuedShareSessionResponse{
		ProtocolVersion:  requiredShareProtocolVersion,
		ShareMode:        shareModeV3,
		RoomID:           roomID,
		ServerAuthTicket: serverConnect,
		ClientAuthTicket: clientConnect,
		ServerRenewToken: serverRenew,
		AuthExpiresAt:    authExp.UTC().Format(time.RFC3339),
		RenewExpiresAt:   renewExp.UTC().Format(time.RFC3339),
		Notice:           "",
	}, nil
}

func randomHex(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func signShareTicket(secret string, claims shareTicketClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func verifyShareTicket(secret, token string) (shareTicketClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return shareTicketClaims{}, errors.New("invalid share ticket")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return shareTicketClaims{}, fmt.Errorf("decode share payload: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return shareTicketClaims{}, fmt.Errorf("decode share signature: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return shareTicketClaims{}, errors.New("invalid share signature")
	}
	var claims shareTicketClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return shareTicketClaims{}, fmt.Errorf("decode share claims: %w", err)
	}
	return claims, nil
}
