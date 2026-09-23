package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
)

// --- sa-104 coverage net: session store pure helpers, endpoint stats
// matrix, export, search, locks, backfill. Zero production-code changes. ---

func sa104UserMsg(text string) provider.Message {
	return provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

func sa104WriteSessionFile(t *testing.T, dir, id string, recs []jsonlRecord) string {
	t.Helper()
	path := filepath.Join(dir, id+".jsonl")
	var sb strings.Builder
	for _, r := range recs {
		r.SessionID = id
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- pure byte-scan helpers (store.go:258-506) ---

func TestSa104_QuickRecordType_Table(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{`{"type":"message","message":{}}`, "message"},
		{`{"type":"meta"}`, "meta"},
		{`{"type":"usage"}`, "usage"},
		{`{"other":1}`, ""},
		{`{"type":`, ""}, // no closing quote
		{`{}`, ""},
	}
	for _, tc := range cases {
		if got := quickRecordType([]byte(tc.line)); got != tc.want {
			t.Errorf("quickRecordType(%q)=%q want %q", tc.line, got, tc.want)
		}
	}
}

func TestSa104_QuickIsDialogueRole_Table(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`{"type":"message","message":{"role":"user"}}`, true},
		{`{"message":{"role":"assistant"}}`, true},
		{`{"message":{"role":"system"}}`, false},
		{`{"message":{}}`, false},
		{`{"foo":1}`, false},
	}
	for _, tc := range cases {
		if got := quickIsDialogueRole([]byte(tc.line)); got != tc.want {
			t.Errorf("quickIsDialogueRole(%q)=%t want %t", tc.line, got, tc.want)
		}
	}
}

func TestSa104_QuickExtractTimestamp_NestedPseudoTS(t *testing.T) {
	// #558C: a pseudo-timestamp inside tool_use input must NOT be picked up.
	nested := `{"type":"message","message":{"role":"user","content":[{"type":"tool_use","input":{"timestamp":"2099-01-01T00:00:00Z"}}]},"timestamp":"2026-06-15T10:00:00Z"}`
	ts := quickExtractTimestamp([]byte(nested))
	if ts.Year() != 2026 || ts.Month() != time.June {
		t.Fatalf("must read the top-level timestamp, got %v", ts)
	}
	// Short value, bad format, missing → zero time.
	for _, line := range []string{
		`{"timestamp":"x"}`,
		`{"timestamp":"2026-13-99T99:99:99Z"}`,
		`{"nope":1}`,
	} {
		if !quickExtractTimestamp([]byte(line)).IsZero() {
			t.Errorf("expected zero time for %q", line)
		}
	}
}

func TestSa104_TopLevelStringField_Table(t *testing.T) {
	cases := []struct {
		line    string
		name    string
		wantS   int
		wantE   int
		wantVal string
	}{
		{`{"title":"hello","n":1}`, "title", 10, 15, "hello"},
		{`{"a":{"title":"nested"}}`, "title", -1, -1, ""},                  // depth-2 key ignored
		{`{"title":"has \"quotes\""} `, "title", 10, 24, `has \"quotes\"`}, // escaped quotes in value
		{`{"title":123}`, "title", -1, -1, ""},                             // non-string value
		{`{"title":"unterminated`, "title", -1, -1, ""},                    // unterminated value string
		{`{"msg":"mentions \"title\" inside"}`, "title", -1, -1, ""},       // key name inside string value
		{`{"workspace":"/tmp/x","title":"t"}`, "title", 31, 32, "t"},       // later key
	}
	for _, tc := range cases {
		s, e := topLevelStringField([]byte(tc.line), tc.name)
		if s != tc.wantS || e != tc.wantE {
			t.Errorf("topLevelStringField(%q, %q)=(%d,%d) want (%d,%d)", tc.line, tc.name, s, e, tc.wantS, tc.wantE)
			continue
		}
		if tc.wantVal != "" && string([]byte(tc.line)[s:e]) != tc.wantVal {
			t.Errorf("value mismatch for %q: got %q", tc.line, tc.line[s:e])
		}
	}
}

