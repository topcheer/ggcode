package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// E2E tests: real LLM provider calls tools.
// Set ZAI_API_KEY or GGCODE_ZAI_API_KEY to run; otherwise tests are skipped.

const (
	e2eAnthropicBaseURL = "https://open.bigmodel.cn/api/anthropic"
	e2eDefaultModel     = "glm-5-turbo"
)

func e2eAPIKey() string {
	key := os.Getenv("ZAI_API_KEY")
	if key == "" {
		key = os.Getenv("GGCODE_ZAI_API_KEY")
	}
	return key
}

func e2eModel() string {
	m := os.Getenv("ZAI_MODEL")
	if m == "" {
		m = e2eDefaultModel
	}
	return m
}

// e2eProvider creates a real Anthropic-compatible provider for E2E tests.
func e2eProvider(t *testing.T) provider.Provider {
	t.Helper()
	key := e2eAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping E2E test")
	}
	return provider.NewAnthropicProviderWithBaseURL(key, e2eModel(), 1024, e2eAnthropicBaseURL)
}

// e2eRegistry creates a tool registry with common file tools, scoped to dir.
func e2eRegistry(t *testing.T, dir string) *Registry {
	t.Helper()
	reg := NewRegistry()
	sandbox := func(string) bool { return true } // allow all in tests

	if err := reg.Register(ReadFile{SandboxCheck: sandbox}); err != nil {
		t.Fatalf("register read_file: %v", err)
	}
	if err := reg.Register(WriteFile{SandboxCheck: sandbox}); err != nil {
		t.Fatalf("register write_file: %v", err)
	}
	if err := reg.Register(ListDir{SandboxCheck: sandbox}); err != nil {
		t.Fatalf("register list_directory: %v", err)
	}
	if err := reg.Register(Glob{SandboxCheck: sandbox}); err != nil {
		t.Fatalf("register glob: %v", err)
	}
	if err := reg.Register(Grep{SandboxCheck: sandbox}); err != nil {
		t.Fatalf("register grep: %v", err)
	}
	if err := reg.Register(SearchFiles{SandboxCheck: sandbox}); err != nil {
		t.Fatalf("register search_files: %v", err)
	}
	return reg
}

// e2eCallToolLoop sends a message to the LLM, executes any tool calls,
// and returns the final text response. Max 5 tool rounds to prevent infinite loops.
func e2eCallToolLoop(ctx context.Context, t *testing.T, prov provider.Provider, reg *Registry, userPrompt string) string {
	t.Helper()
	messages := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(userPrompt)}},
	}

	defs := reg.ToDefinitions()

	for round := 0; round < 5; round++ {
		resp, err := prov.Chat(ctx, messages, defs)
		if err != nil {
			t.Fatalf("LLM chat round %d: %v", round, err)
		}

		// Collect tool calls and text from response
		var toolCalls []provider.ContentBlock
		var textParts []string
		for _, block := range resp.Message.Content {
			switch block.Type {
			case "text":
				textParts = append(textParts, block.Text)
			case "tool_use":
				toolCalls = append(toolCalls, block)
			}
		}

		// If no tool calls, return the text
		if len(toolCalls) == 0 {
			return strings.Join(textParts, "")
		}

		// Append assistant message with tool calls
		messages = append(messages, resp.Message)

		// Execute each tool call and append results
		for _, tc := range toolCalls {
			tool, ok := reg.Get(tc.ToolName)
			if !ok {
				messages = append(messages, provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{
						provider.ToolResultBlock(tc.ToolID, fmt.Sprintf("unknown tool: %s", tc.ToolName), true),
					},
				})
				continue
			}

			result, err := tool.Execute(ctx, tc.Input)
			if err != nil {
				messages = append(messages, provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{
						provider.ToolResultBlock(tc.ToolID, fmt.Sprintf("execution error: %v", err), true),
					},
				})
				continue
			}

			messages = append(messages, provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{
					provider.ToolResultBlock(tc.ToolID, result.Content, result.IsError),
				},
			})
		}
	}

	return "max tool rounds reached"
}

// --- E2E Tests ---

