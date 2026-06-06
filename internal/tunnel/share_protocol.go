package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

const (
	ShareProtocolLegacy          = 1
	ShareProtocolV2              = 2
	ShareProtocolV3              = 3
	RequiredShareProtocolVersion = ShareProtocolV3

	ShareModeLegacy = "legacy"
	ShareModeV2     = "v2"
	ShareModeV3     = "v3"
	ShareModeCompat = "compat"

	shareProtocolEnv        = "GGCODE_SHARE_PROTOCOL"
	shareSessionPath        = "/share/session"
	shareSessionRefreshPath = "/share/session/refresh"
	defaultShareIssueTimout = 15 * time.Second
)

var defaultShareCapabilities = []string{
	"share_v2",
	"share_v3",
	"share_notice",
	"share_renew",
	"tunnel_messages_v1",
}

type ShareRuntimeConfig struct {
	EnableV2 bool
	EnableV3 bool
}

type RelayClientMetadata struct {
	ClientKind    string
	ClientVersion string
	Capabilities  []string
}

type ShareDescriptor struct {
	ProtocolVersion  int
	ShareMode        string
	RoomID           string
	Token            string
	AuthTicket       string
	RenewToken       string
	CryptoKey        string
	ServerPublicKey  string
	ServerPrivateKey string
	Notice           string
	AuthExpiresAt    time.Time
	RenewExpiresAt   time.Time
}

type relayIssuedShareSessionResponse struct {
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

type relayRefreshShareSessionRequest struct {
	RoomID           string `json:"room_id"`
	ServerRenewToken string `json:"server_renew_token"`
}

type relayRefreshedShareSessionResponse struct {
	ProtocolVersion  int    `json:"protocol_version"`
	ShareMode        string `json:"share_mode"`
	RoomID           string `json:"room_id"`
	ClientAuthTicket string `json:"client_auth_ticket"`
	ServerRenewToken string `json:"server_renew_token,omitempty"`
	AuthExpiresAt    string `json:"auth_expires_at,omitempty"`
	RenewExpiresAt   string `json:"renew_expires_at,omitempty"`
	Notice           string `json:"notice,omitempty"`
}

func loadShareRuntimeConfig() ShareRuntimeConfig {
	return ShareRuntimeConfig{
		EnableV2: true,
		EnableV3: true,
	}
}

func (cfg ShareRuntimeConfig) v2Enabled() bool {
	return cfg.EnableV2
}

func (cfg ShareRuntimeConfig) v3Enabled() bool {
	return cfg.EnableV3
}

func (cfg ShareRuntimeConfig) issuedProtocolVersion() int {
	return RequiredShareProtocolVersion
}

func (d ShareDescriptor) IsV2() bool {
	return d.ProtocolVersion >= ShareProtocolV2 && d.RoomID != ""
}

func (d ShareDescriptor) IsV3() bool {
	return d.ProtocolVersion >= ShareProtocolV3 && d.RoomID != ""
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
		if cryptoKey := strings.TrimSpace(d.CryptoKey); cryptoKey != "" && !d.IsV3() {
			q.Set("crypto_key", cryptoKey)
		}
		if serverPublicKey := strings.TrimSpace(d.ServerPublicKey); serverPublicKey != "" {
			q.Set("kx_pub", serverPublicKey)
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

func requestIssuedShareSession(ctx context.Context, relayURL string, cfg ShareRuntimeConfig) (server ShareDescriptor, client ShareDescriptor, err error) {
	endpoint, err := shareSessionEndpoint(relayURL)
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, err
	}
	endpoint = endpoint + "?proto=" + strconv.Itoa(cfg.issuedProtocolVersion())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := util.NewInsecureAwareClient(defaultShareIssueTimout).Do(req)
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("issue share session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("issue share session: %s", msg)
	}

	var issued relayIssuedShareSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("decode issued share session: %w", err)
	}
	if issued.ProtocolVersion < RequiredShareProtocolVersion {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("issue share session: invalid protocol version %d", issued.ProtocolVersion)
	}
	if requested := cfg.issuedProtocolVersion(); issued.ProtocolVersion != requested {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("issue share session: relay returned protocol %d, want %d", issued.ProtocolVersion, requested)
	}
	if strings.TrimSpace(issued.RoomID) == "" || strings.TrimSpace(issued.ServerAuthTicket) == "" || strings.TrimSpace(issued.ClientAuthTicket) == "" {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("issue share session: incomplete descriptor")
	}

	authExpiresAt, err := parseShareTimestamp(issued.AuthExpiresAt)
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("issue share session auth expiry: %w", err)
	}
	renewExpiresAt, err := parseShareTimestamp(issued.RenewExpiresAt)
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("issue share session renew expiry: %w", err)
	}
	shareMode := strings.TrimSpace(issued.ShareMode)
	if shareMode == "" {
		shareMode = ShareModeV2
	}
	base := ShareDescriptor{
		ProtocolVersion: issued.ProtocolVersion,
		ShareMode:       shareMode,
		RoomID:          issued.RoomID,
		Notice:          issued.Notice,
		AuthExpiresAt:   authExpiresAt,
		RenewExpiresAt:  renewExpiresAt,
	}
	cryptoKey, err := randomHex(32)
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, err
	}
	if issued.ProtocolVersion >= ShareProtocolV3 {
		publicKey, privateKey, err := generateShareKeyExchangeKeyPair()
		if err != nil {
			return ShareDescriptor{}, ShareDescriptor{}, err
		}
		base.ServerPublicKey = publicKey
		base.ServerPrivateKey = privateKey
	} else {
		base.CryptoKey = cryptoKey
	}
	server = base
	server.CryptoKey = cryptoKey
	server.AuthTicket = issued.ServerAuthTicket
	server.RenewToken = issued.ServerRenewToken
	client = publicShareDescriptorFromServer(server, issued.ClientAuthTicket)
	return server, client, nil
}

