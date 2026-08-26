package agent

import "testing"

func TestQueryConvergeNoQueries(t *testing.T) {
	q := newQueryConvergeState()
	if msg := q.maybeWarn(1); msg != "" {
		t.Fatalf("expected no warning with no queries, got: %s", msg)
	}
}

func TestQueryConvergeNoWarningAfterAction(t *testing.T) {
	q := newQueryConvergeState()
	q.recordToolCall("grep", `{"pattern":"authentication handler"}`, 1)
	q.recordToolCall("grep", `{"pattern":"auth handler function"}`, 2)
	q.recordToolCall("grep", `{"pattern":"authentication handlers"}`, 3)
	q.recordToolCall("edit_file", `{"file_path":"test.go"}`, 4)
	// Code action resets, should not warn
	if msg := q.maybeWarn(5); msg != "" {
		t.Fatalf("expected no warning after code action, got: %s", msg)
	}
}

func TestQueryConvergeWarningFires(t *testing.T) {
	q := newQueryConvergeState()
	q.recordToolCall("grep", `{"pattern":"authentication handler login"}`, 1)
	q.recordToolCall("grep", `{"pattern":"auth handler login function"}`, 2)
	q.recordToolCall("grep", `{"pattern":"authentication login handler"}`, 3)
	msg := q.maybeWarn(4)
	if msg == "" {
		t.Fatal("expected warning for similar repeated queries")
	}
	if q.warnCount != 1 {
		t.Fatalf("expected warnCount=1, got %d", q.warnCount)
	}
}

func TestQueryConvergeDifferentQueriesNoWarning(t *testing.T) {
	q := newQueryConvergeState()
	q.recordToolCall("grep", `{"pattern":"database connection pool"}`, 1)
	q.recordToolCall("grep", `{"pattern":"grpc server middleware"}`, 2)
	q.recordToolCall("grep", `{"pattern":"react component props"}`, 3)
	if msg := q.maybeWarn(4); msg != "" {
		t.Fatalf("expected no warning for dissimilar queries, got: %s", msg)
	}
}

func TestQueryConvergeSameIterationNoWarning(t *testing.T) {
	q := newQueryConvergeState()
	q.recordToolCall("grep", `{"pattern":"authentication handler"}`, 1)
	q.recordToolCall("grep", `{"pattern":"auth handler"}`, 1)
	q.recordToolCall("grep", `{"pattern":"authentication"}`, 1)
	// All same iteration - not a convergence loop
	if msg := q.maybeWarn(1); msg != "" {
		t.Fatalf("expected no warning for same-iteration queries, got: %s", msg)
	}
}

func TestQueryConvergeWarnCap(t *testing.T) {
	q := newQueryConvergeState()
	q.recordToolCall("grep", `{"pattern":"authentication handler login"}`, 1)
	q.recordToolCall("grep", `{"pattern":"auth handler login function"}`, 2)
	q.recordToolCall("grep", `{"pattern":"authentication login handler"}`, 3)
	if msg1 := q.maybeWarn(4); msg1 == "" {
		t.Fatal("expected first warning")
	}
	// Second warning suppressed (1 per run, batch 2 guidance-noise cleanup)
	q.warned = false
	q.recordToolCall("grep", `{"pattern":"authentication login"}`, 5)
	q.recordToolCall("grep", `{"pattern":"auth login handler"}`, 6)
	if msg2 := q.maybeWarn(7); msg2 != "" {
		t.Fatalf("expected second warning to be suppressed, got: %s", msg2)
	}
	if q.warnCount != 1 {
		t.Fatalf("expected warnCount=1, got %d", q.warnCount)
	}
	// Third should also not fire
	q.warned = false
	if msg3 := q.maybeWarn(8); msg3 != "" {
		t.Fatalf("expected no third warning, got: %s", msg3)
	}
}

func TestQueryConvergeReset(t *testing.T) {
	q := newQueryConvergeState()
	q.recordToolCall("grep", `{"pattern":"authentication handler login"}`, 1)
	q.recordToolCall("grep", `{"pattern":"auth handler login function"}`, 2)
	q.recordToolCall("grep", `{"pattern":"authentication login handler"}`, 3)
	q.maybeWarn(4)
	q.reset()
	if len(q.queries) != 0 {
		t.Fatalf("expected queries cleared after reset, got %d", len(q.queries))
	}
	if q.warnCount != 0 {
		t.Fatalf("expected warnCount=0 after reset, got %d", q.warnCount)
	}
}

func TestQCJaccard(t *testing.T) {
	a := qcTokenize("authentication handler")
	b := qcTokenize("authentication handler")
	if sim := qcJaccard(a, b); sim != 1.0 {
		t.Fatalf("expected 1.0 for identical sets, got %f", sim)
	}
	c := qcTokenize("database connection pool")
	if sim := qcJaccard(a, c); sim != 0.0 {
		t.Fatalf("expected 0.0 for disjoint sets, got %f", sim)
	}
}

func TestQCJaccardPartial(t *testing.T) {
	a := qcTokenize("authentication handler login")
	b := qcTokenize("authentication handler logout")
	// intersection: authentication, handler = 2; union: 4 -> 0.5
	sim := qcJaccard(a, b)
	if sim < 0.4 || sim > 0.6 {
		t.Fatalf("expected ~0.5 similarity, got %f", sim)
	}
}

func TestQCTokenize(t *testing.T) {
	tokens := qcTokenize("The Authentication Handler")
	if !tokens["authentication"] {
		t.Fatal("expected 'authentication' token")
	}
	if !tokens["handler"] {
		t.Fatal("expected 'handler' token")
	}
	if tokens["the"] {
		t.Fatal("did not expect stop word 'the'")
	}
}

func TestQCExtractQuery(t *testing.T) {
	q := qcExtractQuery(`{"pattern":"func.*auth"}`)
	if q != "func.*auth" {
		t.Fatalf("expected 'func.*auth', got '%s'", q)
	}
	q2 := qcExtractQuery(`{"query":"database connection"}`)
	if q2 != "database connection" {
		t.Fatalf("expected 'database connection', got '%s'", q2)
	}
	q3 := qcExtractQuery(`{"path":"/some/path"}`)
	if q3 != "" {
		t.Fatalf("expected empty for non-query field, got '%s'", q3)
	}
}

func TestQCIntToStr(t *testing.T) {
	if s := qcIntToStr(42); s != "42" {
		t.Fatalf("expected '42', got '%s'", s)
	}
	if s := qcIntToStr(0); s != "0" {
		t.Fatalf("expected '0', got '%s'", s)
	}
}

func TestQCFloatToStr(t *testing.T) {
	s := qcFloatToStr(0.50)
	if s != "0.50" {
		t.Fatalf("expected '0.50', got '%s'", s)
	}
}
