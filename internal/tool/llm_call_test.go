package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// fakeLLMProvider records the last request and returns a scripted reply.
type fakeLLMProvider struct {
	mu       sync.Mutex
	name     string
	replies  []string // popped per Chat call
	gotMsgs  []provider.Message
	chats    int
	gotTools []provider.ToolDefinition
	chatErr  error
}

func (f *fakeLLMProvider) Name() string { return f.name }
func (f *fakeLLMProvider) ChatStream(_ context.Context, _ []provider.Message, _ []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeLLMProvider) CountTokens(_ context.Context, _ []provider.Message) (int, error) {
	return 0, nil
}
func (f *fakeLLMProvider) Chat(_ context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chats++
	f.gotMsgs = append(f.gotMsgs, msgs...)
	f.gotTools = tools
	if f.chatErr != nil {
		return nil, f.chatErr
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	return &provider.ChatResponse{Message: provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{provider.TextBlock(reply)},
	}}, nil
}

// clonableFake proves the model override path uses CloneProviderWithModel.
type clonableFake struct {
	*fakeLLMProvider
	clonedModel string
}

func (c *clonableFake) CloneWithModel(model string) provider.Provider {
	c.clonedModel = model
	return c
}

func lastUserMsg(f *fakeLLMProvider) provider.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotMsgs[len(f.gotMsgs)-1]
}

func TestLLMCall_PlainPromptPassthrough(t *testing.T) {
	f := &fakeLLMProvider{name: "fake", replies: []string{"Bonjour"}}
	res, err := LLMCallTool{Provider: f}.Execute(context.Background(),
		json.RawMessage(`{"prompt":"translate to French: hello"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if res.Content != "Bonjour" {
		t.Fatalf("want model reply as content, got %q", res.Content)
	}
	msg := lastUserMsg(f)
	if msg.Role != "user" || len(msg.Content) != 1 || msg.Content[0].Text != "translate to French: hello" {
		t.Fatalf("prompt must be the sole user block, got %+v", msg)
	}
	if f.gotTools != nil {
		t.Fatal("inner call must pass zero tools (bare completion)")
	}
	if len(f.gotMsgs) != 1 {
		t.Fatalf("no system message expected without system/schema, got %d msgs", len(f.gotMsgs))
	}
}

func TestLLMCall_SystemInjectedAndEmptyPromptRejected(t *testing.T) {
	f := &fakeLLMProvider{name: "fake", replies: []string{"ok"}}
	res, _ := LLMCallTool{Provider: f}.Execute(context.Background(),
		json.RawMessage(`{"prompt":"hi","system":"You are a translator."}`))
	if res.IsError {
		t.Fatalf("system call failed: %s", res.Content)
	}
	f.mu.Lock()
	first := f.gotMsgs[0]
	f.mu.Unlock()
	if first.Role != "system" || !strings.Contains(first.Content[0].Text, "translator") {
		t.Fatalf("system message must be injected first, got %+v", first)
	}

	res, _ = LLMCallTool{Provider: f}.Execute(context.Background(), json.RawMessage(`{"prompt":"   "}`))
	if !res.IsError || !strings.Contains(res.Content, "required") {
		t.Fatalf("empty prompt must yield IsError, got %+v", res)
	}
}

func TestLLMCall_ModelOverrideUsesClone(t *testing.T) {
	cf := &clonableFake{fakeLLMProvider: &fakeLLMProvider{name: "fake", replies: []string{"x"}}}
	res, _ := LLMCallTool{Provider: cf}.Execute(context.Background(),
		json.RawMessage(`{"prompt":"p","model":"glm-4.5-air"}`))
	if res.IsError {
		t.Fatalf("err: %s", res.Content)
	}
	if cf.clonedModel != "glm-4.5-air" {
		t.Fatalf("expected CloneWithModel(glm-4.5-air), got %q", cf.clonedModel)
	}
}

func TestLLMCall_ImagePathAttachedAsImageBlock(t *testing.T) {
	dir := t.TempDir()
	// Minimal valid PNG (1x1).
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	p := filepath.Join(dir, "img.png")
	if err := os.WriteFile(p, png, 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeLLMProvider{name: "fake", replies: []string{"a cat"}}
	res, _ := LLMCallTool{Provider: f, WorkingDir: dir}.Execute(context.Background(),
		json.RawMessage(`{"prompt":"describe","image_paths":["img.png"]}`))
	if res.IsError {
		t.Fatalf("err: %s", res.Content)
	}
	blocks := lastUserMsg(f).Content
	if len(blocks) != 2 || blocks[1].Type != "image" || blocks[1].ImageMIME != "image/png" {
		t.Fatalf("expected text+image blocks, got %+v", blocks)
	}
	if blocks[1].ImageData != base64.StdEncoding.EncodeToString(png) {
		t.Fatal("image data must be base64 of file contents")
	}
	// Unsupported extension must fail cleanly.
	res, _ = LLMCallTool{Provider: f, WorkingDir: dir}.Execute(context.Background(),
		json.RawMessage(`{"prompt":"x","image_paths":["img.bmp"]}`))
	if !res.IsError {
		t.Fatal("unsupported image type must be rejected")
	}
}

func TestLLMCall_SchemaModeValidatesAndReasks(t *testing.T) {
	f := &fakeLLMProvider{name: "fake", replies: []string{
		"```json\n{\"title\": 42}\n```", // fenced but wrong type -> re-ask
		`not json at all`,               // invalid -> re-ask
		`{"title": "ok", "count": 3}`,
	}}
	res, _ := LLMCallTool{Provider: f}.Execute(context.Background(), json.RawMessage(
		`{"prompt":"extract","schema":{"type":"object","required":["title","count"],`+
			`"properties":{"title":{"type":"string"},"count":{"type":"integer"}}}}`))
	if res.IsError {
		t.Fatalf("third attempt should validate: %s", res.Content)
	}
	f.mu.Lock()
	n := f.chats
	f.mu.Unlock()
	if n != 3 {
		t.Fatalf("expected 3 Chat calls (2 re-asks), got %d", n)
	}
}

func TestLLMCall_SchemaModeFailsAfterMaxAttempts(t *testing.T) {
	f := &fakeLLMProvider{name: "fake", replies: []string{"nope", "still nope", `{"wrong": 1}`}}
	res, _ := LLMCallTool{Provider: f}.Execute(context.Background(), json.RawMessage(
		`{"prompt":"x","schema":{"type":"object","required":["title"]}}`))
	if !res.IsError || !strings.Contains(res.Content, "3 attempts") {
		t.Fatalf("expected IsError after 3 attempts, got %+v", res)
	}
}

func TestLLMCall_ProviderErrorPropagates(t *testing.T) {
	f := &fakeLLMProvider{name: "fake", chatErr: errors.New("boom")}
	_, err := LLMCallTool{Provider: f}.Execute(context.Background(), json.RawMessage(`{"prompt":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("provider error must propagate, got %v", err)
	}
}

func TestLLMCall_ConcurrentCallsSafe(t *testing.T) {
	replies := make([]string, 32)
	for i := range replies {
		replies[i] = "r"
	}
	f := &fakeLLMProvider{name: "fake", replies: replies}
	tool := LLMCallTool{Provider: f}
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"p"}`))
			if err != nil || res.IsError {
				errs <- errors.New(res.Content)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent call failed: %v", e)
	}
}
