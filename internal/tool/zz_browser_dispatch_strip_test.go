package tool

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/util"
)

// The real dispatch path (agent.executeToolInner, internal/agent/agent_tool.go
// "strippedArgs := tool.StripUnknownParams(t.Parameters(), tc.Arguments)")
// removes every key the tool schema does not declare BEFORE Execute runs.
// Since the browser schema has no "profile" property, an explicit "profile"
// never reaches resolveProfile through that path - it is dropped, and the
// call still runs in the session's profile. Execute's own rejection is the
// second line of defence for callers that bypass the dispatch layer.
func TestBrowserDispatchPath_StripsExplicitProfileBeforeExecute(t *testing.T) {
	b := NewBrowser()
	schema := b.Parameters()

	raw := json.RawMessage(`{"action":"status","profile":"rejection-check-dispatch"}`)
	stripped := StripUnknownParams(schema, raw)

	var args browserActionArgs
	if err := json.Unmarshal(stripped, &args); err != nil {
		t.Fatalf("stripped args are not JSON: %v", err)
	}
	if args.Profile != "" {
		t.Fatalf("dispatch must strip the undeclared profile key before Execute, got %q (args=%s)", args.Profile, stripped)
	}

	// What the stripped call resolves to: the session's profile, never the
	// requested name.
	ctx := util.WithSessionID(t.Context(), "20261127-101500-00b389147c828ccc")
	got, err := b.resolveProfile(ctx, args.Profile, args.SharedProfile)
	if err != nil || got != "sess-20261127-101500-00b389147c828ccc" {
		t.Fatalf("stripped call must resolve to the session profile, got %q, %v", got, err)
	}

	// The same raw args fed straight to Execute (bypassing the dispatch strip)
	// are refused - the second line of defence.
	if _, err := b.resolveProfile(ctx, "rejection-check-dispatch", false); err == nil {
		t.Fatal("an explicit profile that does reach Execute must be rejected")
	}
}
