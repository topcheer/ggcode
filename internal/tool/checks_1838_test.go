package tool

import (
	"encoding/json"
	"testing"
)

// #1838 case 1 pins: explicit "" is provided for delete-semantics params;
// absence and null are still missing.
func Test1838EmptyStringLegal(t *testing.T) {
	editSchema := json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["file_path","old_text","new_text"]}`)
	// Delete edit: new_text "" present — must pass.
	if msg := ValidateRequiredParams(editSchema, json.RawMessage(`{"file_path":"a.go","old_text":"X","new_text":""}`), "edit_file"); msg != "" {
		t.Fatalf("delete edit must pass, got: %s", msg)
	}
	// new_text ABSENT — must fail.
	if msg := ValidateRequiredParams(editSchema, json.RawMessage(`{"file_path":"a.go","old_text":"X"}`), "edit_file"); msg == "" {
		t.Fatal("absent new_text must still be missing")
	}
	// new_text null — must fail.
	if msg := ValidateRequiredParams(editSchema, json.RawMessage(`{"file_path":"a.go","old_text":"X","new_text":null}`), "edit_file"); msg == "" {
		t.Fatal("null new_text must still be missing")
	}
	// Whitespace content on a whitelisted param IS content (replacing with
	// two spaces is a real edit); #542's whitespace=missing is preserved for
	// NON-whitelisted params (see file_path below).
	if msg := ValidateRequiredParams(editSchema, json.RawMessage(`{"file_path":"a.go","old_text":"X","new_text":"  "}`), "edit_file"); msg != "" {
		t.Fatalf("whitespace replacement content must be provided, got: %s", msg)
	}
	// Empty file creation: write_file content "".
	wfSchema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)
	if msg := ValidateRequiredParams(wfSchema, json.RawMessage(`{"path":"empty.txt","content":""}`), "write_file"); msg != "" {
		t.Fatalf("empty-file creation must pass, got: %s", msg)
	}
	// Delete-by-empty-replacement: batch_replace replacement "".
	brSchema := json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"replacement":{"type":"string"},"files":{"type":"array"}},"required":["pattern","replacement","files"]}`)
	if msg := ValidateRequiredParams(brSchema, json.RawMessage(`{"pattern":"TODO","replacement":"","files":["a.go"]}`), "batch_replace"); msg != "" {
		t.Fatalf("delete-by-empty-replacement must pass, got: %s", msg)
	}
	// Whitelist is tool-scoped: "" for a NON-whitelisted tool still missing.
	if msg := ValidateRequiredParams(editSchema, json.RawMessage(`{"file_path":"","old_text":"X","new_text":"Y"}`), "edit_file"); msg == "" {
		t.Fatal("empty file_path (not whitelisted) must still be missing")
	}
}