func TestE2EWriteAndReadFile(t *testing.T) {
	prov := e2eProvider(t)
	dir := t.TempDir()
	reg := e2eRegistry(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Use the write_file tool to create a file at %s/greeting.txt with the content 'Hello from E2E test!'. "+
			"Then use the read_file tool to read it back and tell me what the file contains.",
		dir)

	result := e2eCallToolLoop(ctx, t, prov, reg, prompt)

	// Verify file was actually created
	data, err := os.ReadFile(filepath.Join(dir, "greeting.txt"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if !containsStr(string(data), "Hello from E2E test!") {
		t.Errorf("file content mismatch: %q", string(data))
	}

	// Verify LLM read the content
	if !containsStr(result, "Hello from E2E test") {
		t.Errorf("LLM did not report file content correctly: %s", result)
	}
	t.Logf("LLM response: %s", result)
}

func TestE2EListDirAndGlob(t *testing.T) {
	prov := e2eProvider(t)
	dir := t.TempDir()
	reg := e2eRegistry(t, dir)

	// Pre-create files
	os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "util.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Test\n"), 0o644)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Use the list_directory tool to list files in %s. "+
			"Then use the glob tool with pattern '*.go' in directory %s to find Go files. "+
			"Tell me how many Go files you found and list their names.",
		dir, dir)

	result := e2eCallToolLoop(ctx, t, prov, reg, prompt)

	// LLM should mention both .go files
	if !containsStr(result, "app.go") || !containsStr(result, "util.go") {
		t.Errorf("LLM did not list Go files: %s", result)
	}
	t.Logf("LLM response: %s", result)
}

func TestE2ESearchAndGrep(t *testing.T) {
	prov := e2eProvider(t)
	dir := t.TempDir()
	reg := e2eRegistry(t, dir)

	// Create files with known content
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello world\")\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "handler.go"), []byte("package main\n\nfunc handleRequest() {\n\tfmt.Println(\"handling request\")\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "utils.txt"), []byte("some utility text\n"), 0o644)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Use the search_files tool to search for pattern 'handleRequest' in directory %s. "+
			"Then use the grep tool to search for pattern 'Println' in directory %s with type 'go'. "+
			"Tell me which files contain these patterns.",
		dir, dir)

	result := e2eCallToolLoop(ctx, t, prov, reg, prompt)

	if !containsStr(result, "handler.go") {
		t.Errorf("LLM did not find handler.go: %s", result)
	}
	t.Logf("LLM response: %s", result)
}

func TestE2EMultipleToolRounds(t *testing.T) {
	prov := e2eProvider(t)
	dir := t.TempDir()
	reg := e2eRegistry(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Perform the following steps using the available tools:\n"+
			"1. Use write_file to create %s/step1.txt with content 'step one done'\n"+
			"2. Use write_file to create %s/step2.txt with content 'step two done'\n"+
			"3. Use read_file to read both files and confirm their contents\n"+
			"4. Use list_directory to verify both files exist\n"+
			"Report the final status of all steps.",
		dir, dir)

	result := e2eCallToolLoop(ctx, t, prov, reg, prompt)

	// Verify both files exist
	for _, f := range []string{"step1.txt", "step2.txt"} {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Errorf("file %s not created: %v", f, err)
			continue
		}
		t.Logf("File %s content: %q", f, string(data))
	}

	if !containsStr(result, "step") {
		t.Errorf("LLM response seems empty or irrelevant: %s", result)
	}
	t.Logf("LLM response: %s", result)
}

func TestE2EToolError(t *testing.T) {
	prov := e2eProvider(t)
	dir := t.TempDir()
	reg := e2eRegistry(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Try to use read_file to read the file at %s/nonexistent_file.txt. "+
			"This file does not exist. Report what error you get.",
		dir)

	result := e2eCallToolLoop(ctx, t, prov, reg, prompt)

	// LLM should report an error about the missing file
	if !containsStr(result, "nonexistent") && !containsStr(result, "error") && !containsStr(result, "not found") && !containsStr(result, "does not exist") {
		t.Errorf("LLM did not report error for missing file: %s", result)
	}
	t.Logf("LLM response: %s", result)
}

