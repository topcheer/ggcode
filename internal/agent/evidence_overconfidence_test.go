package agent

import (
	"testing"
)

func TestEvidenceOverconfidenceState_Reset(t *testing.T) {
	s := newEvidenceOverconfidenceState()
	s.warnings = 5
	s.evidenceCalls = 10
	s.hasRecentEvid = true
	s.editAfterEvid = true
	s.recentTools = append(s.recentTools, "web_search", "edit_file")

	s.reset()

	if s.warnings != 0 || s.evidenceCalls != 0 || s.hasRecentEvid || s.editAfterEvid {
		t.Fatalf("reset did not clear state: %+v", s)
	}
	if len(s.recentTools) != 0 {
		t.Fatalf("reset did not clear recentTools: %v", s.recentTools)
	}
}

func TestEvidenceOverconfidenceState_RecordEvidenceTool(t *testing.T) {
	s := newEvidenceOverconfidenceState()
	s.recordToolCall("web_search", `{"query":"test"}`)

	if s.evidenceCalls != 1 {
		t.Fatalf("expected evidenceCalls=1, got %d", s.evidenceCalls)
	}
	if !s.hasRecentEvid {
		t.Fatal("expected hasRecentEvid=true after evidence tool")
	}
}

func TestEvidenceOverconfidenceState_RecordVerificationClearsEvidence(t *testing.T) {
	s := newEvidenceOverconfidenceState()
	s.recordToolCall("web_search", `{"query":"test"}`)
	s.recordToolCall("grep", `{"pattern":"foo"}`)

	if s.evidenceCalls != 2 {
		t.Fatalf("expected evidenceCalls=2, got %d", s.evidenceCalls)
	}

	// Verification tool should clear evidence state
	s.recordToolCall("run_command", `{"command":"go test"}`)

	if s.hasRecentEvid {
		t.Fatal("expected hasRecentEvid=false after verification tool")
	}
	if s.editAfterEvid {
		t.Fatal("expected editAfterEvid=false after verification tool")
	}
}

func TestEvidenceOverconfidenceState_EditAfterEvidenceCascade(t *testing.T) {
	s := newEvidenceOverconfidenceState()
	// Evidence → edit without verification = cascade
	s.recordToolCall("web_search", `{"query":"test"}`)
	s.recordToolCall("grep", `{"pattern":"foo"}`)
	s.recordToolCall("edit_file", `{"file_path":"main.go"}`)

	if !s.editAfterEvid {
		t.Fatal("expected editAfterEvid=true after evidence→edit cascade")
	}
	if s.evidenceCalls != 2 {
		t.Fatalf("expected evidenceCalls=2, got %d", s.evidenceCalls)
	}
}

func TestEvidenceOverconfidenceState_EditAfterVerificationNoCascade(t *testing.T) {
	s := newEvidenceOverconfidenceState()
	s.recordToolCall("web_search", `{"query":"test"}`)
	s.recordToolCall("run_command", `{"command":"go build"}`)
	s.recordToolCall("edit_file", `{"file_path":"main.go"}`)

	if s.editAfterEvid {
		t.Fatal("expected editAfterEvid=false: verification cleared evidence state before edit")
	}
}

func TestEvidenceOverconfidenceState_EvidenceToolRe(t *testing.T) {
	tests := []struct {
		tool   string
		expect bool
	}{
		{"web_search", true},
		{"web_fetch", true},
		{"search_files", true},
		{"grep", true},
		{"glob", true},
		{"code_search", true},
		{"lsp_references", true},
		{"lsp_definition", true},
		{"edit_file", false},
		{"run_command", false},
		{"write_file", false},
	}

	for _, tt := range tests {
		got := evidenceToolRe.MatchString(tt.tool)
		if got != tt.expect {
			t.Errorf("evidenceToolRe.MatchString(%q) = %v, want %v", tt.tool, got, tt.expect)
		}
	}
}

func TestFindEvidenceDerivedClaims(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		atLeast int
	}{
		{
			"docs_say",
			"The docs say this is the correct way to configure it.",
			1,
		},
		{
			"based_on_search",
			"Based on my search, the answer is clear. This confirms that the approach works.",
			1,
		},
		{
			"i_found_the_answer",
			"I found the answer. The correct way is to use defer.",
			1,
		},
		{
			"empty_text",
			"",
			0,
		},
		{
			"no_claims",
			"Let me check the file contents first.",
			0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := findEvidenceDerivedClaims(tt.text)
			if len(claims) < tt.atLeast {
				t.Fatalf("expected at least %d claim(s), got %d: %v", tt.atLeast, len(claims), claims)
			}
		})
	}
}

func TestFindEvidenceDerivedClaims_MaxLimit(t *testing.T) {
	text := "The docs say X. The docs say Y. The docs say Z. The docs say W."
	claims := findEvidenceDerivedClaims(text)
	if len(claims) > 3 {
		t.Fatalf("expected at most 3 claims, got %d", len(claims))
	}
}