func shareSessionEndpoint(relayURL string) (string, error) {
	return shareEndpoint(relayURL, shareSessionPath)
}

func shareSessionRefreshEndpoint(relayURL string) (string, error) {
	return shareEndpoint(relayURL, shareSessionRefreshPath)
}

func shareEndpoint(relayURL, path string) (string, error) {
	if err := validateRelayURLSecurity(relayURL); err != nil {
		return "", err
	}
	u, err := parseRelayURLBase(relayURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported relay scheme %q", u.Scheme)
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	return u.String(), nil
}

func refreshIssuedShareSession(ctx context.Context, relayURL string, server ShareDescriptor) (updatedServer ShareDescriptor, client ShareDescriptor, err error) {
	roomID := strings.TrimSpace(server.RoomID)
	renewToken := strings.TrimSpace(server.RenewToken)
	if roomID == "" || renewToken == "" {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("refresh share session: incomplete descriptor")
	}
	endpoint, err := shareSessionRefreshEndpoint(relayURL)
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, err
	}
	body, err := json.Marshal(relayRefreshShareSessionRequest{
		RoomID:           roomID,
		ServerRenewToken: renewToken,
	})
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("refresh share session request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := util.NewInsecureAwareClient(defaultShareIssueTimout).Do(req)
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("refresh share session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("refresh share session: %s", msg)
	}
	var refreshed relayRefreshedShareSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&refreshed); err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("decode refreshed share session: %w", err)
	}
	if strings.TrimSpace(refreshed.RoomID) == "" || strings.TrimSpace(refreshed.ClientAuthTicket) == "" || strings.TrimSpace(refreshed.ServerRenewToken) == "" {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("refresh share session: incomplete descriptor")
	}
	if refreshed.RoomID != roomID {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("refresh share session: relay returned mismatched room %q", refreshed.RoomID)
	}
	authExpiresAt, err := parseShareTimestamp(refreshed.AuthExpiresAt)
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("refresh share session auth expiry: %w", err)
	}
	renewExpiresAt, err := parseShareTimestamp(refreshed.RenewExpiresAt)
	if err != nil {
		return ShareDescriptor{}, ShareDescriptor{}, fmt.Errorf("refresh share session renew expiry: %w", err)
	}
	updatedServer = server
	if refreshed.ProtocolVersion != 0 {
		updatedServer.ProtocolVersion = refreshed.ProtocolVersion
	}
	if shareMode := strings.TrimSpace(refreshed.ShareMode); shareMode != "" {
		updatedServer.ShareMode = shareMode
	}
	updatedServer.Notice = refreshed.Notice
	updatedServer.AuthExpiresAt = authExpiresAt
	updatedServer.RenewExpiresAt = renewExpiresAt
	updatedServer.RenewToken = refreshed.ServerRenewToken
	client = publicShareDescriptorFromServer(updatedServer, refreshed.ClientAuthTicket)
	return updatedServer, client, nil
}

func publicShareDescriptorFromServer(server ShareDescriptor, clientAuthTicket string) ShareDescriptor {
	client := server
	client.AuthTicket = clientAuthTicket
	client.RenewToken = ""
	if client.ProtocolVersion >= ShareProtocolV3 {
		client.ServerPrivateKey = ""
		client.CryptoKey = ""
	}
	return client
}

func parseShareTimestamp(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func randomHex(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
