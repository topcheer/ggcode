package im

import (
	"context"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

type Platform string

const (
	PlatformUnknown  Platform = ""
	PlatformQQ       Platform = "qq"
	PlatformTelegram Platform = "telegram"
	PlatformDiscord  Platform = "discord"
	PlatformFeishu   Platform = "feishu"
	PlatformDingTalk Platform = "dingtalk"
	PlatformSlack    Platform = "slack"
	PlatformDummy    Platform = "dummy"
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
			if strings.TrimSpace(attachment.MIME) != "" && strings.TrimSpace(attachment.DataBase64) != "" {
				blocks = append(blocks, provider.ImageBlock(attachment.MIME, attachment.DataBase64))
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
	Lang     string // "zh-CN" or "en", set by emitter
}

type ToolResultInfo struct {
	ToolName string
	Args     string
	Result   string
	IsError  bool
	Detail   string // display text for the tool call (e.g. command line)
	Lang     string // "zh-CN" or "en", set by emitter
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
}

type AdapterDescriptor struct {
	Name         string
	Platform     Platform
	Capabilities []string
}

type AdapterState struct {
	Name      string
	Platform  Platform
	Healthy   bool
	Status    string
	LastError string
	UpdatedAt time.Time
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

type Bridge interface {
	SubmitInboundMessage(ctx context.Context, msg InboundMessage) error
}

type Sink interface {
	Name() string
	Send(context.Context, ChannelBinding, OutboundEvent) error
}

type ShareLinkProvider interface {
	GenerateShareLink(context.Context, string) (string, error)
}

// TypingIndicator is an optional interface that adapters can implement
// to show a native "bot is typing" indicator on the IM platform.
type TypingIndicator interface {
	TriggerTyping(ctx context.Context, binding ChannelBinding) error
}

// Closer is an optional interface that adapters implement to close
// their underlying network connections. Called by Manager when an
// adapter is muted or disabled to physically disconnect.
type Closer interface {
	Close() error
}

// LastMessageID returns the most recent message ID for typing reaction targeting:
// prefers the user's inbound message, falls back to the bot's last outbound message.
func LastMessageID(b ChannelBinding) string {
	if id := strings.TrimSpace(b.LastInboundMessageID); id != "" {
		return id
	}
	return strings.TrimSpace(b.LastOutboundMessageID)
}