func TestTruncateEvidenceClaim(t *testing.T) {
	short := "short claim"
	if got := truncateEvidenceClaim(short); got != short {
		t.Fatalf("expected %q, got %q", short, got)
	}

	long := ""
	for i := 0; i < 200; i++ {
		long += "x"
	}
	got := truncateEvidenceClaim(long)
	if len(got) > 80 {
		t.Fatalf("expected len <= 80, got %d", len(got))
	}
	if got[len(got)-3:] != "..." {
		t.Fatalf("expected trailing '...', got %q", got[len(got)-3:])
	}
}

func TestMaybeWarnEvidenceOverconfidence_Pattern1_Cascade(t *testing.T) {
	a := &Agent{evidenceOverconfidence: newEvidenceOverconfidenceState()}
	a.evidenceOverconfidence.recordToolCall("web_search", `{"query":"test"}`)
	a.evidenceOverconfidence.recordToolCall("grep", `{"pattern":"foo"}`)
	a.evidenceOverconfidence.recordToolCall("edit_file", `{"file_path":"main.go"}`)

	hint := a.maybeWarnEvidenceOverconfidence("I made the changes.")
	if hint == "" {
		t.Fatal("expected cascade warning, got empty")
	}
	if a.evidenceOverconfidence.warnings != 1 {
		t.Fatalf("expected warnings=1, got %d", a.evidenceOverconfidence.warnings)
	}
}

func TestMaybeWarnEvidenceOverconfidence_Pattern2_DefinitiveClaims(t *testing.T) {
	a := &Agent{evidenceOverconfidence: newEvidenceOverconfidenceState()}
	a.evidenceOverconfidence.recordToolCall("web_search", `{"query":"how to"}`)
	a.evidenceOverconfidence.recordToolCall("web_fetch", `{"url":"example.com"}`)

	hint := a.maybeWarnEvidenceOverconfidence("Based on my search, this confirms that the approach is correct.")
	if hint == "" {
		t.Fatal("expected definitive claim warning, got empty")
	}
	if a.evidenceOverconfidence.warnings != 1 {
		t.Fatalf("expected warnings=1, got %d", a.evidenceOverconfidence.warnings)
	}
}

func TestMaybeWarnEvidenceOverconfidence_NoEvidenceTools(t *testing.T) {
	a := &Agent{evidenceOverconfidence: newEvidenceOverconfidenceState()}
	// No evidence tools called, just a normal edit
	a.evidenceOverconfidence.recordToolCall("edit_file", `{"file_path":"main.go"}`)

	hint := a.maybeWarnEvidenceOverconfidence("This confirms the fix is correct.")
	if hint != "" {
		t.Fatalf("expected no warning without evidence tools, got: %s", hint)
	}
}

func TestMaybeWarnEvidenceOverconfidence_MaxWarnings(t *testing.T) {
	a := &Agent{evidenceOverconfidence: newEvidenceOverconfidenceState()}
	a.evidenceOverconfidence.warnings = evidenceOverconfMaxWarnings

	hint := a.maybeWarnEvidenceOverconfidence("The docs say it works.")
	if hint != "" {
		t.Fatal("expected no warning after max warnings reached")
	}
}

func TestMaybeWarnEvidenceOverconfidence_VerificationClearsState(t *testing.T) {
	a := &Agent{evidenceOverconfidence: newEvidenceOverconfidenceState()}
	a.evidenceOverconfidence.recordToolCall("web_search", `{"query":"test"}`)
	a.evidenceOverconfidence.recordToolCall("grep", `{"pattern":"foo"}`)
	// Verification should clear evidence state, so no warning even with claim
	a.evidenceOverconfidence.recordToolCall("run_command", `{"command":"go test"}`)

	hint := a.maybeWarnEvidenceOverconfidence("The docs say this is correct.")
	if hint != "" {
		t.Fatalf("expected no warning after verification cleared state, got: %s", hint)
	}
}

func TestMaybeWarnEvidenceOverconfidence_SingleEvidenceNoFire(t *testing.T) {
	a := &Agent{evidenceOverconfidence: newEvidenceOverconfidenceState()}
	// Only 1 evidence call -- below minimum threshold
	a.evidenceOverconfidence.recordToolCall("web_search", `{"query":"test"}`)

	hint := a.maybeWarnEvidenceOverconfidence("The docs say it works.")
	if hint != "" {
		t.Fatalf("expected no warning with only 1 evidence call, got: %s", hint)
	}
}

func TestMaybeWarnEvidenceOverconfidence_NilState(t *testing.T) {
	a := &Agent{evidenceOverconfidence: nil}
	hint := a.maybeWarnEvidenceOverconfidence("The docs say it works.")
	if hint != "" {
		t.Fatal("expected empty hint with nil state")
	}
}

func TestIsVerificationToolCall(t *testing.T) {
	tests := []struct {
		tool  string
		input string
		want  bool
	}{
		{"run_command", `{"command":"go test"}`, true},
		{"run_command", `{"command":"echo hi"}`, true},
		{"start_command", `{"command":"go build"}`, true},
		{"edit_file", `{"file_path":"main.go"}`, false},
		{"web_search", `{"query":"test"}`, false},
	}

	for _, tt := range tests {
		got := isVerificationToolCall(tt.tool, tt.input)
		if got != tt.want {
			t.Errorf("isVerificationToolCall(%q, %q) = %v, want %v", tt.tool, tt.input, got, tt.want)
		}
	}
}
