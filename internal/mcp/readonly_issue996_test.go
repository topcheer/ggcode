package mcp

import "testing"

func TestIssue996ReadOnlyRootCollisionNotBlocked(t *testing.T) {
	allowed := []string{
		"get_dataset",
		"asset_search",
		"offset_query",
		"get_settings",
		"output_stream_read",
		"search_input",
		"list_products",
		"truncate_rows",
		"current_state",
		"runway_info",
		"truncated_stats",
	}
	for _, name := range allowed {
		if isWriteToolName(name) {
			t.Errorf("read-only tool %q wrongly blocked (root collision)", name)
		}
	}
}

func TestIssue996RealWriteToolsStillBlocked(t *testing.T) {
	blocked := []string{
		"set_value",
		"put_record",
		"run_command",
		"exec_query",
		"post_data",
		"delete_row",
		"create_user",
		"update_settings",
		"upsert_record",
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
