package knight

import "testing"

func TestIssue3637ChineseSamePrefixFamilyNotDuplicate(t *testing.T) {
	a := skillSimilarityFingerprint("自动化数据库备份", "自动化执行数据库备份任务", "")
	b := skillSimilarityFingerprint("自动化数据库恢复", "自动化执行数据库恢复任务", "")
	if got := jaccardSimilarity(a, b); got >= similarityDuplicateThreshold(a) {
		t.Fatalf("备份 vs 恢复 family must NOT reach the CJK duplicate threshold, got %.4f", got)
	}

	an := skillSimilarityFingerprint("自动化数据库备份", "", "")
	bn := skillSimilarityFingerprint("自动化数据库恢复", "", "")
	if got := jaccardSimilarity(an, bn); got >= similarityDuplicateThreshold(an) {
		t.Fatalf("name-only 备份 vs 恢复 must stay below threshold, got %.4f", got)
	}
	if !cjkDominantFingerprint(an) {
		t.Fatal("pure-Chinese fingerprint must be classified CJK-dominant")
	}
}

func TestIssue3637ShortChinesePairStillNotDuplicate(t *testing.T) {
	a := skillSimilarityFingerprint("数据库备份", "", "")
	b := skillSimilarityFingerprint("数据库恢复", "", "")
	if got := jaccardSimilarity(a, b); got >= 0.6 {
		t.Fatalf("数据库备份 vs 数据库恢复 must stay below 0.6, got %.4f", got)
	}
}

func TestIssue3637IdenticalSkillsStillDuplicate(t *testing.T) {
	a := skillSimilarityFingerprint("自动化数据库的备份", "对数据库的备份", "")
	b := skillSimilarityFingerprint("自动化数据库备份", "数据库备份", "")
	if got := jaccardSimilarity(a, b); got < similarityDuplicateThreshold(a) {
		t.Fatalf("function-word-only variants must remain duplicates, got %.4f", got)
	}
}

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

	x := tokenizeForSimilarity("build verification pipeline")
	y := tokenizeForSimilarity("build validation pipeline")
	if cjkDominantFingerprint(x) {
		t.Fatal("english fingerprint must not be CJK-dominant")
	}
	if got := jaccardSimilarity(x, y); got != 0.5 {
		t.Fatalf("english jaccard changed: got %.4f want %.4f", got, 0.5)
	}
}
