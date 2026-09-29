package toollabel

import "testing"

// TestActivityParityWithLegacyIM pins the shared tables against the legacy
// internal/im localizedToolActivity behavior (daemon_bridge_pure_test.go's
// expectations) for the action set the IM frontend actually requests.
// The IM switch was a strict subset of the TUI switch; these assertions
// prove the extraction preserved its outputs byte-for-byte.
func TestActivityParityWithLegacyIM(t *testing.T) {
	cases := []struct {
		lang   Lang
		action string
		target string
		want   string
	}{
		{En, "read", "", "Reading file"},
		{ZhCN, "read", "", "读取文件"},
		{En, "read", "main.go", "Reading main.go"},
		{En, "edit", "", "Editing file"},
		{En, "write", "", "Writing file"},
		{En, "search", "", "Searching..."},
		{En, "find", "", "Finding files"},
		{En, "list", "", "Listing directory"},
		{En, "run", "", "Running command"},
		{En, "fetch", "", "Fetching page"},
		{En, "todo", "", "Updating todos"},
		{En, "task", "", "Running task"},
		{En, "skill", "", "Loading skill"},
		{En, "ask", "", "Waiting for user input"},
		{En, "inspect", "", "Inspecting..."},
		{En, "unknown", "", "Running "},
		{ZhCN, "search", "pattern", "搜索 pattern"},
		{ZhCN, "run", "npm test", "执行 npm test"},
		{En, "task", "review changes", "Running task review changes"},
	}
	for _, tc := range cases {
		if got := Activity(tc.lang, tc.action, tc.target); got != tc.want {
			t.Errorf("Activity(%d, %q, %q) = %q, want %q", tc.lang, tc.action, tc.target, got, tc.want)
		}
	}
}

// TestLabelParityWithLegacyIM pins the label tier against the legacy im
// localizedToolLabel outputs (tool_status_test.go: generic fallback "Read
// File" style plus exact short labels).
func TestLabelParityWithLegacyIM(t *testing.T) {
	cases := []struct {
		lang   Lang
		action string
		want   string
	}{
		{En, "read", "Read"},
		{En, "edit", "Edit"},
		{En, "write", "Write"},
		{En, "search", "Search"},
		{En, "run", "Run"},
		{En, "fetch", "Fetch"},
		{En, "todo", "Update todos"},
		{En, "task", "Run task"},
		{En, "ask", "Ask"},
		{En, "inspect", "Inspect"},
		{En, "unknown_action", "Unknown Action"},
		{En, "read_file", "Read File"},
		{ZhCN, "read", "读"},
		{ZhCN, "read_file", "read file"},
		{ZhCN, "unknown_action", "unknown action"},
	}
	for _, tc := range cases {
		if got := Label(tc.lang, tc.action); got != tc.want {
			t.Errorf("Label(%d, %q) = %q, want %q", tc.lang, tc.action, got, tc.want)
		}
	}
}

// TestTableCounts reconciles table entry counts against the transcribed
// legacy switch case counts (r250 lesson: count equality proves no lost
// or duplicated keys during transcription).
func TestTableCounts(t *testing.T) {
	if got := len(labels[ZhCN]); got != 70 {
		t.Errorf("labels[ZhCN] has %d entries, want 70", got)
	}
	if got := len(labels[En]); got != 70 {
		t.Errorf("labels[En] has %d entries, want 70", got)
	}
	if got := len(activityNoTarget[ZhCN]); got != 67 {
		t.Errorf("activityNoTarget[ZhCN] has %d entries, want 67", got)
	}
	if got := len(activityNoTarget[En]); got != 67 {
		t.Errorf("activityNoTarget[En] has %d entries, want 67", got)
	}
	if got := len(activityWithTarget[ZhCN]); got != 14 {
		t.Errorf("activityWithTarget[ZhCN] has %d entries, want 14", got)
	}
	if got := len(activityWithTarget[En]); got != 14 {
		t.Errorf("activityWithTarget[En] has %d entries, want 14", got)
	}
	// Key-set symmetry across languages: a key present in one language's
	// tier must exist in the other.
	for name, tiers := range map[string][2]map[string]string{
		"labels":             {labels[ZhCN], labels[En]},
		"activityNoTarget":   {activityNoTarget[ZhCN], activityNoTarget[En]},
		"activityWithTarget": {activityWithTarget[ZhCN], activityWithTarget[En]},
	} {
		for k := range tiers[0] {
			if _, ok := tiers[1][k]; !ok {
				t.Errorf("%s: key %q missing from En table", name, k)
			}
		}
		for k := range tiers[1] {
			if _, ok := tiers[0][k]; !ok {
				t.Errorf("%s: key %q missing from ZhCN table", name, k)
			}
		}
	}
}

// TestTUISupersetKeys samples TUI-only keys that the legacy IM switch did
// not carry, ensuring the shared tables kept the superset intact.
func TestTUISupersetKeys(t *testing.T) {
	cases := []struct {
		lang   Lang
		action string
		want   string
	}{
		{ZhCN, "spawn_agent", "启动子代理"},
		{En, "spawn_agent", "Starting subagent"},
		{ZhCN, "a2a_cancel_task", "取消任务"},
		{En, "a2a_cancel_task", "Cancel Task"},
		{En, "run_in_background", "Run in background"},
		{En, "list_mcp_capabilities", "MCP Servers"},
		{ZhCN, "list_mcp_capabilities", "MCP 服务器"},
	}
	for _, tc := range cases {
		if got := Label(tc.lang, tc.action); got != tc.want {
			t.Errorf("Label(%d, %q) = %q, want %q", tc.lang, tc.action, got, tc.want)
		}
	}
	if got := Activity(En, "a2a_send_task", ""); got != "Sending task..." {
		t.Errorf("Activity no-target a2a_send_task = %q", got)
	}
	if got := Activity(En, "wait_agent", "agent-1"); got != "Running agent-1" {
		t.Errorf("Activity with-target fallback for wait_agent = %q, want generic Running agent-1", got)
	}
}
