package tunnel

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	ShareProtocolLegacy = 1
	ShareProtocolV2     = 2

	ShareModeLegacy = "legacy"
	ShareModeV2     = "v2"
	ShareModeCompat = "compat"

	ShareTicketKindConnect = "connect"
	ShareTicketKindRenew   = "renew"

	shareProtocolEnv   = "GGCODE_SHARE_PROTOCOL"
	shareSecretEnv     = "GGCODE_SHARE_V2_SECRET"
	shareConnectTTLEnv = "GGCODE_SHARE_V2_CONNECT_TTL"
	shareRenewTTLEnv   = "GGCODE_SHARE_V2_RENEW_TTL"
)

const (
	defaultShareConnectTTL = 15 * time.Minute
	defaultShareRenewTTL   = 30 * 24 * time.Hour
)

var defaultShareCapabilities = []string{
	"share_v2",
	"share_notice",
	"share_renew",
}

type ShareRuntimeConfig struct {
	EnableV2   bool
	Secret     string
	ConnectTTL time.Duration
	RenewTTL   time.Duration
}

type RelayClientMetadata struct {
	ClientKind    string
	ClientVersion string
	Capabilities  []string
}

type ShareDescriptor struct {
	ProtocolVersion int
	ShareMode       string
	RoomID          string
	Token           string
	AuthTicket      string
	RenewToken      string
	CryptoKey       string
	Notice          string
	AuthExpiresAt   time.Time
	RenewExpiresAt  time.Time
}

type shareTicketClaims struct {
	RoomID string `json:"room_id"`
	Role   string `json:"role"`
	Kind   string `json:"kind"`
	Exp    int64  `json:"exp"`
	V      int    `json:"v"`
}

