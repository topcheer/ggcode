package mcp

import "testing"

func TestIssue998AllCapsWriteToolsBlocked(t *testing.T) {
	blocked := []string{
		"DELETE_FILE",
		"DELETE",
		"SET_VALUE",
		"RUN_JOB",
		"PUT_RECORD",
		"EXEC_CMD",
		"POST_TWEET",
		"MOVE_ITEM",
		"EDIT_FILE",
		"EXECUTE_QUERY",
		"EXECUTE_SCRIPT",
		"CREATE_USER",
		"UPDATE_PROFILE",
		"UPLOAD_BLOB",
		"INSTALL_PACKAGE",
		"DEPLOY_APP",
		"WRITE_FILE",
		"RENAME_DIR",
		"UPSERT_RECORD",
	}
	for _, name := range blocked {
		if !isWriteToolName(name) {
			t.Errorf("ALL-CAPS write tool %q NOT blocked (case-only bypass)", name)
		}
	}

	t.Log("note: SETVALUE (no-underscore ALL-CAPS short root) known gap, see comment")
}

func TestIssue998AllCapsReadToolsNotBlocked(t *testing.T) {
	allowed := []string{
		"GET_DATASET",
		"LIST_PRODUCTS",
		"SEARCH_INPUT",
		"FETCH_STATS",
		"QUERY_ROWS",
		"GET_SETTINGS",
		"TRUNCATED_ROWS",
		"ASSET_SEARCH",
		"OUTPUT_STREAM",
	}
	for _, name := range allowed {
		if isWriteToolName(name) {
			t.Errorf("ALL-CAPS read tool %q wrongly blocked (false positive)", name)
		}
	}
}
