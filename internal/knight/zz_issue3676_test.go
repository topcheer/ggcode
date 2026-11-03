package knight

import (
	"testing"
)

func TestIssue3676_DominantRatioNotContainsAny(t *testing.T) {

	mixed := skillSimilarityFingerprint("deploy-vercel-prod",
		"deploy the vercel project build preview alias dns edge cache prod 部署", "")
	if cjkDominantFingerprint(mixed) {
		t.Fatalf("9 ASCII tokens + 1 CJK bigram must not be dominant: %v", mixed)
	}
	if similarityDuplicateThreshold(mixed) != similarityBaseDuplicateThreshold {
		t.Fatal("mixed fingerprint must use the base 0.6 gate")
	}

	pureCJK := skillSimilarityFingerprint("自动化数据库备份", "自动备份", "")
	if !cjkDominantFingerprint(pureCJK) {
		t.Fatalf("pure-Chinese fingerprint must be dominant: %v", pureCJK)
	}
	if similarityDuplicateThreshold(pureCJK) != similarityCJKDuplicateThreshold {
		t.Fatal("pure-Chinese fingerprint must use the strict 0.75 gate")
	}
}

func TestIssue3676_FullWidthPunctuationDoesNotRaiseGate(t *testing.T) {

	fp := skillSimilarityFingerprint("deploy-vercel",
		"build, preview, alias dns edge cache and prod，done", "")
	if cjkDominantFingerprint(fp) {
		t.Fatalf("single full-width comma must not make the fp CJK-dominant: %v", fp)
	}
}

func TestIssue3676_PairThresholdDirectionConsistent(t *testing.T) {
	eng := skillSimilarityFingerprint("deploy-vercel", "deploy the vercel project to production", "")
	cjk := skillSimilarityFingerprint("部署站点", "部署站点到生产环境", "")
	if got := duplicateThresholdForPair(eng, cjk); got != similarityBaseDuplicateThreshold {
		t.Fatalf("mixed-script pair gate = %v, want 0.6 (either direction)", got)
	}
	if got := duplicateThresholdForPair(cjk, eng); got != similarityBaseDuplicateThreshold {
		t.Fatalf("mixed-script pair gate reversed = %v, want 0.6", got)
	}
	if got := duplicateThresholdForPair(cjk, cjk); got != similarityCJKDuplicateThreshold {
		t.Fatalf("both-CJK pair gate = %v, want 0.75 (#3637 guard)", got)
	}
}

func TestIssue3676_LeakBandDuplicateNowCaught(t *testing.T) {

	cand := skillSimilarityFingerprint("deploy-vercel-prod",
		"deploy the vercel project build preview alias dns edge cache prod 部署", "")
	exist := skillSimilarityFingerprint("deploy-vercel",
		"deploy the vercel project build preview alias dns edge cache", "")
	j := jaccardSimilarity(cand, exist)
	if j < 0.6 || j >= 0.75 {
		t.Fatalf("fixture jaccard %v not in the leak band [0.6,0.75)", j)
	}
	if j < duplicateThresholdForPair(cand, exist) {
		t.Fatalf("[0.6,0.75) duplicate must be caught now: jaccard=%v gate=%v", j, duplicateThresholdForPair(cand, exist))
	}
}

func TestIssue3676_LeakBandCaughtViaActiveLoop(t *testing.T) {

	k := &Knight{}
	c := SkillCandidate{
		Name:        "deploy-vercel-prod",
		Description: "deploy the vercel project build preview alias dns edge cache prod 部署",
	}
	active := []*SkillEntry{{
		Name: "deploy-vercel",
		Meta: SkillMeta{
			Name:        "deploy-vercel",
			Description: "deploy the vercel project build preview alias dns edge cache",
		},
	}}
	if !k.isKnownCandidate(c, active, nil) {
		t.Fatal("[0.6,0.75) duplicate must be caught via the ACTIVE loop too")
	}
}

func TestIssue3676_SameFamilyCJKStillGuarded(t *testing.T) {

	a := skillSimilarityFingerprint("自动化数据库备份", "自动化数据库备份任务", "")
	b := skillSimilarityFingerprint("自动化数据库恢复", "自动化数据库恢复任务", "")
	j := jaccardSimilarity(a, b)
	if j >= duplicateThresholdForPair(a, b) {
		t.Fatalf("same-family CJK pair (jaccard=%v) wrongly flagged as duplicate", j)
	}
}