func loadShareRuntimeConfig() ShareRuntimeConfig {
	cfg := ShareRuntimeConfig{
		Secret:     strings.TrimSpace(os.Getenv(shareSecretEnv)),
		ConnectTTL: defaultShareConnectTTL,
		RenewTTL:   defaultShareRenewTTL,
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(shareProtocolEnv))) {
	case "2", ShareModeV2:
		cfg.EnableV2 = true
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

func (cfg ShareRuntimeConfig) v2Enabled() bool {
	return cfg.EnableV2 && strings.TrimSpace(cfg.Secret) != ""
}

func (cfg ShareRuntimeConfig) v2RequestedWithoutSecret() bool {
	return cfg.EnableV2 && strings.TrimSpace(cfg.Secret) == ""
}

func (d ShareDescriptor) IsV2() bool {
	return d.ProtocolVersion >= ShareProtocolV2 && d.RoomID != ""
}

func (d ShareDescriptor) CryptoMaterial() string {
	if strings.TrimSpace(d.CryptoKey) != "" {
		return d.CryptoKey
	}
	return d.Token
}

func (d ShareDescriptor) SessionToken() string {
	if d.Token != "" {
		return d.Token
	}
	return d.RoomID
}

func (d ShareDescriptor) PublicConnectURL(relayURL string) string {
	return buildShareURL(relayURL, d, "client", RelayClientMetadata{}, false)
}

func (d ShareDescriptor) RuntimeConnectURL(relayURL, role string, meta RelayClientMetadata, preferRenew bool) string {
	return buildShareURL(relayURL, d, role, meta, preferRenew)
}

func buildShareURL(relayURL string, d ShareDescriptor, role string, meta RelayClientMetadata, preferRenew bool) string {
	base := strings.TrimSuffix(relayURL, "/")
	u, err := url.Parse(base + "/ws")
	if err != nil {
		return base + "/ws"
	}
	q := u.Query()
	q.Set("role", role)
	if kind := strings.TrimSpace(meta.ClientKind); kind != "" {
		q.Set("client", kind)
	}
	if version := strings.TrimSpace(meta.ClientVersion); version != "" {
		q.Set("client_version", version)
	}
	if caps := encodeCapabilities(meta.Capabilities); caps != "" {
		q.Set("caps", caps)
	}
	if d.IsV2() {
		q.Set("proto", strconv.Itoa(d.ProtocolVersion))
		q.Set("room_id", d.RoomID)
		if cryptoKey := strings.TrimSpace(d.CryptoKey); cryptoKey != "" {
			q.Set("crypto_key", cryptoKey)
		}
		if preferRenew && strings.TrimSpace(d.RenewToken) != "" {
			q.Set("renew_token", d.RenewToken)
		} else if strings.TrimSpace(d.AuthTicket) != "" {
			q.Set("auth_ticket", d.AuthTicket)
		}
	} else {
		q.Set("token", d.Token)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func encodeCapabilities(caps []string) string {
	filtered := make([]string, 0, len(caps))
	seen := make(map[string]struct{}, len(caps))
	for _, cap := range caps {
		trimmed := strings.TrimSpace(cap)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		filtered = append(filtered, trimmed)
	}
	return strings.Join(filtered, ",")
}

func defaultRelayClientMetadata(kind, version string) RelayClientMetadata {
	return RelayClientMetadata{
		ClientKind:    strings.TrimSpace(kind),
		ClientVersion: strings.TrimSpace(version),
		Capabilities:  append([]string(nil), defaultShareCapabilities...),
	}
}

func newLegacyShareDescriptor(token string) ShareDescriptor {
	return ShareDescriptor{
		ProtocolVersion: ShareProtocolLegacy,
		ShareMode:       ShareModeLegacy,
		Token:           token,
	}
}

func buildV2ShareDescriptors(cfg ShareRuntimeConfig) (server ShareDescriptor, client ShareDescriptor, err error) {
	roomID, err := randomHex(16)
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, err
	}
	cryptoKey, err := randomHex(32)
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, err
	}
	now := time.Now().UTC()
	connectExp := now.Add(cfg.ConnectTTL)
	renewExp := now.Add(cfg.RenewTTL)
	serverConnect, err := signShareTicket(cfg.Secret, shareTicketClaims{
		RoomID: roomID,
		Role:   "server",
		Kind:   ShareTicketKindConnect,
		Exp:    connectExp.Unix(),
		V:      ShareProtocolV2,
	})
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, err
	}
	clientConnect, err := signShareTicket(cfg.Secret, shareTicketClaims{
		RoomID: roomID,
		Role:   "client",
		Kind:   ShareTicketKindConnect,
		Exp:    connectExp.Unix(),
		V:      ShareProtocolV2,
	})
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, err
	}
	serverRenew, err := signShareTicket(cfg.Secret, shareTicketClaims{
		RoomID: roomID,
		Role:   "server",
		Kind:   ShareTicketKindRenew,
		Exp:    renewExp.Unix(),
		V:      ShareProtocolV2,
	})
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, err
	}
	clientRenew, err := signShareTicket(cfg.Secret, shareTicketClaims{
		RoomID: roomID,
		Role:   "client",
		Kind:   ShareTicketKindRenew,
		Exp:    renewExp.Unix(),
		V:      ShareProtocolV2,
	})
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, err
	}
	notice := "Experimental share v2 is enabled locally."
	server = ShareDescriptor{
		ProtocolVersion: ShareProtocolV2,
		ShareMode:       ShareModeV2,
		RoomID:          roomID,
		AuthTicket:      serverConnect,
		RenewToken:      serverRenew,
		CryptoKey:       cryptoKey,
		Notice:          notice,
		AuthExpiresAt:   connectExp,
		RenewExpiresAt:  renewExp,
	}
	client = ShareDescriptor{
		ProtocolVersion: ShareProtocolV2,
		ShareMode:       ShareModeV2,
		RoomID:          roomID,
		AuthTicket:      clientConnect,
		RenewToken:      clientRenew,
		CryptoKey:       cryptoKey,
		Notice:          notice,
		AuthExpiresAt:   connectExp,
		RenewExpiresAt:  renewExp,
	}
	return server, client, nil
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
		return shareTicketClaims{}, fmt.Errorf("invalid share ticket")
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
		return shareTicketClaims{}, fmt.Errorf("invalid share signature")
	}
	var claims shareTicketClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return shareTicketClaims{}, fmt.Errorf("decode share claims: %w", err)
	}
	return claims, nil
}