func TestSa104_FindMessageCutoff_Edges(t *testing.T) {
	// Missing file.
	off, n, ts := findMessageCutoff(filepath.Join(t.TempDir(), "missing.jsonl"))
	if off != 0 || n != 0 || !ts.IsZero() {
		t.Fatalf("missing file must be (0,0,zero), got (%d,%d,%v)", off, n, ts)
	}
	// Below threshold → load all.
	dir := t.TempDir()
	path := sa104WriteSessionFile(t, dir, "small", []jsonlRecord{
		{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}}, Timestamp: time.Now().Add(-time.Hour)},
		{Type: "message", Message: &provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}}, Timestamp: time.Now()},
	})
	off, n, ts = findMessageCutoff(path)
	if off != 0 || n != 2 || !ts.IsZero() {
		t.Fatalf("small session must be (0,2,zero), got (%d,%d,%v)", off, n, ts)
	}
}

func TestSa104_OrphanAndSummaryClassifiers(t *testing.T) {
	orphanCases := []struct {
		msg  provider.Message
		want bool
	}{
		{provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "t1"}}}, true},
		{provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "tool_use", ToolName: "read"}}}, true},
		{provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "tool_use", ToolName: "read"}, {Type: "tool_result", ToolID: "t1"}}}, true},
		{sa104UserMsg("plain"), false},
	}
	for i, tc := range orphanCases {
		if got := isOrphanToolMessage(tc.msg); got != tc.want {
			t.Errorf("isOrphanToolMessage case %d = %t want %t", i, got, tc.want)
		}
	}

	if isSummaryNoteMessage(nil) {
		t.Error("nil message must not be a summary note")
	}
	if isSummaryNoteMessage(&provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "[Previous conversation summary] x"}}}) {
		t.Error("non-system role must not be a summary note")
	}
	if !isSummaryNoteMessage(&provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "[Previous conversation summary] ..."}}}) {
		t.Error("system summary note not detected")
	}
}

func TestSa104_CapContextTail(t *testing.T) {
	small := []provider.Message{sa104UserMsg("a"), {Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "b"}}}}
	if got := capContextTail(small); len(got) != len(small) {
		t.Fatalf("under-cap input must pass through unchanged, got %d messages", len(got))
	}
	// Over cap: the first message past the window is an orphan assistant
	// tool_use, so the window must shift back one slot to keep the pair.
	big := []provider.Message{
		sa104UserMsg("a"),
		sa104UserMsg("b"),
		sa104UserMsg("c"),
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "tool_use", ToolName: "x"}}},
	}
	for i := 0; i < MaxContextMessages-1; i++ {
		big = append(big, sa104UserMsg("m"))
	}
	got := capContextTail(big)
	// cap window (200) + 1 slot from the orphan shift + 1 truncation note.
	if len(got) != MaxContextMessages+2 {
		t.Fatalf("expected cap+1 (note), got %d", len(got))
	}
	if got[0].Role != "system" || !strings.Contains(got[0].Content[0].Text, "truncated") {
		t.Fatal("truncation note must be prepended")
	}
	if !isOrphanToolMessage(got[2]) {
		t.Fatal("window must shift back to include the orphan tool half-pair")
	}
}

// --- normalizeTags / NormalizeWorkspacePath / messageFingerprint ---

func TestSa104_NormalizeTags(t *testing.T) {
	if got := normalizeTags([]string{"  a ", "", "A", strings.Repeat("x", 40)}); len(got) != 2 || got[0] != "a" || len([]rune(got[1])) != maxTagRunes {
		t.Fatalf("trim/dedupe/truncate failed: %q", got)
	}
	many := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, "t"+string(rune('a'+i)))
	}
	if got := normalizeTags(many); len(got) != maxTagsPerSession {
		t.Fatalf("cap to %d failed: %d", maxTagsPerSession, len(got))
	}
	if got := normalizeTags([]string{"", "   "}); got != nil {
		t.Fatalf("all-empty must return nil, got %q", got)
	}
}

func TestSa104_NormalizeWorkspacePath(t *testing.T) {
	if got := NormalizeWorkspacePath("   "); got != "" {
		t.Fatalf("blank must be empty, got %q", got)
	}
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	got := NormalizeWorkspacePath(missing)
	if !filepath.IsAbs(got) || got != filepath.Clean(missing) {
		t.Fatalf("missing path must fall back to abs+clean, got %q", got)
	}
	existing := t.TempDir()
	wantExisting := filepath.Clean(existing)
	if resolved, err := filepath.EvalSymlinks(existing); err == nil {
		wantExisting = filepath.Clean(resolved)
	}
	if got := NormalizeWorkspacePath(existing + string(filepath.Separator)); got != wantExisting {
		t.Fatalf("symlink/clean resolution failed: %q want %q", got, wantExisting)
	}
}

