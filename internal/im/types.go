package im

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	imagepkg "github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

type Platform string

const (
	PlatformUnknown    Platform = ""
	PlatformQQ         Platform = "qq"
	PlatformTelegram   Platform = "telegram"
	PlatformDiscord    Platform = "discord"
	PlatformFeishu     Platform = "feishu"
	PlatformDingTalk   Platform = "dingtalk"
	PlatformSlack      Platform = "slack"
	PlatformDummy      Platform = "dummy"
	PlatformWechat     Platform = "wechat"
	PlatformWeCom      Platform = "wecom"
	PlatformMattermost Platform = "mattermost"
	PlatformMatrix     Platform = "matrix"
	PlatformSignal     Platform = "signal"
	PlatformIRC        Platform = "irc"
	PlatformNostr      Platform = "nostr"
	PlatformSynology   Platform = "synology"
	PlatformTwitch     Platform = "twitch"
	PlatformWhatsApp   Platform = "whatsapp"
)

type AttachmentKind string

const (
	AttachmentImage AttachmentKind = "image"
	AttachmentVoice AttachmentKind = "voice"
	AttachmentAudio AttachmentKind = "audio"
	AttachmentFile  AttachmentKind = "file"
)

type Envelope struct {
	Adapter    string
	Platform   Platform
	ChannelID  string
	ThreadID   string
	SenderID   string
	SenderName string
	MessageID  string
	ReceivedAt time.Time
}

type Attachment struct {
	ID         string
	Kind       AttachmentKind
	Name       string
	MIME       string
	Path       string
	URL        string
	DataBase64 string
	Transcript string
	Metadata   map[string]string
}

type InboundMessage struct {
	Envelope    Envelope
	Text        string
	Attachments []Attachment
	Metadata    map[string]string
}

func (m InboundMessage) ProviderContent() []provider.ContentBlock {
	blocks := make([]provider.ContentBlock, 0, 1+len(m.Attachments))
	text := strings.TrimSpace(m.Text)
	if text != "" {
		blocks = append(blocks, provider.TextBlock(text))
	}
	for _, attachment := range m.Attachments {
		switch attachment.Kind {
		case AttachmentImage:
			if hint := attachmentPromptHint(attachment); hint != "" {
				blocks = append(blocks, provider.TextBlock(hint))
			}
			mime := strings.TrimSpace(attachment.MIME)
			b64 := strings.TrimSpace(attachment.DataBase64)

			if mime == "" && b64 != "" {
				if data, err := base64.StdEncoding.DecodeString(b64); err == nil {
					mime = imagepkg.DetectMIME(data)
				}
			}
			if mime != "" && b64 != "" {
				blocks = append(blocks, provider.ImageBlock(mime, b64))
				continue
			}
		case AttachmentVoice, AttachmentAudio:
			if transcript := strings.TrimSpace(attachment.Transcript); transcript != "" {
				blocks = append(blocks, provider.TextBlock(transcript))
			} else if hint := attachmentPromptHint(attachment); hint != "" {
				blocks = append(blocks, provider.TextBlock(hint))
			}
		default:
			if hint := attachmentPromptHint(attachment); hint != "" {
				blocks = append(blocks, provider.TextBlock(hint))
			}
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, provider.TextBlock(""))
	}
	return blocks
}

func attachmentPromptHint(attachment Attachment) string {
	label := strings.TrimSpace(attachment.Name)
	if label == "" {
		label = string(attachment.Kind)
	}
	switch {
	case strings.TrimSpace(attachment.Path) != "":
		return "[Attached " + label + " path: " + strings.TrimSpace(attachment.Path) + "]"
	case strings.TrimSpace(attachment.URL) != "":
		return "[Attached " + label + " url: " + strings.TrimSpace(attachment.URL) + "]"
	default:
		return ""
	}
}

type OutboundEventKind string

const (
	OutboundEventText            OutboundEventKind = "text"
	OutboundEventStatus          OutboundEventKind = "status"
	OutboundEventToolCall        OutboundEventKind = "tool_call"
	OutboundEventToolResult      OutboundEventKind = "tool_result"
	OutboundEventApprovalRequest OutboundEventKind = "approval_request"
	OutboundEventApprovalResult  OutboundEventKind = "approval_result"
)

