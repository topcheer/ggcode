package agent

import (
	"encoding/json"
	"testing"
)

func TestCacheKeyStripsDescriptionForTemplateTools(t *testing.T) {

	if cacheKey("git_status", json.RawMessage(`{"description":"check tree"}`)) !=
		cacheKey("git_status", json.RawMessage(`{}`)) {
		t.Errorf("bare git_status call with description does not key-match the noArg template")
	}
	if cacheKey("git_branch_list", json.RawMessage(`{"description":"list branches"}`)) !=
		cacheKey("git_branch_list", noArgTemplates["git_branch_list"]) {
		t.Errorf("bare git_branch_list call with description does not key-match the template")
	}

	if cacheKey("git_status", json.RawMessage(`{"description":"d","path":"."}`)) ==
		cacheKey("git_status", json.RawMessage(`{}`)) {
		t.Errorf("path-bearing git_status call collides with bare template key")
	}

	if cacheKey("read_file", json.RawMessage(`{"path":"a","description":"d"}`)) ==
		cacheKey("read_file", json.RawMessage(`{"path":"a"}`)) {
		t.Errorf("description stripped for non-template tool read_file")
	}

	_ = cacheKey("git_log", json.RawMessage(`notjson`))
	_ = cacheKey("git_log", json.RawMessage(`null`))
	_ = cacheKey("git_log", json.RawMessage(`[]`))
}
