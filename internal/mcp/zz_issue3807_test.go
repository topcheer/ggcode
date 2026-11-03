package mcp

import "testing"

func TestIssue3807_NounPostRunAllowed(t *testing.T) {
	allowed := []string{
		"get_post",
		"get_run_status",
		"list_runs",
		"fetch_run_logs",
		"query_posts",
		"getRunStatus",
		"read_run_history",
		"GET_RUN_STATUS",
		"get.run.status",
	}
	for _, name := range allowed {
		if isWriteToolName(name) {
			t.Errorf("read-only noun form %q wrongly blocked", name)
		}
	}
}

func TestIssue3807_WritePostRunStillBlocked(t *testing.T) {
	blocked := []string{
		"post_data",
		"run_command",
		"create_post",
		"delete_run",
		"update_post",
		"trigger_run",
		"start_post",
		"PostData",
		"POST_DATA",
		"post.data",
		"run.query",
		"execute_run",
	}
	for _, name := range blocked {
		if !isWriteToolName(name) {
			t.Errorf("write form %q wrongly allowed", name)
		}
	}
}
