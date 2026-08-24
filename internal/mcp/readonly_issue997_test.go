package mcp

import "testing"

// TestIssue997CamelCaseWriteToolsBlocked pins the #997 fix: camelCase
// write tools whose short root sits at a word boundary (setValue, runJob,
// ...) must be blocked exactly like their snake_case twins. Before the
// camel normalization they lowercased into a single segment ("setvalue")
// and escaped the read_only gate.
func TestIssue997CamelCaseWriteToolsBlocked(t *testing.T) {
	blocked := []string{
		"setValue",     // set + Value
		"runJob",       // run + Job
		"editFile",     // edit (4-char root) + File
		"moveItem",     // move + Item
		"postTweet",    // post + Tweet
		"execScript",   // exec + Script
		"putRecord",    // put + Record
		"upsertRecord", // long root, was already caught
		"HTTPExec",     // run of capitals + long root substring
	}
	for _, name := range blocked {
		if !isWriteToolName(name) {
			t.Errorf("camelCase write tool %q NOT blocked (escape regression)", name)
		}
	}
}

// TestIssue997CamelCaseReadToolsNotBlocked pins the other direction:
// camel-normalizing must not reintroduce the #996 false-positive class -
// camelCase read names split into harmless segments.
func TestIssue997CamelCaseReadToolsNotBlocked(t *testing.T) {
	allowed := []string{
		"getDataset",    // -> get_dataset
		"searchInput",   // -> search_input
		"PostgresQuery", // -> postgres_query
		"outputStream",  // -> output_stream
		"truncateRows",  // -> truncate_rows
		"assetSearch",   // -> asset_search
		"offsetQuery",   // -> offset_query
		"listProducts",  // -> list_products
		"getSettings",   // -> get_settings
		"runwayInfo",    // -> runway_info
	}
	for _, name := range allowed {
		if isWriteToolName(name) {
			t.Errorf("camelCase read tool %q wrongly blocked (false positive regression)", name)
		}
	}
}