func TestSa104_MessageFingerprint(t *testing.T) {
	base := provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}}
	f1 := messageFingerprint(&base)
	if f1 != messageFingerprint(&provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}}) {
		t.Fatal("identical messages must share a fingerprint")
	}
	userHi := sa104UserMsg("hi")
	if f1 == messageFingerprint(&userHi) {
		t.Fatal("role must be part of the fingerprint")
	}
	variants := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "tool_use", ToolName: "read", Input: json.RawMessage(`{"p":"a"}`)}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "tool_use", ToolName: "read", Input: json.RawMessage(`{"p":"b"}`)}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolID: "t1", Output: "out"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "custom"}}},
	}
	seen := map[string]bool{f1: true}
	for i, m := range variants {
		f := messageFingerprint(&m)
		if seen[f] {
			t.Errorf("variant %d collides: %q", i, f)
		}
		seen[f] = true
	}
	if messageFingerprint(&variants[0]) == messageFingerprint(&variants[1]) {
		t.Fatal("tool_use input must be part of the fingerprint")
	}
}

// --- endpoint stats matrix (endpoint_stats.go) ---

func TestSa104_EndpointStats_NilReceiversAndEmptyKey(t *testing.T) {
	var s *Session
	s.AddUsageForEndpoint("v", "e", provider.TokenUsage{InputTokens: 1})
	s.AppendMetricForEndpoint("v", "e", metrics.MetricEvent{})
	s.RebuildEndpointStats()
	if u := s.UsageForEndpoint("v", "e"); u != (provider.TokenUsage{}) {
		t.Fatal("nil receiver usage must be zero")
	}
	if m := s.MetricsForEndpoint("v", "e"); m != nil {
		t.Fatal("nil receiver metrics must be nil")
	}

	// Empty key: falls back to session totals when no buckets and no history.
	ses := &Session{Vendor: "v", Endpoint: "e", TokenUsage: provider.TokenUsage{InputTokens: 7}}
	if u := ses.UsageForEndpoint("", ""); u.InputTokens != 7 {
		t.Fatalf("empty-key fallback to TokenUsage failed: %+v", u)
	}
	// ...but returns empty when metadata exists (history with buckets-like metadata).
	ses2 := &Session{Metrics: []metrics.MetricEvent{{Vendor: "v"}}}
	if m := ses2.MetricsForEndpoint("", ""); m != nil {
		t.Fatal("empty-key with metadata must return nil metrics")
	}
	if m := ses2.MetricsForEndpoint("other", ""); m != nil {
		t.Fatal("unknown endpoint with metadata must return nil")
	}
}

func TestSa104_EndpointStats_BucketsAndRebuild(t *testing.T) {
	ses := &Session{Vendor: "v", Endpoint: "e"}
	ses.AddUsageForEndpoint("v", "e", provider.TokenUsage{InputTokens: 3})
	ses.AddUsageForEndpoint("v", "e", provider.TokenUsage{InputTokens: 4})
	if got := ses.UsageForEndpoint("v", "e"); got.InputTokens != 7 {
		t.Fatalf("accumulation failed: %+v", got)
	}
	ses.AppendMetricForEndpoint("v", "e", metrics.MetricEvent{Type: "a"})
	ses.AppendMetricForEndpoint("v", "e", metrics.MetricEvent{Type: "b"})
	if got := ses.MetricsForEndpoint("v", "e"); len(got) != 2 || got[0].Type != "a" {
		t.Fatalf("metrics append failed: %+v", got)
	}

	// History-only session rebuilds on first query.
	hist := &Session{Vendor: "v", Endpoint: "e", UsageHistory: []UsageEntry{{Vendor: "v", Endpoint: "e", Usage: provider.TokenUsage{OutputTokens: 9}}}, Metrics: []metrics.MetricEvent{{Vendor: "v", Endpoint: "e", Type: "m"}}}
	if u := hist.UsageForEndpoint("v", "e"); u.OutputTokens != 9 {
		t.Fatalf("rebuild from history failed: %+v", u)
	}
	if m := hist.MetricsForEndpoint("v", "e"); len(m) != 1 {
		t.Fatalf("rebuild from metrics failed: %+v", m)
	}
	// Session-total fallback when no history and session key matches.
	legacy := &Session{Vendor: "v", Endpoint: "e", TokenUsage: provider.TokenUsage{OutputTokens: 5}}
	if u := legacy.UsageForEndpoint("v", "e"); u.OutputTokens != 5 {
		t.Fatalf("legacy fallback failed: %+v", u)
	}
}

