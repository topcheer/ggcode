package agent

import (
	"testing"
)

func TestGoalDriftCtx_InitFromUserMessage(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("Fix the authentication bug in internal/auth/handler.go where login fails")

	if !s.initialized {
		t.Fatal("expected initialized=true")
	}
	if len(s.originKeywords) < 3 {
		t.Fatalf("expected at least 3 keywords, got %d: %v", len(s.originKeywords), s.originKeywords)
	}
	for _, expected := range []string{"authentication", "handler", "login", "fails"} {
		if !s.originKeywords[expected] {
			t.Errorf("expected keyword %q in origin keywords", expected)
		}
	}
	for _, stop := range []string{"the", "where", "in"} {
		if s.originKeywords[stop] {
			t.Errorf("stop word %q should not be in origin keywords", stop)
		}
	}
}

func TestGoalDriftCtx_InitOnlyOnce(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("fix handler.go authentication")
	firstCount := len(s.originKeywords)
	s.initFromUserMessage("completely different message about database")
	if len(s.originKeywords) != firstCount {
		t.Fatalf("keywords changed on second init: %d -> %d", firstCount, len(s.originKeywords))
	}
}

func TestGoalDriftCtx_InitEmpty(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("")
	if s.initialized {
		t.Fatal("empty message should not initialize")
	}
}

func TestGoalDriftCtx_RecordToolCall(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("Fix authentication handler in login module")
	s.recordToolCall("edit_file", `{"path": "/src/auth/handler.go"}`)
	s.recordToolCall("read_file", `{"path": "/src/auth/handler.go"}`)
	if len(s.recentTargets) != 2 {
		t.Fatalf("expected 2 recent targets, got %d", len(s.recentTargets))
	}
}

func TestGoalDriftCtx_ShortTargetIgnored(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("Fix authentication handler")
	s.recordToolCall("run_command", `{"command": "ls"}`)
	if len(s.recentTargets) != 0 {
		t.Fatalf("short target should be ignored, got %d targets", len(s.recentTargets))
	}
}

func TestGoalDriftCtx_NoDriftBeforeMinIter(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("Fix authentication handler login module")
	for i := 0; i < 8; i++ {
		s.recordToolCall("read_file", `{"path": "/unrelated/random/file.go"}`)
	}
	hint := s.checkDrift(8)
	if hint != "" {
		t.Fatalf("should not trigger before min iterations, got: %s", hint)
	}
}

func TestGoalDriftCtx_DriftDetected(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("Fix authentication handler in the login module")
	for i := 0; i < goalDriftWindowSize; i++ {
		s.recordToolCall("read_file", `{"path": "/unrelated/random/file.go"}`)
	}
	hint := s.checkDrift(goalDriftMinIter)
	if hint == "" {
		t.Fatal("expected drift detection at min iterations")
	}
	if !s.warned {
		t.Fatal("expected warned=true after drift detection")
	}
}

func TestGoalDriftCtx_NoDriftWhenOnTarget(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("Fix authentication handler in the login module")
	for i := 0; i < goalDriftWindowSize; i++ {
		s.recordToolCall("read_file", `{"path": "/src/auth/handler.go"}`)
	}
	hint := s.checkDrift(goalDriftMinIter)
	if hint != "" {
		t.Fatalf("should not detect drift when targets match, got: %s", hint)
	}
}

func TestGoalDriftCtx_WarnOncePerRun(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("Fix authentication handler in the login module")
	for i := 0; i < goalDriftWindowSize; i++ {
		s.recordToolCall("read_file", `{"path": "/unrelated/random/file.go"}`)
	}
	first := s.checkDrift(goalDriftMinIter)
	if first == "" {
		t.Fatal("expected first drift detection")
	}
	second := s.checkDrift(goalDriftMinIter + goalDriftCheckInterval + 1)
	if second != "" {
		t.Fatal("should not warn twice per run")
	}
}

func TestGoalDriftCtx_NotEnoughKeywords(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("fix bug")
	for i := 0; i < goalDriftWindowSize; i++ {
		s.recordToolCall("read_file", `{"path": "/unrelated/random.go"}`)
	}
	hint := s.checkDrift(goalDriftMinIter)
	if hint != "" {
		t.Fatal("should not trigger with too few keywords")
	}
}

func TestGoalDriftCtx_NotEnoughTargets(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("Fix authentication handler login module")
	s.recordToolCall("read_file", `{"path": "/unrelated/random.go"}`)
	hint := s.checkDrift(goalDriftMinIter)
	if hint != "" {
		t.Fatal("should not trigger with too few targets")
	}
}

func TestGoalDriftCtx_PartialDriftNotTriggered(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("Fix authentication handler login module")
	for i := 0; i < goalDriftWindowSize/2; i++ {
		s.recordToolCall("read_file", `{"path": "/src/auth/handler.go"}`)
	}
	for i := 0; i < goalDriftWindowSize/2; i++ {
		s.recordToolCall("read_file", `{"path": "/unrelated/random.go"}`)
	}
	hint := s.checkDrift(goalDriftMinIter)
	if hint != "" {
		t.Fatalf("should not trigger with half targets matching, got: %s", hint)
	}
}

func TestGoalDriftCtx_ExtractTargetTools(t *testing.T) {
	tests := []struct {
		toolName string
		rawArgs  string
		want     string
	}{
		{"read_file", `{"path": "/src/auth/handler.go"}`, "/src/auth/handler.go"},
		{"edit_file", `{"file_path": "/src/main.go"}`, "/src/main.go"},
		{"grep", `{"pattern": "TODO"}`, "TODO"},
		{"search_files", `{"query": "auth"}`, "auth"},
		{"glob", `{"pattern": "*.go"}`, "*.go"},
		{"run_command", `{"command": "go test"}`, "go test"},
		{"code_search", `{"query": "login"}`, "login"},
	}
	for _, tt := range tests {
		got := extractTarget(tt.toolName, []byte(tt.rawArgs))
		if got != tt.want {
			t.Errorf("extractTarget(%q, %q) = %q, want %q", tt.toolName, tt.rawArgs, got, tt.want)
		}
	}
}

func TestGoalDriftCtx_CleanToken(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"hello(", "hello"},
		{")world", "world"},
		{"test_123", "test_123"},
		{"auth/handler.go", "auth/handler.go"},
	}
	for _, tt := range tests {
		got := cleanToken(tt.input)
		if got != tt.want {
			t.Errorf("cleanToken(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGoalDriftCtx_GoalDriftIsStopWord(t *testing.T) {
	if !goalDriftIsStopWord("the") {
		t.Error("expected 'the' to be stop word")
	}
	if !goalDriftIsStopWord("THE") {
		t.Error("expected case-insensitive stop word match")
	}
	if goalDriftIsStopWord("authentication") {
		t.Error("expected 'authentication' to NOT be stop word")
	}
}

func TestGoalDriftCtx_PathKeywordExtraction(t *testing.T) {
	s := newGoalDriftCtxState()
	s.initFromUserMessage("Fix the bug in auth/handler.go")
	if !s.originKeywords["auth"] {
		t.Error("expected 'auth' extracted from path")
	}
	if !s.originKeywords["handler"] {
		t.Error("expected 'handler' extracted from path")
	}
}
