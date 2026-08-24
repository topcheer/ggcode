package mcp

import "testing"

// TestIssue998AllCapsWriteToolsBlocked pins the #998 fix: ALL-CAPS REST-style
// write tools fragment into single-letter segments under camelToSnake, so
// long keywords are additionally substring-checked against the
// plain-lowercased name. Same name differing only in case must not bypass
// the gate (DELETE_FILE vs delete_file).
//
// Known limitation, accepted per #998 scope: ALL-CAPS SHORT-root names
// WITHOUT underscores (SETVALUE, EXEC, PUT) stay uncatchable - plain
// substring checks on short roots would re-flag get_dataset/asset_search
// (#996 regression). Underscored ALL-CAPS twins (SET_VALUE, RUN_JOB) ARE
// caught since the short-root segment match also runs on the
// plain-lowercased segments. readOnlyHint annotations remain the
// follow-up for full coverage.
func TestIssue998AllCapsWriteToolsBlocked(t *testing.T) {
	blocked := []string{
		"DELETE_FILE", // long root "delete" via plain substring
		"DELETE",      // long root on plain lowercase
		"SET_VALUE",   // short root as whole plain segment (final-review variant)
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
	// No-underscore ALL-CAPS short-root names remain the documented gap.
	t.Log("note: SETVALUE (no-underscore ALL-CAPS short root) known gap, see comment")
}

// TestIssue998AllCapsReadToolsNotBlocked pins the other direction: the plain
// substring check on long keywords must not re-flag ALL-CAPS read names.
func TestIssue998AllCapsReadToolsNotBlocked(t *testing.T) {
	allowed := []string{
		"GET_DATASET", // plain "get_dataset": no keyword substring
		"LIST_PRODUCTS",
		"SEARCH_INPUT",
		"FETCH_STATS",
		"QUERY_ROWS",
		"GET_SETTINGS",   // "set" only inside "settings", not a whole segment
		"TRUNCATED_ROWS", // "run" only inside "truncated", not a whole segment
		"ASSET_SEARCH",   // "set" inside "asset"
		"OUTPUT_STREAM",  // "put" inside "output"
	}
	for _, name := range allowed {
		if isWriteToolName(name) {
			t.Errorf("ALL-CAPS read tool %q wrongly blocked (false positive)", name)
		}
	}
}
