package tool

// Regression tests for issue #568 (tool batch fixes).
// Bug A (GUI SIGKILL) is covered by zz_issue568_unix_test.go.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// --- Bug F: explicit empty containers are provided, not missing ---

func TestEmptyArrayRequiredNotMissing(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"todos": {"type": "array", "items": {"type": "object"}}},
		"required": ["todos"]
	}`)
	// The todo_write clear operation: "Existing todos not in this list are
	// removed" — [] is the legitimate clearing value.
	if msg := ValidateRequiredParams(schema, json.RawMessage(`{"todos":[]}`)); msg != "" {
		t.Fatalf("explicit [] must be treated as provided, got: %s", msg)
	}
	if msg := isEmptyValue(json.RawMessage(`[]`)); msg {
		t.Fatal("isEmptyValue([]) must be false — explicit empty array is provided")
	}
	if msg := isEmptyValue(json.RawMessage(`{}`)); msg {
		t.Fatal("isEmptyValue({}) must be false — explicit empty object is provided")
	}
	// Guards: key absent and null are still missing.
	if msg := ValidateRequiredParams(schema, json.RawMessage(`{}`)); msg == "" {
		t.Fatal("absent required field must still be missing")
	}
	if msg := ValidateRequiredParams(schema, json.RawMessage(`{"todos":null}`)); msg == "" {
		t.Fatal("null required field must still be missing")
	}
	if msg := ValidateRequiredParams(schema, json.RawMessage(`{"todos":""}`)); msg == "" {
		t.Fatal("empty string required field must still be missing")
	}
}

// --- Bug D: ~ expansion ---

func TestExpandHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory available")
	}
	cases := map[string]string{
		"~":             home,
		"~/":            home,
		"~/notes.txt":   filepath.Join(home, "notes.txt"),
		"~/a/b/c.txt":   filepath.Join(home, "a", "b", "c.txt"),
		"~other/x":      "~other/x", // another user's home — untouched
		"~user":         "~user",
		"relative.txt":  "relative.txt",
		"/abs/path.txt": "/abs/path.txt",
		"":              "",
	}
	for in, want := range cases {
		if got := expandHomePath(in); got != want {
			t.Errorf("expandHomePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveToolPathTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory available")
	}
	got, err := resolveToolPath("~/notes.txt", "/tmp/workdir")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "notes.txt"); got != want {
		t.Fatalf("resolveToolPath(~) = %q, want %q (must not join into workdir as literal ~ dir)", got, want)
	}
}

func TestWriteFileTildePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory available")
	}
	name := filepath.Join(home, ".ggcode-issue568-tilde-test.txt")
	defer os.Remove(name)

	tool := WriteFile{WorkingDir: t.TempDir()}
	input, _ := json.Marshal(map[string]string{
		"path":        "~/.ggcode-issue568-tilde-test.txt",
		"content":     "issue568",
		"description": "test",
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("write_file failed: %s", res.Content)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("file not written under $HOME: %v", err)
	}
	if string(data) != "issue568" {
		t.Fatalf("unexpected content: %q", data)
	}
	// No literal "~" directory may appear in the working dir.
	if _, err := os.Stat(filepath.Join(tool.WorkingDir, "~")); !os.IsNotExist(err) {
		t.Error("literal '~' directory was created under working dir")
	}
}

func TestFileOpsTildePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory available")
	}
	base := filepath.Join(home, ".ggcode-issue568-fileops")
	defer os.RemoveAll(base)
	if err := os.MkdirAll(filepath.Join(base, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	srcFile := filepath.Join(base, "src", "f.txt")
	if err := os.WriteFile(srcFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := FileOps{WorkingDir: t.TempDir()}
	input, _ := json.Marshal(map[string]interface{}{
		"operations": []map[string]string{
			{"action": "move", "source": "~/.ggcode-issue568-fileops/src/f.txt", "destination": "~/.ggcode-issue568-fileops/dst/f.txt"},
		},
		"description": "test",
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("file_ops failed: %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(base, "dst", "f.txt")); err != nil {
		t.Fatalf("file not moved under $HOME: %v", err)
	}
	if _, err := os.Stat(srcFile); !os.IsNotExist(err) {
		t.Error("source still exists after move")
	}
	// No literal "~" directory in working dir.
	if _, err := os.Stat(filepath.Join(tool.WorkingDir, "~")); !os.IsNotExist(err) {
		t.Error("literal '~' directory was created under working dir")
	}
}

// --- Bug C: read_mcp_resource rune-boundary-safe truncation ---

type issue568FakeRuntime struct{ text string }

func (f issue568FakeRuntime) SnapshotMCP() []MCPServerSnapshot { return nil }
func (f issue568FakeRuntime) GetPrompt(ctx context.Context, server, name string, args map[string]interface{}) (*MCPPromptResult, error) {
	return nil, nil
}
func (f issue568FakeRuntime) ReadResource(ctx context.Context, server, uri string) (*MCPResourceResult, error) {
	return &MCPResourceResult{Contents: []MCPResourceContent{{URI: uri, Text: f.text}}}, nil
}

func TestReadMCPResourceRuneBoundary(t *testing.T) {
	// 20000 CJK runes = 60000 bytes > 50KB cap. A byte-slice cut at 51200
	// splits a 3-byte rune and produced U+FFFD shards (#568).
	text := strings.Repeat("汉", 20000)
	tool := ReadMCPResourceTool{Runtime: issue568FakeRuntime{text: text}}
	input, _ := json.Marshal(map[string]string{"server": "s", "uri": "test://r"})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !utf8.ValidString(res.Content) {
		t.Fatal("truncated MCP resource is not valid UTF-8 — rune boundary was split")
	}
	if strings.ContainsRune(res.Content, 0xFFFD) {
		t.Fatal("truncated MCP resource contains U+FFFD replacement runes")
	}
	if !strings.Contains(res.Content, "[... MCP resource truncated") {
		t.Fatal("truncation notice missing")
	}
}

// --- Bug E: nested schema-aware coercion ---

func TestCoerceNestedValues(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"items": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"id":    {"type": "integer"},
						"ratio": {"type": "number"},
						"on":    {"type": "boolean"}
					}
				}
			},
			"opts": {
				"type": "object",
				"properties": {
					"depth": {"type": "integer"},
					"force": {"type": "boolean"}
				}
			}
		}
	}`)
	args := json.RawMessage(`{"items":[{"id":"7","ratio":"1.5","on":"true"},{"id":"9","on":"no"}],"opts":{"depth":"3","force":"yes"}}`)
	out := CoerceArguments(schema, args)

	var got struct {
		Items []struct {
			ID    int     `json:"id"`
			Ratio float64 `json:"ratio"`
			On    bool    `json:"on"`
		} `json:"items"`
		Opts struct {
			Depth int  `json:"depth"`
			Force bool `json:"force"`
		} `json:"opts"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("coerced args no longer unmarshal into typed struct (coercion failed): %v\n%s", err, out)
	}
	if len(got.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got.Items))
	}
	if got.Items[0].ID != 7 || got.Items[0].Ratio != 1.5 || !got.Items[0].On {
		t.Errorf("item[0] not coerced: %+v", got.Items[0])
	}
	if got.Items[1].ID != 9 || got.Items[1].On {
		t.Errorf("item[1] not coerced: %+v", got.Items[1])
	}
	if got.Opts.Depth != 3 || !got.Opts.Force {
		t.Errorf("opts not coerced: %+v", got.Opts)
	}
}

func TestCoerceNestedUnchangedWhenTyped(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"list": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {"n": {"type": "integer"}}
				}
			}
		}
	}`)
	args := json.RawMessage(`{"list":[{"n":1},{"n":"2"}]}`)
	out := CoerceArguments(schema, args)
	// Only the string-encoded element changes; already-typed values keep
	// their exact JSON representation.
	want := `{"list":[{"n":1},{"n":2}]}`
	if got := string(out); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
