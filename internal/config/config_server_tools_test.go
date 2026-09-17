package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestServerToolConfigYAMLRoundTrip(t *testing.T) {
	src := `
vendors:
  openai:
    protocol: openai-responses
    endpoints:
      default:
        model: gpt-5.2
        server_tools:
          - type: code_interpreter
            memory_limit: 4g
            file_ids: [file-1, file-2]
          - type: file_search
            vector_store_ids: [vs_1, vs_2]
            max_num_results: 8
`
	cfg, err := Load(writeTemp(t, src))
	if err != nil {
		t.Fatal(err)
	}
	eps := cfg.Vendors["openai"].Endpoints["default"].ServerTools
	if len(eps) != 2 {
		t.Fatalf("expected 2 server tools, got %d: %+v", len(eps), eps)
	}
	ci := eps[0]
	if ci.Type != "code_interpreter" || ci.MemoryLimit != "4g" || len(ci.FileIDs) != 2 {
		t.Errorf("code_interpreter parse wrong: %+v", ci)
	}
	fs := eps[1]
	if fs.Type != "file_search" || len(fs.VectorStoreIDs) != 2 || fs.MaxNumResults != 8 {
		t.Errorf("file_search parse wrong: %+v", fs)
	}

	// JSON marshaling must omit zero-valued parameters so simple
	// declarations (Anthropic-style {type: ...}) stay byte-identical.
	b, _ := json.Marshal(ServerToolConfig{Type: "web_search_20250305"})
	if string(b) != `{"type":"web_search_20250305"}` {
		t.Errorf("simple declaration not omitempty: %s", b)
	}
	b2, _ := json.Marshal(fs)
	if !strings.Contains(string(b2), `"max_num_results":8`) {
		t.Errorf("file_search JSON wrong: %s", b2)
	}
}
