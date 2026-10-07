package tool

// #3492 probe: a node sitting EXACTLY at cap depth with no outgoing edges
// is a COMPLETE closure - it must not be flagged Truncated. Only a capped
// node with a real unexplored dep edge sets Truncated.

import (
	"strings"
	"testing"
)

func TestIssue3492_LeafAtCapDepthNotTruncated(t *testing.T) {
	// s1->s2->s3->s4, cap 3: s4 is popped at depth 3 (capped) and is a leaf.
	lookup := closureSkillLookup{
		"s1": mkSkill("s1", "s2"),
		"s2": mkSkill("s2", "s3"),
		"s3": mkSkill("s3", "s4"),
		"s4": mkSkill("s4"),
	}
	c := DependencyClosure(mkSkill("s1", "s2"), lookup, 3)
	if c.Truncated {
		t.Fatalf("complete closure with leaf at cap depth must not be Truncated: %+v", c)
	}
	if got := c.String(); strings.Contains(got, "depth capped") {
		t.Fatalf("complete closure must not render a capped suffix, got %q", got)
	}
}

func TestIssue3492_NodeAtCapWithUnvisitedDepIsTruncated(t *testing.T) {
	// s1->s2->s3->s4->s5, cap 3: s4 is popped at depth 3 (capped) and its
	// dep s5 is unexplored - Truncated must be set (real truncation).
	lookup := closureSkillLookup{
		"s1": mkSkill("s1", "s2"),
		"s2": mkSkill("s2", "s3"),
		"s3": mkSkill("s3", "s4"),
		"s4": mkSkill("s4", "s5"),
		"s5": mkSkill("s5"),
	}
	c := DependencyClosure(mkSkill("s1", "s2"), lookup, 3)
	if !c.Truncated {
		t.Fatalf("capped node with unvisited dep s5 must be Truncated: %+v", c)
	}
}

func TestIssue3492_CappedNodeAllDepsVisitedNotTruncated(t *testing.T) {
	// Diamond: s1 -> {s2, s3}; s3 deps s2 (already visited). Cap 1: s2/ss3
	// popped at depth 1; s3's dep s2 is visited -> complete, not truncated.
	lookup := closureSkillLookup{
		"s1": mkSkill("s1", "s2", "s3"),
		"s2": mkSkill("s2"),
		"s3": mkSkill("s3", "s2"),
	}
	c := DependencyClosure(mkSkill("s1", "s2", "s3"), lookup, 1)
	if c.Truncated {
		t.Fatalf("capped node whose only dep is already visited is complete: %+v", c)
	}
}
