package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvDrift_NoExampleFile(t *testing.T) {
	dir := t.TempDir()
	s := newEnvDriftState()
	if msg := s.check(dir); msg != "" {
		t.Errorf("expected empty message when no .env.example, got: %s", msg)
	}
}

func TestEnvDrift_AllVarsSet(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.example", "DATABASE_URL=postgresql://localhost\nAPI_KEY=secret\n")
	writeEnvFile(t, dir, ".env", "DATABASE_URL=postgresql://localhost\nAPI_KEY=mykey\n")

	s := newEnvDriftState()
	if msg := s.check(dir); msg != "" {
		t.Errorf("expected empty message when all vars set, got: %s", msg)
	}
}

func TestEnvDrift_MissingVars(t *testing.T) {
	dir := t.TempDir()
	// API_KEY/REDIS_URL use empty template values (no default) so they count
	// as required; DATABASE_URL carries a default and must be skipped (#1034).
	writeEnvFile(t, dir, ".env.example", "DATABASE_URL=postgresql://localhost\nAPI_KEY=\nREDIS_URL=\n")
	writeEnvFile(t, dir, ".env", "DATABASE_URL=postgresql://localhost\n")

	s := newEnvDriftState()
	msg := s.check(dir)
	if msg == "" {
		t.Fatal("expected non-empty drift message")
	}
	if !strings.Contains(msg, "API_KEY") {
		t.Errorf("expected API_KEY in message, got: %s", msg)
	}
	if !strings.Contains(msg, "REDIS_URL") {
		t.Errorf("expected REDIS_URL in message, got: %s", msg)
	}
	if strings.Contains(msg, "DATABASE_URL") {
		t.Errorf("DATABASE_URL should not be in missing list, got: %s", msg)
	}
}

func TestEnvDrift_FiresOncePerRun(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.example", "MISSING_VAR=\n")

	s := newEnvDriftState()
	msg1 := s.check(dir)
	if msg1 == "" {
		t.Fatal("expected non-empty message on first call")
	}
	msg2 := s.check(dir)
	if msg2 != msg1 {
		t.Errorf("expected cached message on second call, got: %q vs %q", msg2, msg1)
	}
}

func TestEnvDrift_ResetClearsFired(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.example", "MISSING_VAR=\n")

	s := newEnvDriftState()
	_ = s.check(dir)
	if !s.fired {
		t.Fatal("expected fired=true after check")
	}
	s.reset()
	if s.fired {
		t.Fatal("expected fired=false after reset")
	}
}

func TestEnvDrift_VarsInShellEnvCount(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.example", "MY_TEST_VAR_12345=value\n")

	t.Setenv("MY_TEST_VAR_12345", "fromshell")

	s := newEnvDriftState()
	if msg := s.check(dir); msg != "" {
		t.Errorf("expected empty message when var is in shell env, got: %s", msg)
	}
}

func TestEnvDrift_CommentedVarsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.example", "# COMMENTED_VAR=value\nREAL_MISSING=\n")

	s := newEnvDriftState()
	msg := s.check(dir)
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
	if strings.Contains(msg, "COMMENTED_VAR") {
		t.Errorf("COMMENTED_VAR should be skipped, got: %s", msg)
	}
	if !strings.Contains(msg, "REAL_MISSING") {
		t.Errorf("REAL_MISSING should be in message, got: %s", msg)
	}
}

func TestEnvDrift_ExportSyntax(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.example", "export FOO=bar\nexport BAZ=\n")
	writeEnvFile(t, dir, ".env", "export FOO=mybar\n")

	s := newEnvDriftState()
	msg := s.check(dir)
	if msg == "" {
		t.Fatal("expected non-empty message for missing BAZ")
	}
	if strings.Contains(msg, "FOO") {
		t.Errorf("FOO should not be in missing list, got: %s", msg)
	}
	if !strings.Contains(msg, "BAZ") {
		t.Errorf("BAZ should be in message, got: %s", msg)
	}
}

func TestEnvDrift_DefaultedVarsSkipped(t *testing.T) {
	dir := t.TempDir()
	// #1034: vars with non-empty defaults are not required from the user;
	// only truly unset vars (empty / "" / '') are reported.
	writeEnvFile(t, dir, ".env.example", "LOG_LEVEL=info\nDB_URL=postgres://localhost\nAPI_KEY=\n")

	s := newEnvDriftState()
	msg := s.check(dir)
	if msg == "" {
		t.Fatal("expected non-empty message for API_KEY")
	}
	if !strings.Contains(msg, "API_KEY") {
		t.Errorf("API_KEY should be in message, got: %s", msg)
	}
	for _, skipped := range []string{"LOG_LEVEL", "DB_URL"} {
		if strings.Contains(msg, skipped) {
			t.Errorf("%s has a default and should be skipped, got: %s", skipped, msg)
		}
	}
}

func TestEnvDrift_LargeNumberOfVars(t *testing.T) {
	dir := t.TempDir()
	content := ""
	for i := 0; i < 15; i++ {
		content += "VAR_" + string(rune('A'+i)) + "_TEST=\n"
	}
	writeEnvFile(t, dir, ".env.example", content)

	s := newEnvDriftState()
	msg := s.check(dir)
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(msg, "total") {
		t.Logf("message: %s", msg)
	}
}

func TestEnvDrift_EmptyWorkingDir(t *testing.T) {
	s := newEnvDriftState()
	if msg := s.check(""); msg != "" {
		t.Errorf("expected empty message for empty working dir, got: %s", msg)
	}
}

func TestParseEnvContent(t *testing.T) {
	content := `# Comment line
FOO=bar
BAZ=qux
EMPTY=
#ANOTHER=comment
export EXPORTED=yes
INVALID==double
`
	result := parseEnvContent(content)
	// FOO, BAZ, EXPORTED, INVALID(="=double") are non-empty; EMPTY is empty
	if len(result) != 4 {
		t.Errorf("expected 4 set vars, got %d: %v", len(result), result)
	}
	if !result["FOO"] {
		t.Error("FOO should be set")
	}
	if !result["BAZ"] {
		t.Error("BAZ should be set")
	}
	if !result["EXPORTED"] {
		t.Error("EXPORTED should be set")
	}
	if result["EMPTY"] {
		t.Error("EMPTY should not be counted as set")
	}
}

func TestIsValidEnvName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"VALID_VAR", true},
		{"API_KEY", true},
		{"var_lower", true},
		{"VAR123", true},
		{"", false},
		{"INVALID-VAR", false},
		{"INVALID.VAR", false},
		{"INVALID VAR", false},
	}
	for _, tt := range tests {
		if got := isValidEnvName(tt.name); got != tt.want {
			t.Errorf("isValidEnvName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestEnvDrift_TemplateFilePriority(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.example", "FROM_EXAMPLE=\n")
	writeEnvFile(t, dir, ".env.template", "FROM_TEMPLATE=\n")

	s := newEnvDriftState()
	msg := s.check(dir)
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(msg, "FROM_EXAMPLE") {
		t.Errorf("FROM_EXAMPLE should be in message, got: %s", msg)
	}
	if strings.Contains(msg, "FROM_TEMPLATE") {
		t.Errorf("FROM_TEMPLATE should NOT be in message (.env.example takes priority), got: %s", msg)
	}
}

func writeEnvFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
