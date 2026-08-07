package agent

import "testing"

func TestInputUnderspec_VeryShortWithActionVerb(t *testing.T) {
	a := &Agent{inputUnderspec: newInputUnderspecState()}
	hint := a.maybeWarnInputUnderspec("fix the bug")
	if hint == "" {
		t.Error("expected warning for underspecified 4-word request with action verb")
	}
	if !a.inputUnderspec.warned {
		t.Error("warned flag should be set")
	}
}

func TestInputUnderspec_MediumLengthNoIdentifiers(t *testing.T) {
	a := &Agent{inputUnderspec: newInputUnderspecState()}
	hint := a.maybeWarnInputUnderspec("add tests for the project")
	if hint == "" {
		t.Error("expected warning for medium-length request without identifiers")
	}
}

func TestInputUnderspec_VagueWordsNoIdentifiers(t *testing.T) {
	a := &Agent{inputUnderspec: newInputUnderspecState()}
	hint := a.maybeWarnInputUnderspec("make it faster and better for the user experience here")
	if hint == "" {
		t.Error("expected warning for vague quality words without identifiers")
	}
}

func TestInputUnderspec_WithFilePath_NoWarning(t *testing.T) {
	a := &Agent{inputUnderspec: newInputUnderspecState()}
	hint := a.maybeWarnInputUnderspec("fix the bug")
	_ = hint // first call may warn
	// Second call with a file path should NOT warn
	a2 := &Agent{inputUnderspec: newInputUnderspecState()}
	hint2 := a2.maybeWarnInputUnderspec("fix the bug in main.go")
	if hint2 != "" {
		t.Error("expected no warning when request contains a file path identifier")
	}
}

func TestInputUnderspec_WithQuotedString_NoWarning(t *testing.T) {
	a := &Agent{inputUnderspec: newInputUnderspecState()}
	hint := a.maybeWarnInputUnderspec(`fix the error "undefined variable x"`)
	if hint != "" {
		t.Error("expected no warning when request contains a quoted string identifier")
	}
}

func TestInputUnderspec_WithFuncRef_NoWarning(t *testing.T) {
	a := &Agent{inputUnderspec: newInputUnderspecState()}
	hint := a.maybeWarnInputUnderspec("update the func processRequest to handle nil")
	if hint != "" {
		t.Error("expected no warning when request contains a func declaration reference")
	}
}

func TestInputUnderspec_LongRequestWithDetail_NoWarning(t *testing.T) {
	a := &Agent{inputUnderspec: newInputUnderspecState()}
	hint := a.maybeWarnInputUnderspec("Update the database schema to add a new column called status with type varchar")
	if hint != "" {
		t.Error("expected no warning for long, specific request")
	}
}

func TestInputUnderspec_OnlyFiresOnce(t *testing.T) {
	a := &Agent{inputUnderspec: newInputUnderspecState()}
	_ = a.maybeWarnInputUnderspec("fix the bug")
	hint2 := a.maybeWarnInputUnderspec("fix another bug")
	if hint2 != "" {
		t.Error("expected detector to fire only once per run")
	}
}

func TestInputUnderspec_EmptyInput(t *testing.T) {
	a := &Agent{inputUnderspec: newInputUnderspecState()}
	hint := a.maybeWarnInputUnderspec("")
	if hint != "" {
		t.Error("expected no warning for empty input")
	}
}

func TestInputUnderspec_Reset(t *testing.T) {
	a := &Agent{inputUnderspec: newInputUnderspecState()}
	_ = a.maybeWarnInputUnderspec("fix the bug")
	if !a.inputUnderspec.warned {
		t.Fatal("should have warned")
	}
	a.inputUnderspec.reset()
	if a.inputUnderspec.warned {
		t.Error("reset should clear warned flag")
	}
}

func TestInputUnderspec_NoActionVerb_Short_NoWarning(t *testing.T) {
	a := &Agent{inputUnderspec: newInputUnderspecState()}
	// "hello world" has no action verb, very short
	hint := a.maybeWarnInputUnderspec("hello world greetings")
	if hint != "" {
		t.Error("expected no warning for short request without action verb")
	}
}

func TestInputUnderspec_WithCommitHash_NoWarning(t *testing.T) {
	a := &Agent{inputUnderspec: newInputUnderspecState()}
	hint := a.maybeWarnInputUnderspec("fix the bug in abc1234")
	if hint != "" {
		t.Error("expected no warning when request contains a commit hash")
	}
}
