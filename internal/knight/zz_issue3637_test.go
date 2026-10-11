package knight

import "testing"

// Probe for #3637: isKnownCandidate uses
// jaccardSimilarity(candFP, skillSimilarityFingerprint(...)) >=
// similarityDuplicateThreshold(candFP) (scheduler.go): 0.6 for English, 0.75
// for CJK-dominant fingerprints. These tests pin the decision at that seam.

// Case 1 (the bug): same-domain same-prefix Chinese family. Before the fix,
// per-rune tokens scored 6/10 = 0.60 >= 0.6 and the survivor was silently
// dropped via queue.Remove. Bigram tokens keep it high — name-only 5/9 =
// 0.556, full fingerprint 9/15 = 0.60 — so CJK-dominant fingerprints get
// the 0.75 threshold and the pair is NOT a duplicate.
func TestIssue3637ChineseSamePrefixFamilyNotDuplicate(t *testing.T) {
	a := skillSimilarityFingerprint("自动化数据库备份", "自动化执行数据库备份任务", "")
	b := skillSimilarityFingerprint("自动化数据库恢复", "自动化执行数据库恢复任务", "")
	if got := jaccardSimilarity(a, b); got >= similarityDuplicateThreshold(a) {
		t.Fatalf("备份 vs 恢复 family must NOT reach the CJK duplicate threshold, got %.4f", got)
	}
	// Name-only variant (issue's exact example).
	an := skillSimilarityFingerprint("自动化数据库备份", "", "")
	bn := skillSimilarityFingerprint("自动化数据库恢复", "", "")
	if got := jaccardSimilarity(an, bn); got >= similarityDuplicateThreshold(an) {
		t.Fatalf("name-only 备份 vs 恢复 must stay below threshold, got %.4f", got)
	}
	if !cjkDominantFingerprint(an) {
		t.Fatal("pure-Chinese fingerprint must be classified CJK-dominant")
	}
}

// Case 2: the issue's shorter control pair stays well below the threshold
// (bigram 2/6 = 0.333; single-rune baseline was 3/7 ≈ 0.43, no trigger).
func TestIssue3637ShortChinesePairStillNotDuplicate(t *testing.T) {
	a := skillSimilarityFingerprint("数据库备份", "", "")
	b := skillSimilarityFingerprint("数据库恢复", "", "")
	if got := jaccardSimilarity(a, b); got >= 0.6 {
		t.Fatalf("数据库备份 vs 数据库恢复 must stay below 0.6, got %.4f", got)
	}
}

// Genuine duplicates must still be caught: identical names (with function
// words differing only) must score 1.0 after stopword stripping.
func TestIssue3637IdenticalSkillsStillDuplicate(t *testing.T) {
	a := skillSimilarityFingerprint("自动化数据库的备份", "对数据库的备份", "")
	b := skillSimilarityFingerprint("自动化数据库备份", "数据库备份", "")
	if got := jaccardSimilarity(a, b); got < similarityDuplicateThreshold(a) {
		t.Fatalf("function-word-only variants must remain duplicates, got %.4f", got)
	}
}

// Case 3: English behavior is completely unchanged (regression guard).
// Same assertions as p0_hardening_test.go's expectations: stopwords and
// len<2 tokens are filtered, jaccard identical to the pre-fix values.
func TestIssue3637EnglishBehaviorUnchanged(t *testing.T) {
	tok := tokenizeForSimilarity("The a build verification of the DB")
	for want := range map[string]struct{}{"build": {}, "verification": {}, "db": {}} {
		if _, ok := tok[want]; !ok {
			t.Fatalf("english token %q missing: %v", want, tok)
		}
	}
	for _, banned := range []string{"the", "a", "of"} {
		if _, ok := tok[banned]; ok {
			t.Fatalf("english stopword %q must be filtered: %v", banned, tok)
		}
	}
	if _, ok := tokenizeForSimilarity("a I x")["i"]; ok {
		t.Fatal("len<2 english tokens must still be filtered")
	}

	// English family pair keeps its pre-fix score of 2/4 = 0.5 (both
	// "verification" and "validation" survive stopword filtering; the ASCII
	// path is untouched by the CJK change).
	x := tokenizeForSimilarity("build verification pipeline")
	y := tokenizeForSimilarity("build validation pipeline")
	if cjkDominantFingerprint(x) {
		t.Fatal("english fingerprint must not be CJK-dominant")
	}
	if got := jaccardSimilarity(x, y); got != 0.5 {
		t.Fatalf("english jaccard changed: got %.4f want %.4f", got, 0.5)
	}
}