func TestSa104_EndpointStats_RebuildCapAndEmptyKeys(t *testing.T) {
	ses := &Session{}
	// Over-cap metrics for one endpoint + an empty-key event that must be skipped.
	for i := 0; i < maxEndpointMetricsPerKey+25; i++ {
		ses.Metrics = append(ses.Metrics, metrics.MetricEvent{Vendor: "v", Endpoint: "e", Type: "t"})
	}
	ses.Metrics = append(ses.Metrics, metrics.MetricEvent{})
	ses.RebuildEndpointStats()
	if got := ses.MetricsForEndpoint("v", "e"); len(got) != maxEndpointMetricsPerKey {
		t.Fatalf("rebuild must cap at %d, got %d", maxEndpointMetricsPerKey, len(got))
	}
	if got := ses.MetricsForEndpoint("", ""); got != nil {
		t.Fatal("empty key must return nil after rebuild")
	}
}

// --- AppendUsageEntry / AppendMetric / AppendTunnelEventToDisk ---

func sa104InteractiveSession(t *testing.T, store *JSONLStore, ws string) *Session {
	t.Helper()
	ses := NewSession("zai", "default", "glm-4")
	ses.Workspace = ws
	ses.Messages = []provider.Message{sa104UserMsg("hello")}
	// EnsureMeta first: it creates the file with the meta record (required
	// for repairIndex to rediscover the workspace from disk) and populates
	// the index.
	if err := store.EnsureMeta(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageToDisk(ses, sa104UserMsg("hello")); err != nil {
		t.Fatal(err)
	}
	return ses
}

func TestSa104_AppendRecordVariants(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ses := sa104InteractiveSession(t, store, t.TempDir())

	if err := store.AppendUsageEntry(ses, UsageEntry{Timestamp: time.Now(), TurnIndex: 1, Vendor: "v", Endpoint: "e"}); err != nil {
		t.Fatalf("AppendUsageEntry: %v", err)
	}
	if err := store.AppendMetric(ses, metrics.MetricEvent{Vendor: "v", Type: "ttft"}); err != nil {
		t.Fatalf("AppendMetric: %v", err)
	}
	if err := store.AppendTunnelEventToDisk(ses, TunnelEvent{EventID: "ev1", Type: "open", Data: json.RawMessage(`{"x":1}`)}); err != nil {
		t.Fatalf("AppendTunnelEventToDisk: %v", err)
	}
	// No-interaction sessions are silently skipped.
	empty := NewSession("zai", "default", "m")
	if err := store.AppendUsageEntry(empty, UsageEntry{}); err != nil {
		t.Fatal("no-interaction append must not error")
	}

	raw, err := os.ReadFile(filepath.Join(store.Dir(), ses.ID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, marker := range []string{`"type":"usage"`, `"type":"metric"`, `"tunnel_event"`, "ev1"} {
		if !strings.Contains(text, marker) {
			t.Errorf("session file missing %s", marker)
		}
	}
}

// --- ListForWorkspace / LatestForWorkspace / HasUserInteractionOnDisk ---

func TestSa104_ListForWorkspace(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wsA := t.TempDir()
	wsB := t.TempDir()

	s1 := sa104InteractiveSession(t, store, wsA)
	s2 := sa104InteractiveSession(t, store, wsB)
	time.Sleep(10 * time.Millisecond)
	s3 := sa104InteractiveSession(t, store, wsA)

	got, err := store.ListForWorkspace(wsA)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 sessions in wsA, got %d", len(got))
	}
	if got[0].ID != s3.ID {
		t.Fatalf("list must be sorted newest-first: %s then %s", got[0].ID, got[1].ID)
	}
	latest, err := store.LatestForWorkspace(wsB)
	if err != nil || latest == nil || latest.ID != s2.ID {
		t.Fatalf("LatestForWorkspace failed: %+v err=%v", latest, err)
	}
	if none, err := store.LatestForWorkspace(filepath.Join(t.TempDir(), "nope")); err != nil || none != nil {
		t.Fatalf("unknown workspace must be (nil,nil), got %+v err=%v", none, err)
	}

	// Stale-index repair: dropping the index file must not lose sessions.
	if err := os.Remove(store.indexPath()); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	got, err = store.ListForWorkspace(wsA)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("repair path must rediscover sessions, got %d", len(got))
	}

	ok, err := store.HasUserInteractionOnDisk(s1.ID)
	if err != nil || !ok {
		t.Fatalf("HasUserInteractionOnDisk(s1)=(%t,%v), want (true,nil)", ok, err)
	}
	if ok, _ := store.HasUserInteractionOnDisk("missing-id"); ok {
		t.Fatal("missing session must report no interaction")
	}
}

// --- ExportSessionMarkdownWithDisplay ---

func TestSa104_ExportMarkdownWithDisplay(t *testing.T) {
	now := time.Now()
	ses := &Session{
		ID: "sess-1", Title: "T", CreatedAt: now, UpdatedAt: now,
		Vendor: "zai", Endpoint: "default", Model: "glm-4",
		Messages: []provider.Message{
			sa104UserMsg("question"),
			{Role: "assistant", Content: []provider.ContentBlock{
				{Type: "tool_use", ToolName: "read", Input: json.RawMessage(`{"p":"f.go"}`)},
				{Type: "tool_result", ToolID: "t1", Output: "contents"},
				{Type: "text", Text: "answer"},
			}},
			{Role: "custom-role", Content: []provider.ContentBlock{{Type: "text", Text: "x"}}},
		},
	}
	out := ExportSessionMarkdownWithDisplay(ses, "智谱", "主端点")
	for _, want := range []string{
		"# T", "**Session:** sess-1", "**Vendor:** 智谱 / 主端点 / glm-4",
		"## User", "## Assistant", "## custom-role", "**Tool Call:** `read`", "answer",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
	plain := ExportSessionMarkdownWithDisplay(ses, "", "")
	if !strings.Contains(plain, "**Vendor:** zai / default / glm-4") {
		t.Fatalf("raw names must be used when display names empty: %q", plain)
	}
}

// --- search (search.go) ---

func TestSa104_SearchSessionsAndLine(t *testing.T) {
	dir := t.TempDir()
	path := sa104WriteSessionFile(t, dir, "abc123", []jsonlRecord{
		{Type: "meta"},
		{Type: "usage"},
		{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "find the NEEDLE here"}}}, Timestamp: time.Now()},
	})
	if _, ok := searchJSONLLine(`{bad`, path, "t", "x"); ok {
		t.Error("invalid JSON line must not match")
	}
	if _, ok := searchJSONLLine(`{"type":"meta"}`, path, "t", "meta"); ok {
		t.Error("non-message records must be skipped")
	}
	res, ok := searchJSONLLine(`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"find the NEEDLE here"}]}}`, path, "t", "needle")
	if !ok || res.Role != "user" || !strings.Contains(strings.ToLower(res.Snippet), "needle") {
		t.Fatalf("match failed: %+v ok=%t", res, ok)
	}

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.repairIndex(nil); err != nil {
		t.Fatal(err)
	}
	results, err := store.SearchSessions("needle", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].SessionID != "abc123" {
		t.Fatalf("SearchSessions failed: %+v", results)
	}
	if empty, err := store.SearchSessions("zzz-not-there", 5); err != nil || len(empty) != 0 {
		t.Fatalf("no-match must be empty, got %+v err=%v", empty, err)
	}
}

