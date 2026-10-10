package mcp

// #3807 companion: the short roots "post"/"run" are write VERBS
// (post_data, run_command) but also ubiquitous read-only NOUNS
// (get_post, get_run_status). Read-only mode must not block the noun
// forms, while every write form stays blocked (fail-closed for
// non-read-verb predecessors like trigger_run).

import "testing"

func TestIssue3807_NounPostRunAllowed(t *testing.T) {
	allowed := []string{
		"get_post",         // WordPress read
		"get_run_status",   // GitHub Actions read
		"list_runs",        // plural twin (was already allowed)
		"fetch_run_logs",   // noun after another read verb
		"query_posts",      // plural
		"getRunStatus",     // camel twin of get_run_status
		"read_run_history", //
		"GET_RUN_STATUS",   // ALL-CAPS twin (plainSegments path)
		"get.run.status",   // dot namespace (plainSegments path)
	}
	for _, name := range allowed {
		if isWriteToolName(name) {
			t.Errorf("read-only noun form %q wrongly blocked", name)
		}
	}
}

func TestIssue3807_WritePostRunStillBlocked(t *testing.T) {
	blocked := []string{
		"post_data",   // verb-initial
		"run_command", // verb-initial
		"create_post", // noun after WRITE verb stays blocked
		"delete_run",  //
		"update_post", //
		"trigger_run", // fail-closed: non-read predecessor
		"start_post",  //
		"PostData",    // camel verb-initial
		"POST_DATA",   // ALL-CAPS
		"post.data",   // dot separator
		"run.query",   // verb-initial with dot
		"execute_run", //
	}
	for _, name := range blocked {
		if !isWriteToolName(name) {
			t.Errorf("write form %q wrongly allowed", name)
		}
	}
}
