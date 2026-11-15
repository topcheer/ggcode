package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #5655: an explicit host:port target must reach the peer WITHOUT any
// discovery roster (pipe/headless sessions browse no mDNS) and carry the
// self-declared workspace header.
func TestIntrospectToolDirectEndpointArm(t *testing.T) {
	var gotWorkspace string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotWorkspace = r.Header.Get("X-Ggcode-Workspace")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"status":{"mode":"idle","goal":"g","iteration":3}}}`)
	}))
	defer ts.Close()

	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	tl := NewIntrospectTool(reg, "", "/self/ws")
	out, err := tl.Execute(context.Background(), mustJSON(t, map[string]any{
		"target": strings.TrimPrefix(ts.URL, "http://"),
		"depth":  "brief",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if out.IsError {
		t.Fatalf("direct-endpoint call failed: %s", out.Content)
	}
	if gotWorkspace != "/self/ws" {
		t.Fatalf("X-Ggcode-Workspace = %q, want /self/ws", gotWorkspace)
	}
	if !strings.Contains(out.Content, "g") {
		t.Fatalf("snapshot goal missing from output: %.200s", out.Content)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