// --- backfillTimestamps (store.go:3181) ---

func TestSa104_BackfillTimestamps(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Missing file: no panic.
	store.backfillTimestamps("missing-id")

	// Fast path: first message already timestamped → byte-identical file.
	recs := []jsonlRecord{
		{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "a"}}}, Timestamp: time.Now().Add(-time.Hour)},
		{Type: "message", Message: &provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "b"}}}, Timestamp: time.Now()},
	}
	path := sa104WriteSessionFile(t, store.Dir(), "fast", recs)
	before, _ := os.ReadFile(path)
	store.backfillTimestamps("fast")
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("fast path must not rewrite a timestamped session")
	}

	// Slow path: all messages lack timestamps → strictly increasing backfill.
	legacy := sa104WriteSessionFile(t, store.Dir(), "legacy", []jsonlRecord{
		{Type: "meta"},
		{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "u"}}}},
		{Type: "message", Message: &provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a"}}}},
		{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "u2"}}}},
	})
	store.backfillTimestamps("legacy")
	raw, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var last time.Time
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var rec jsonlRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("rewritten line invalid: %v", err)
		}
		if rec.Type != "message" {
			continue
		}
		if rec.Timestamp.IsZero() {
			t.Fatal("every message must get a timestamp")
		}
		if !last.IsZero() && !rec.Timestamp.After(last) {
			t.Fatalf("backfilled timestamps must strictly increase: %v not after %v", rec.Timestamp, last)
		}
		last = rec.Timestamp
		count++
	}
	if count != 3 {
		t.Fatalf("expected 3 backfilled messages, got %d", count)
	}
}
