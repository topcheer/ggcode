package agent

import "testing"

func TestIssue4003ResearchModeUsesCurrentRunText(t *testing.T) {
	fix := &Agent{overseer: newOverseerState()}
	fix.resetOverseer("fix the login handler bug")
	if fix.overseer.researchMode {
		t.Fatal("plain fix task must not enable research mode")
	}

	fix.overseer = newOverseerState()
	fix.resetOverseer("research competitor architectures and produce a comparative analysis")
	if !fix.overseer.researchMode {
		t.Fatal("current-run research task must enable research mode (session-first-message fallback bug #4003)")
	}

	fix.overseer = newOverseerState()
	fix.resetOverseer("refactor the config loader")
	if fix.overseer.researchMode {
		t.Fatal("current-run plain task must disable research mode (stale run-1 verdict #4003)")
	}
}

func TestIssue4004ANotAQuestionsAreNotRejection(t *testing.T) {
	for _, q := range []string{
		"这个函数名改成 foo 对不对？",
		"这样改到底行不行？",
		"你是不是这样理解的？",
		"我这样做是不是不对？",
	} {
		if cat := detectNegativeFeedback(q); cat == negCatRejection {
			t.Fatalf("A-not-A confirmation question misclassified as rejection: %q -> %q", q, cat)
		}
	}
}

func TestIssue4004GenuineCJKRejectionStillDetected(t *testing.T) {
	for _, neg := range []string{
		"不对，重新做",
		"还是不行",
		"这个方案错了",
	} {
		if cat := detectNegativeFeedback(neg); cat != negCatRejection {
			t.Fatalf("genuine CJK rejection missed: %q -> %q", neg, cat)
		}
	}
}

func TestIssue4004ANotADetector(t *testing.T) {
	for _, hit := range []string{"对不对", "行不行", "是不是", "好不好", "能不能", "要不要"} {
		if !containsCJKANotA(hit) {
			t.Errorf("containsCJKANotA(%q) = false, want true", hit)
		}
	}
	for _, miss := range []string{"不对", "不行", "还是不对", "研究一下"} {
		if containsCJKANotA(miss) {
			t.Errorf("containsCJKANotA(%q) = true, want false", miss)
		}
	}
	if !isCJKANotAQuestion("对不对？") {
		t.Error("interrogative suffix ? must qualify")
	}
	if isCJKANotAQuestion("对不对，重来") {
		t.Error("non-interrogative continuation must not qualify")
	}
}