type OutboundEvent struct {
	Kind      OutboundEventKind
	Text      string
	Status    string
	ToolCall  *ToolCallInfo
	ToolRes   *ToolResultInfo
	Approval  *ApprovalRequest
	Result    *ApprovalResult
	CreatedAt time.Time
}

type ToolCallInfo struct {
	ToolName string
	Args     string
	Detail   string
	Lang     string
}

type ToolResultInfo struct {
	ToolName     string
	Args         string
	Result       string
	IsError      bool
	Detail       string
	Lang         string
	CallNotified bool
}

type SessionBinding struct {
	SessionID string
	Workspace string
	BoundAt   time.Time
}

type ChannelBinding struct {
	Workspace             string
	Platform              Platform
	Adapter               string
	TargetID              string
	ChannelID             string
	ThreadID              string
	LastInboundMessageID  string
	LastOutboundMessageID string
	LastInboundAt         time.Time
	PassiveReplyCount     int
	PassiveReplyStartedAt time.Time
	BoundAt               time.Time
	Muted                 bool
	ContextToken          string
	ContextTokenUpdatedAt time.Time
	LastSessionID         string
}

type AdapterState struct {
	Name       string
	Platform   Platform
	Healthy    bool
	Status     string
	LastError  string
	ContactURI string
	QRCode     string
	UpdatedAt  time.Time
}

type ApprovalRequest struct {
	ID          string
	ToolName    string
	Input       string
	RequestedAt time.Time
	Source      string
}

type ApprovalResponse struct {
	ApprovalID  string
	Decision    permission.Decision
	RespondedBy string
	RespondedAt time.Time
}

type ApprovalResult struct {
	Request     ApprovalRequest
	Decision    permission.Decision
	RespondedBy string
	RespondedAt time.Time
}

type ApprovalState struct {
	Request     ApprovalRequest
	Resolved    bool
	Decision    permission.Decision
	RespondedBy string
	RespondedAt time.Time
}

type StatusSnapshot struct {
	ActiveSession    *SessionBinding
	CurrentBindings  []ChannelBinding
	DisabledBindings []ChannelBinding
	MutedBindings    []ChannelBinding
	PendingPairing   *PairingChallenge
	Adapters         []AdapterState
	PendingApprovals []ApprovalState
}

func (s StatusSnapshot) BindingByAdapter(adapter string) *ChannelBinding {
	for i := range s.CurrentBindings {
		if s.CurrentBindings[i].Adapter == adapter {
			return &s.CurrentBindings[i]
		}
	}
	return nil
}

type Bridge interface {
	SubmitInboundMessage(ctx context.Context, msg InboundMessage) error
}

type Sink interface {
	Name() string
	Send(context.Context, ChannelBinding, OutboundEvent) error
}

type OutboundFile struct {
	Path     string
	Filename string
	MIME     string
	Data     []byte
}

type FileSender interface {
	SendFile(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error
}

type ShareLinkProvider interface {
	GenerateShareLink(context.Context, string) (string, error)
}

type TypingIndicator interface {
	TriggerTyping(ctx context.Context, binding ChannelBinding) error
}

type InteractiveSender interface {
	SendInteractive(ctx context.Context, binding ChannelBinding, msg InteractiveMessage) (string, error)
}

type InteractiveMessage struct {
	ID          string
	Text        string
	Buttons     []InteractiveButton
	MultiSelect bool
	Placeholder string
}

type InteractiveButton struct {
	Label string
	Value string
	Style string
}

type InteractiveCallback struct {
	MessageID string
	Values    []string
	Adapter   string
	Envelope  Envelope
}

type Closer interface {
	Close() error
}

func LastMessageID(b ChannelBinding) string {
	if id := strings.TrimSpace(b.LastOutboundMessageID); id != "" {
		return id
	}
	if id := strings.TrimSpace(b.LastInboundMessageID); id != "" {
		return id
	}
	return ""
}

func LastReactionTargetMessageID(b ChannelBinding) string {
	if id := strings.TrimSpace(b.LastInboundMessageID); id != "" {
		return id
	}
	if id := strings.TrimSpace(b.LastOutboundMessageID); id != "" {
		return id
	}
	return ""
}
