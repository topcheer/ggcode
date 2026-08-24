package mcp

import "testing"

// TestIssue996ReadOnlyRootCollisionNotBlocked pins the fix: tools whose
// names merely CONTAIN a short denylist root inside an unrelated word
// (dataset, asset, offset, output, truncate...) are read-only queries and
// must NOT be blocked in read_only mode.
func TestIssue996ReadOnlyRootCollisionNotBlocked(t *testing.T) {
	allowed := []string{
		"get_dataset",        // "set" inside "dataset"
		"asset_search",       // "set" inside "asset"
		"offset_query",       // "set" inside "offset"
		"get_settings",       // "set" as prefix of a longer word
		"output_stream_read", // "put" inside "output"
		"search_input",       // "put" inside "input"
		"list_products",      // "put" inside "products"
		"truncate_rows",      // "run" inside "truncate"
		"current_state",      // "run"/"ent" adjacency check
		"runway_info",        // "run" prefix of longer word
		"truncated_stats",    // "run" inside "truncated"
	}
	for _, name := range allowed {
		if isWriteToolName(name) {
			t.Errorf("read-only tool %q wrongly blocked (root collision)", name)
		}
	}
}

// TestIssue996RealWriteToolsStillBlocked guards the fail-closed direction:
// genuine write operations - including short-root ones as whole segments -
// remain blocked.
func TestIssue996RealWriteToolsStillBlocked(t *testing.T) {
	blocked := []string{
		"set_value",   // "set" as complete segment
		"put_record",  // "put" segment
		"run_command", // "run" segment
		"exec_query",  // "exec" segment
		"post_data",   // "post" segment
		"delete_row",
		"create_user",
		"update_settings", // "update" long keyword, substring match
		"upsert_record",   // multi-word root inside a segment
		"execute_script",
		"deploy_service",
		"install_package",
		"write_file",
	}
	for _, name := range blocked {
		if !isWriteToolName(name) {
			t.Errorf("write tool %q NOT blocked in read-only mode (fail-open regression)", name)
		}
	}
}
