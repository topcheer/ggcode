package knight

import "testing"

// Regression for #1644-1: pure-CJK corrections were silently dropped -
// sanitizeName emptied every word, the join made "--" (<8) or fell to
// correction-N, and isValidCandidateName rejected both. The deterministic
// cjk-correction-<hash> name must survive.
func TestPureCJKCorrectionNameSurvives(t *testing.T) {
	name := buildCorrectionSkillName("构建失败 请重试一次 然后清理")
	if !isValidCandidateName(name) {
		t.Fatalf("pure-CJK correction name %q must pass isValidCandidateName", name)
	}
	if name != buildCorrectionSkillName("构建失败 请重试一次 然后清理") {
		t.Fatal("name must be deterministic for identical text")
	}
	if len(name) < 8 || name[:4] != "cjk-" {
		t.Fatalf("expected the cjk- fallback form, got %q", name)
	}
}
