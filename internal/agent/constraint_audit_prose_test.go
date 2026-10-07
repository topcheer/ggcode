package agent

import "testing"

// #prose-gap (DeCRIM, arXiv 2410.06458): list-free multi-request prose
// must reach the per-item constraint audit when >=2 verb-initial
// constraint clauses are present, and must NOT activate for narrative,
// questions, or single asks.

func TestProseConstraintsZhMultiRequest(t *testing.T) {
	// The canonical prose multi-request: four constraints, zero list markers.
	got := buildConstraints("支持 Windows 和 macOS，还要写文档，测试用 table-driven，别改公开 API")
	if len(got) != 4 {
		t.Fatalf("expected 4 prose constraints, got %d: %q", len(got), got)
	}
	wantFirst := "支持 Windows 和 macOS"
	if got[0] != wantFirst {
		t.Errorf("first constraint = %q, want %q", got[0], wantFirst)
	}
}

func TestProseConstraintsEnMultiRequest(t *testing.T) {
	got := buildConstraints("Fix the nil map write. Add a regression test. Don't change the public API.")
	if len(got) != 3 {
		t.Fatalf("expected 3 prose constraints, got %d: %q", len(got), got)
	}
}

func TestProseConstraintsNegativeSingleAsk(t *testing.T) {
	for _, p := range []string{"修复这个 bug", "Fix this bug"} {
		if got := buildConstraints(p); got != nil {
			t.Errorf("single ask %q should not audit, got %q", p, got)
		}
	}
}

func TestProseConstraintsNegativeQuestions(t *testing.T) {
	p := "How do I fix this? Should I add tests?"
	if got := buildConstraints(p); got != nil {
		t.Errorf("question-only message should not audit, got %q", got)
	}
}

func TestProseConstraintsNegativeNarrative(t *testing.T) {
	// Descriptive sentence: first clause is not verb-initial ("这个函数…"),
	// only one qualifying clause in total.
	p := "这个函数负责处理用户输入，确保数据合法"
	if got := buildConstraints(p); got != nil {
		t.Errorf("narrative description should not audit, got %q", got)
	}
}

func TestProseConstraintsNegativeOrdinalProse3095(t *testing.T) {
	// #3095 shape: incoherent ordinals are background explanation, not a
	// task list; falling through to the prose extractor must STILL yield
	// nil (clauses like "这是历史原因" are not verb-initial).
	p := "请重构这个模块。\n三、这是历史原因导致的。\n七、这是设计取舍。"
	if got := buildConstraints(p); got != nil {
		t.Errorf("#3095 ordinal prose should not audit, got %q", got)
	}
}

func TestProseConstraintsNegativeOverflow(t *testing.T) {
	// >8 qualifying clauses reads as notes, not a compact request.
	p := "支持 A，支持 B，支持 C，支持 D，支持 E，支持 F，支持 G，支持 H，支持 I"
	if got := buildConstraints(p); got != nil {
		t.Errorf("overflow prose should not audit, got %q", got)
	}
}

func TestProseConstraintsStructuredListStillWins(t *testing.T) {
	p := "任务如下：\n1. 支持平台 X\n2. 编写迁移文档"
	got := buildConstraints(p)
	if len(got) != 2 {
		t.Fatalf("structured list should take priority, got %d: %q", len(got), got)
	}
	if got[0] != "支持平台 X" || got[1] != "编写迁移文档" {
		t.Errorf("structured items = %q", got)
	}
}

func TestProseConstraintsImperativeAndSplit(t *testing.T) {
	// English "and"-joined imperatives decompose into two clauses.
	got := buildConstraints("Add a config flag and update the docs.")
	if len(got) != 2 {
		t.Fatalf("expected 2 clauses from and-joined imperatives, got %d: %q", len(got), got)
	}
}

func TestProseConstraintsCodeFenceSkipped(t *testing.T) {
	// Constraint-looking text inside a code fence must not be extracted.
	p := "```\nfix this\nadd that\n```\n看看这段日志。"
	if got := buildConstraints(p); got != nil {
		t.Errorf("fenced code must not be extracted as constraints, got %q", got)
	}
}

func TestProseConstraintsDedup(t *testing.T) {
	got := buildConstraints("Add a config flag。Add a config flag。Update the docs。")
	if len(got) != 2 {
		t.Fatalf("duplicate clauses should dedup, got %d: %q", len(got), got)
	}
}
