package im

// PrivateClaw protocol types for communicating with the Relay Server.

// ---- Encrypted Envelope ----

type pcEncryptedEnvelope struct {
	Version    int    `json:"version"`
	MessageID  string `json:"messageId"`
	IV         string `json:"iv"`
	Ciphertext string `json:"ciphertext"`
	Tag        string `json:"tag"`
	SentAt     string `json:"sentAt"`
}

// ---- Invite ----

type PCInvite struct {
	Version       int    `json:"version"`
	SessionID     string `json:"sessionId"`
	SessionKey    string `json:"sessionKey"`
	AppWsURL      string `json:"appWsUrl"`
	ExpiresAt     string `json:"expiresAt"`
	GroupMode     bool   `json:"groupMode,omitempty"`
	ProviderLabel string `json:"providerLabel,omitempty"`
	RelayLabel    string `json:"relayLabel,omitempty"`
}

// ---- Relay WebSocket Messages ----

// Provider → Relay messages

type pcProviderCreateSession struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	TTLMS     *int   `json:"ttlMs,omitempty"`
	Label     string `json:"label,omitempty"`
	GroupMode *bool  `json:"groupMode,omitempty"`
}

type pcProviderFrame struct {
	Type        string              `json:"type"`
	SessionID   string              `json:"sessionId"`
	Envelope    pcEncryptedEnvelope `json:"envelope"`
	TargetAppID string              `json:"targetAppId,omitempty"`
}

type pcProviderCloseSession struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Reason    string `json:"reason,omitempty"`
}

type pcProviderCloseApp struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	AppID     string `json:"appId"`
	Reason    string `json:"reason,omitempty"`
}

type pcProviderRenewSession struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	SessionID string `json:"sessionId"`
	TTLMS     int    `json:"ttlMs"`
}

// Relay → Provider messages

type pcRelayProviderReady struct {
	Type string `json:"type"`
}

type pcRelaySessionCreated struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	SessionID string `json:"sessionId"`
	ExpiresAt string `json:"expiresAt"`
}

type pcRelaySessionRenewed struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	SessionID string `json:"sessionId"`
	ExpiresAt string `json:"expiresAt"`
}

type pcRelayFrame struct {
	Type      string              `json:"type"`
	SessionID string              `json:"sessionId"`
	Envelope  pcEncryptedEnvelope `json:"envelope"`
}

type pcRelaySessionClosed struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Reason    string `json:"reason"`
}

type pcRelayError struct {
	Type      string `json:"type"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	SessionID string `json:"sessionId,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

// ---- Encrypted Payload kinds ----

const (
	pcKindClientHello      = "client_hello"
	pcKindUserMessage      = "user_message"
	pcKindAssistantMessage = "assistant_message"
	pcKindSystemMessage    = "system_message"
	pcKindServerWelcome    = "server_welcome"
	pcKindSessionClose     = "session_close"
	pcKindSessionRenewed   = "session_renewed"
	pcKindProvCapabilities = "provider_capabilities"
)

// pcPayload is the generic encrypted payload container.
// Each payload is a JSON object with at least a "kind" field.
type pcPayload map[string]interface{}

func pcPayloadKind(p pcPayload) string {
	v, _ := p["kind"].(string)
	return v
}

func pcPayloadString(p pcPayload, key string) string {
	v, _ := p[key].(string)
	return v
}

func pcPayloadBool(p pcPayload, key string) bool {
	v, _ := p[key].(bool)
	return v
}

func pcPayloadInt(p pcPayload, key string) int {
	switch v := p[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// Relay message type constants
const (
	pcTypeCreateSession  = "provider:create_session"
	pcTypeRenewSession   = "provider:renew_session"
	pcTypeFrame          = "provider:frame"
	pcTypeCloseSession   = "provider:close_session"
	pcTypeCloseApp       = "provider:close_app"
	pcTypeProviderReady  = "relay:provider_ready"
	pcTypeSessionCreated = "relay:session_created"
	pcTypeSessionRenewed = "relay:session_renewed"
	pcTypeRelayFrame     = "relay:frame"
	pcTypeSessionClosed  = "relay:session_closed"
	pcTypeError          = "relay:error"
)
