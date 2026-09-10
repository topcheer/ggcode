package agent

import "testing"

// #1784 case 1: partial verification partially repays the debt.
func Test1784PartialVerificationRepayment(t *testing.T) {
	v := newVerificationDebtState()
	// Edit two packages (2 debt each via multiple files or count).
	for i := 0; i < 4; i++ {
		v.recordToolCall("edit_file", `{"file_path":"/w/internal/agent/a`+string(rune('0'+i))+`.go","old_text":"a","new_text":"b"}`)
		v.recordToolCall("edit_file", `{"file_path":"/w/internal/config/c`+string(rune('0'+i))+`.go","old_text":"a","new_text":"b"}`)
	}
	if v.debt != 8 {
		t.Fatalf("debt after 8 edits = %d, want 8", v.debt)
	}
	// Verify ONLY internal/config: half the packages covered -> debt halves.
	v.recordToolCall("run_command", `{"command":"# verify config only\ngo test ./internal/config/"}`)
	if v.debt != 4 {
		t.Fatalf("debt after half-scope verification = %d, want 4 (partial repayment)", v.debt)
	}
	// Whole-tree verification clears everything.
	v.recordToolCall("run_command", `{"command":"# verify all\ngo test ./..."}`)
	if v.debt != 0 {
		t.Fatalf("debt after whole-tree verification = %d, want 0", v.debt)
	}
}

// #1784 case 2: lsp_code_actions is grounding (read-only enumeration), not
// verification - it must NOT reset debt.
func Test1784CodeActionsIsGrounding(t *testing.T) {
	v := newVerificationDebtState()
	// Two edits: a full verification reset would give 0; grounding gives 1.
	v.recordToolCall("edit_file", `{"file_path":"/w/x/y.go","old_text":"a","new_text":"b"}`)
	v.recordToolCall("edit_file", `{"file_path":"/w/x/z.go","old_text":"a","new_text":"b"}`)
	v.recordToolCall("lsp_code_actions", `{"path":"/w/x/y.go","start_line":1,"start_character":0,"end_line":1,"end_character":1}`)
	if v.debt != 1 {
		t.Fatalf("lsp_code_actions debt = %d, want 1 (grounding reduces by 1, not reset)", v.debt)
	}
	if v.lastAction != debtGrounding {
		t.Fatalf("lsp_code_actions classified %v, want debtGrounding", v.lastAction)
	}
}

// #1784 case 3: bare relative package paths count as scopes.
func Test1784BarePathScopes(t *testing.T) {
	scopes := parseGoPackageScopes("go test internal/agent/ internal/config/")
	if len(scopes) != 2 || scopes[0] != "internal/agent" || scopes[1] != "internal/config" {
		t.Fatalf("bare relative paths not collected: %v", scopes)
	}
	// ./ forms still work.
	scopes = parseGoPackageScopes("go test ./internal/agent/")
	if len(scopes) != 1 || scopes[0] != "internal/agent" {
		t.Fatalf("./ forms broken: %v", scopes)
	}
	// Flags/urls/whole-tree yield nil or empty (whole-tree).
	if s := parseGoPackageScopes("go build ./..."); s != nil {
		t.Fatalf("./... should collapse elsewhere, got %v", s)
	}
	if s := parseGoPackageScopes("go test -run X https://example.com"); len(s) != 0 {
		t.Fatalf("flags/urls must not be scopes, got %v", s)
	}
}
