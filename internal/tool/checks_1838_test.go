package tool

import (
	"encoding/json"
	"testing"
)

func Test1838EmptyStringLegal(t *testing.T) {
	editSchema := json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["file_path","old_text","new_text"]}`)

	if msg := ValidateRequiredParams(editSchema, json.RawMessage(`{"file_path":"a.go","old_text":"X","new_text":""}`), "edit_file"); msg != "" {
		t.Fatalf("delete edit must pass, got: %s", msg)
	}

	if msg := ValidateRequiredParams(editSchema, json.RawMessage(`{"file_path":"a.go","old_text":"X"}`), "edit_file"); msg == "" {
		t.Fatal("absent new_text must still be missing")
	}

	if msg := ValidateRequiredParams(editSchema, json.RawMessage(`{"file_path":"a.go","old_text":"X","new_text":null}`), "edit_file"); msg == "" {
		t.Fatal("null new_text must still be missing")
	}

	if msg := ValidateRequiredParams(editSchema, json.RawMessage(`{"file_path":"a.go","old_text":"X","new_text":"  "}`), "edit_file"); msg != "" {
		t.Fatalf("whitespace replacement content must be provided, got: %s", msg)
	}

	wfSchema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)
	if msg := ValidateRequiredParams(wfSchema, json.RawMessage(`{"path":"empty.txt","content":""}`), "write_file"); msg != "" {
		t.Fatalf("empty-file creation must pass, got: %s", msg)
	}

	brSchema := json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"replacement":{"type":"string"},"files":{"type":"array"}},"required":["pattern","replacement","files"]}`)
	if msg := ValidateRequiredParams(brSchema, json.RawMessage(`{"pattern":"TODO","replacement":"","files":["a.go"]}`), "batch_replace"); msg != "" {
		t.Fatalf("delete-by-empty-replacement must pass, got: %s", msg)
	}

	if msg := ValidateRequiredParams(editSchema, json.RawMessage(`{"file_path":"","old_text":"X","new_text":"Y"}`), "edit_file"); msg == "" {
		t.Fatal("empty file_path (not whitelisted) must still be missing")
	}
}
