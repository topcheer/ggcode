package agent

import (
	"encoding/json"
	"testing"
)

// #4967: measureEditSize covered only 5 of the 9 productiveEditTools
// (sourceMutatingTools) — batch_replace, lsp_rename, file_ops and
// notebook_edit fell through to default 0, so high-impact refactors
// (multi-file codemods, symbol renames, file moves, notebook rewrites)
// were recorded as zero-byte edits and could trip the diminishing-edit
// advisory as a false "polish spiral". Each previously-unmeasured tool
// must now report a nonzero size for a real edit payload, and an
// unparseable payload still measures 0.
func TestIssue4967MeasureEditSizeCoversAllProductiveTools(t *testing.T) {
	cases := []struct {
		tool string
		args string
	}{
		{"batch_replace", `{"pattern":"fmt.Errorf","replacement":"errors.New","files":["a.go","b.go","c.go"]}`},
		{"batch_replace", `{"pattern":"OldLongSymbolName","replacement":"NewName","files":["only.go"]}`},
		{"lsp_rename", `{"path":"x.go","line":10,"character":3,"new_name":"renamedSymbol"}`},
		{"file_ops", `{"operations":[{"action":"move","source":"/tmp/a.go","destination":"/tmp/b.go"},{"action":"delete","source":"/tmp/c.go"}]}`},
		{"notebook_edit", `{"notebook_path":"n.ipynb","operation":"replace","cell_number":2,"source":"print('hello, world')\nprint('second line')"}`},
	}
	for _, c := range cases {
		if got := measureEditSize(c.tool, json.RawMessage(c.args)); got <= 0 {
			t.Errorf("#4967: %s measured %d bytes for a real edit payload, want > 0", c.tool, got)
		}
	}
	if got := measureEditSize("lsp_rename", json.RawMessage(`{"new_name":"x"}`)); got != 1 {
		t.Errorf("#4967: lsp_rename 1-char rename measured %d, want 1", got)
	}
}

func TestIssue4967MeasureEditSizeZeroOnBadPayload(t *testing.T) {
	for _, tool := range []string{"batch_replace", "lsp_rename", "file_ops", "notebook_edit"} {
		if got := measureEditSize(tool, json.RawMessage(`{not json`)); got != 0 {
			t.Errorf("#4967: %s unparseable payload measured %d, want 0", tool, got)
		}
	}
}