func TestE2EWriteSpecialContent(t *testing.T) {
	prov := e2eProvider(t)
	dir := t.TempDir()
	reg := e2eRegistry(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Use the write_file tool to create a file at %s/multiline.txt with exactly this content (including newlines):\n"+
			"---BEGIN---\n"+
			"Line 1: Hello\n"+
			"Line 2: World\n"+
			"Line 3: 测试中文\n"+
			"Line 4: Special chars: @#$%%\n"+
			"---END---\n"+
			"Then read the file back and confirm all lines are correct.",
		dir)

	result := e2eCallToolLoop(ctx, t, prov, reg, prompt)

	data, err := os.ReadFile(filepath.Join(dir, "multiline.txt"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	content := string(data)
	t.Logf("File content: %q", content)

	// Check at least some key content survived
	if !containsStr(content, "Hello") || !containsStr(content, "World") {
		t.Errorf("file content missing expected text: %q", content)
	}
	t.Logf("LLM response: %s", result)
}

// TestE2EStreamingToolCall verifies that streaming also works for tool calls.
func TestE2EStreamingToolCall(t *testing.T) {
	prov := e2eProvider(t)
	dir := t.TempDir()
	reg := e2eRegistry(t, dir)

	// Pre-create a file
	os.WriteFile(filepath.Join(dir, "data.txt"), []byte("The answer is 42"), 0o644)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			provider.TextBlock(fmt.Sprintf("Read the file at %s/data.txt using the read_file tool and tell me its content.", dir)),
		}},
	}

	defs := reg.ToDefinitions()

	stream, err := prov.ChatStream(ctx, messages, defs)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	// Collect streaming events
	var textParts []string
	var toolCalls []provider.ContentBlock
	var toolInputMap = map[string][]byte{}
	var toolNameMap = map[string]string{}

	for ev := range stream {
		switch ev.Type {
		case provider.StreamEventText:
			textParts = append(textParts, ev.Text)
		case provider.StreamEventToolCallChunk:
			id := ev.Tool.ID
			if ev.Tool.Name != "" {
				toolNameMap[id] = ev.Tool.Name
			}
			toolInputMap[id] = append(toolInputMap[id], ev.Tool.Arguments...)
		case provider.StreamEventToolCallDone:
			id := ev.Tool.ID
			name := toolNameMap[id]
			if name == "" {
				name = ev.Tool.Name
			}
			input := toolInputMap[id]
			if len(ev.Tool.Arguments) > 0 {
				input = ev.Tool.Arguments // use final accumulated args
			}
			toolCalls = append(toolCalls, provider.ToolUseBlock(id, name, input))
		case provider.StreamEventError:
			t.Fatalf("stream error: %v", ev.Error)
		}
	}

	if len(toolCalls) == 0 {
		// LLM might have answered directly without tool call
		t.Logf("LLM answered without tool call: %s", strings.Join(textParts, ""))
		return
	}

	// Execute the tool call
	for _, tc := range toolCalls {
		tool, ok := reg.Get(tc.ToolName)
		if !ok {
			t.Fatalf("unknown tool: %s", tc.ToolName)
		}
		result, err := tool.Execute(ctx, tc.Input)
		if err != nil {
			t.Fatalf("tool execution: %v", err)
		}
		if result.IsError {
			t.Fatalf("tool error: %s", result.Content)
		}
		if !containsStr(result.Content, "42") {
			t.Errorf("tool did not return expected content: %s", result.Content)
		}
		t.Logf("Tool %s result: %s", tc.ToolName, result.Content)
	}
}

// TestE2EJSONRoundtrip tests that tool JSON parameters survive the LLM round-trip.
func TestE2EJSONRoundtrip(t *testing.T) {
	prov := e2eProvider(t)
	dir := t.TempDir()
	reg := e2eRegistry(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Use write_file to create %s/config.json with this exact JSON content:\n"+
			"{\"name\": \"test\", \"version\": 1, \"enabled\": true}\n"+
			"Then read it back and confirm the JSON is valid.",
		dir)

	result := e2eCallToolLoop(ctx, t, prov, reg, prompt)

	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Errorf("file content is not valid JSON: %q — %v", string(data), err)
	}
	t.Logf("LLM response: %s", result)
}
