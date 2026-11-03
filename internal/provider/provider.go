package provider

import (
	"context"
	"encoding/json"
	"math"

	"github.com/topcheer/ggcode/internal/config"
)

type Message struct {
	ID      string         `json:"id,omitempty"`
	Role    string         `json:"role"`
	Origin  string         `json:"origin,omitempty"`
	Content []ContentBlock `json:"content"`
}

type ContentBlock struct {
	Type              string          `json:"type"`
	Text              string          `json:"text,omitempty"`
	ImageMIME         string          `json:"image_mime,omitempty"`
	ImageData         string          `json:"image_data,omitempty"`
	ToolName          string          `json:"tool_name,omitempty"`
	ToolID            string          `json:"tool_id,omitempty"`
	Input             json.RawMessage `json:"input,omitempty"`
	Output            string          `json:"output,omitempty"`
	IsError           bool            `json:"is_error,omitempty"`
	Images            []ContentImage  `json:"images,omitempty"`
	ReasoningContent  string          `json:"reasoning_content,omitempty"`
	ThinkingSignature string          `json:"thinking_signature,omitempty"`
	ThinkingData      string          `json:"thinking_data,omitempty"`
	Cache             bool            `json:"cache,omitempty"`

	Raw json.RawMessage `json:"raw,omitempty"`
	ID  string          `json:"id,omitempty"`

	CallerRaw json.RawMessage `json:"caller_raw,omitempty"`

	ServerTool    string               `json:"server_tool,omitempty"`
	ServerResults []ServerSearchResult `json:"server_results,omitempty"`
	ServerFetch   *ServerFetchResult   `json:"server_fetch,omitempty"`
	ServerError   string               `json:"server_error,omitempty"`
}

func ImageBlock(mime, base64Data string) ContentBlock {
	return ContentBlock{Type: "image", ImageMIME: mime, ImageData: base64Data}
}

func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

func ToolUseBlock(id, name string, input json.RawMessage) ContentBlock {
	return ContentBlock{Type: "tool_use", ToolID: id, ToolName: name, Input: input}
}

func ToolResultBlock(id, output string, isError bool) ContentBlock {
	return ContentBlock{Type: "tool_result", ToolID: id, Output: output, IsError: isError}
}

func ToolResultNamedBlock(id, name, output string, isError bool) ContentBlock {
	return ContentBlock{Type: "tool_result", ToolID: id, ToolName: name, Output: output, IsError: isError}
}

type ContentImage struct {
	MIME   string `json:"mime"`
	Base64 string `json:"base64"`
}

