package provider

import (
	"context"
	"encoding/json"
)

// Message represents a single message in the conversation.
type Message struct {
	Role    string         `json:"role"` // "user", "assistant", "system"
	Content []ContentBlock `json:"content"`
}

// ContentBlock is a union type: text, image, tool call, or tool result.
type ContentBlock struct {
	Type      string          `json:"type"` // "text", "image", "tool_use", "tool_result"
	Text      string          `json:"text,omitempty"`
	ImageMIME string          `json:"image_mime,omitempty"` // MIME type for image blocks
	ImageData string          `json:"image_data,omitempty"` // base64-encoded image data
	ToolName  string          `json:"tool_name,omitempty"`
	ToolID    string          `json:"tool_id,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    string          `json:"output,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// ImageBlock creates an image content block with base64-encoded data.
func ImageBlock(mime, base64Data string) ContentBlock {
	return ContentBlock{Type: "image", ImageMIME: mime, ImageData: base64Data}
}

// TextBlock creates a text content block.
func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// ToolUseBlock creates a tool call content block.
func ToolUseBlock(id, name string, input json.RawMessage) ContentBlock {
	return ContentBlock{Type: "tool_use", ToolID: id, ToolName: name, Input: input}
}

// ToolResultBlock creates a tool result content block.
func ToolResultBlock(id, output string, isError bool) ContentBlock {
	return ContentBlock{Type: "tool_result", ToolID: id, Output: output, IsError: isError}
}

// TokenUsage records token consumption for a single API call.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read_tokens"`
	CacheWrite   int `json:"cache_write_tokens"`
}

// StreamEvent is sent over a channel during streaming responses.
type StreamEvent struct {
	Type    StreamEventType
	Text    string        // for TextChunk
	Tool    ToolCallDelta // for ToolCallChunk / ToolCallDone
	Result  string        // for ToolResult
	IsError bool          // for ToolResult
	Usage   *TokenUsage   // for Done (nil if not final)
	Error   error         // for Error
}

type StreamEventType int

const (
	StreamEventText StreamEventType = iota
	StreamEventToolCallChunk
	StreamEventToolCallDone
	StreamEventToolResult
	StreamEventDone
	StreamEventError
)

// ToolCallDelta represents a (possibly partial) tool call from a streaming response.
type ToolCallDelta struct {
	ID        string          // tool call ID (stable across chunks)
	Index     int             // position in the tool call list
	Name      string          // tool name (may be empty in early chunks)
	Arguments json.RawMessage // accumulated arguments so far
}

// ToolDefinition describes a tool to the LLM provider.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

// Provider is the interface every LLM backend must implement.
type Provider interface {
	// Name returns the provider identifier (e.g., "anthropic", "openai", "gemini").
	Name() string

	// Chat sends a non-streaming request and returns the complete response.
	// Used for token counting, summarization, and cost estimation.
	Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error)

	// ChatStream sends a streaming request and returns a channel of events.
	// The channel is closed when the stream ends.
	ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error)

	// CountTokens returns the token count for the given messages.
	// Returns an error if the provider does not support counting.
	CountTokens(ctx context.Context, messages []Message) (int, error)
}

// ChatResponse is the complete response from a non-streaming Chat call.
type ChatResponse struct {
	Message Message
	Usage   TokenUsage
}
