package mcp

import "testing"

func TestIssue997CamelCaseWriteToolsBlocked(t *testing.T) {
	blocked := []string{
		"setValue",
		"runJob",
		"editFile",
		"moveItem",
		"postTweet",
		"execScript",
		"putRecord",
		"upsertRecord",
		"HTTPExec",
	}
	for _, name := range blocked {
		if !isWriteToolName(name) {
			t.Errorf("camelCase write tool %q NOT blocked (escape regression)", name)
		}
	}
}

func TestIssue997CamelCaseReadToolsNotBlocked(t *testing.T) {
	allowed := []string{
		"getDataset",
		"searchInput",
		"PostgresQuery",
		"outputStream",
		"truncateRows",
		"assetSearch",
		"offsetQuery",
		"listProducts",
		"getSettings",
		"runwayInfo",
	}
	for _, name := range allowed {
		if isWriteToolName(name) {
			t.Errorf("camelCase read tool %q wrongly blocked (false positive regression)", name)
		}
	}
}