type ServerSearchResult struct {
	Title            string `json:"title,omitempty"`
	URL              string `json:"url,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
	PageAge          string `json:"page_age,omitempty"`
}

type ServerFetchResult struct {
	URL         string `json:"url,omitempty"`
	Title       string `json:"title,omitempty"`
	RetrievedAt string `json:"retrieved_at,omitempty"`
	Citations   bool   `json:"citations,omitempty"`
}

func ToolResultWithImages(id, name, textOutput string, images []ContentImage, isError bool) ContentBlock {
	return ContentBlock{
		Type:     "tool_result",
		ToolID:   id,
		ToolName: name,
		Output:   textOutput,
		Images:   images,
		IsError:  isError,
	}
}

type TokenUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CacheRead         int `json:"cache_read_tokens"`
	CacheWrite        int `json:"cache_write_tokens"`
	PromptTokensTotal int `json:"prompt_tokens_total,omitempty"`
}

func (u TokenUsage) Add(delta TokenUsage) TokenUsage {
	u.InputTokens += delta.InputTokens
	u.OutputTokens += delta.OutputTokens
	u.CacheRead += delta.CacheRead
	u.CacheWrite += delta.CacheWrite
	u.PromptTokensTotal += delta.PromptTokensTotal
	return u
}

func (u TokenUsage) DisplayInputTokens() int {
	if u.CacheRead <= 0 {
		return u.InputTokens
	}
	if u.PromptTokensTotal > 0 {
		normalized := u.PromptTokensTotal - u.CacheRead
		if normalized >= 0 {
			if normalized < u.InputTokens {
				return normalized
			}
			return u.InputTokens
		}
	}
	return u.InputTokens
}

func (u TokenUsage) TotalInputTokens() int {
	if u.PromptTokensTotal > 0 {
		return u.PromptTokensTotal
	}
	return u.InputTokens
}

func (u TokenUsage) Total() int {
	return u.TotalInputTokens() + u.OutputTokens
}

func (u TokenUsage) CacheHitPercent() int {
	totalPromptTokens := u.PromptTokensTotal
	if totalPromptTokens <= 0 || totalPromptTokens < u.CacheRead {
		fallbackTotal := u.InputTokens
		if u.CacheWrite > 0 || u.CacheRead > u.InputTokens {
			fallbackTotal = u.InputTokens + u.CacheRead + u.CacheWrite
		}
		if fallbackTotal > totalPromptTokens {
			totalPromptTokens = fallbackTotal
		}
	}
	if totalPromptTokens <= 0 || u.CacheRead <= 0 {
		return 0
	}
	return int(math.Round(float64(u.CacheRead) * 100 / float64(totalPromptTokens)))
}

type StreamEvent struct {
	Type              StreamEventType
	Text              string
	ThinkingSignature string
	Tool              ToolCallDelta
	Result            string
	IsError           bool
	Usage             *TokenUsage
	Confidence        *float64
	Error             error
	Truncated         bool
	PolicyBlocked     bool
	Block             ContentBlock
}

type StreamEventType int

const (
	StreamEventText StreamEventType = iota
	StreamEventToolCallChunk
	StreamEventToolCallDone
	StreamEventToolResult
	StreamEventDone
	StreamEventError
	StreamEventServerTool
	StreamEventReasoning
	StreamEventSystem
)

type ToolCallDelta struct {
	ID               string
	Index            int
	Name             string
	ServerTool       bool
	Arguments        json.RawMessage
	ThoughtSignature []byte
	Caller           json.RawMessage
}

type ToolDefinition struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Parameters     json.RawMessage `json:"parameters"`
	AllowedCallers []string        `json:"allowed_callers,omitempty"`
	Strict         bool            `json:"strict,omitempty"`
	DeferLoading   bool            `json:"defer_loading,omitempty"`
}

type Provider interface {
	Name() string

	Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error)

	ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error)

	CountTokens(ctx context.Context, messages []Message) (int, error)
}

type MaxTokensSetter interface {
	SetMaxTokens(n int)
}

type ReasoningEffortProvider interface {
	SetReasoningEffort(effort string)
	ReasoningEffort() string
}

type TextVerbosityProvider interface {
	SetTextVerbosity(verbosity string)
	TextVerbosity() string
}

type ServiceTierProvider interface {
	SetServiceTier(tier string)
	ServiceTier() string
}

type ServerToolsSetter interface {
	SetServerTools(tools []ServerToolConfig)
}

type ServerToolConfig = config.ServerToolConfig

type ToolChoiceProvider interface {
	SetToolChoice(choice string)
	ToolChoice() string
}

type ResponseSchemaSetter interface {
	SetResponseSchema(schema json.RawMessage)
	ResponseSchema() json.RawMessage
}

type PromptCacheKeySetter interface {
	SetPromptCacheKey(key string)
	PromptCacheKey() string
}

type SamplingOverride struct {
	MaxTokens     int
	StopSequences []string
	Temperature   float64
}

type SamplingOverrideSetter interface {
	SetSamplingOverride(o *SamplingOverride)
	SamplingOverride() *SamplingOverride
}

type SamplingConfigProvider interface {
	SetTemperature(temp float64)
	Temperature() float64
	SetTopP(topP float64)
	TopP() float64
}

type ModelNameProvider interface {
	ModelName() string
}

type RateLimitProvider interface {
	RateLimitInfo() RateLimitInfo
}

type ChatResponse struct {
	Message    Message
	Usage      TokenUsage
	StopReason string
	Confidence *float64
}
