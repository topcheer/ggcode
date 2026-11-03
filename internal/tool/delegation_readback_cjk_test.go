package tool

import "testing"

func TestContentWordSet_CJKParaphraseNotMissing(t *testing.T) {
	cases := []struct {
		name      string
		criterion string
		readBack  string
		want      readBackCoverage
	}{
		{
			name:      "issue-repro: paraphrased Chinese criterion",
			criterion: "所有测试必须通过",
			readBack:  "我会运行测试来验证全部通过",
			want:      coverageFuzzy,
		},
		{
			name:      "issue-repro: verbatim Chinese copy passes",
			criterion: "所有测试必须通过",
			readBack:  "所有测试必须通过",
			want:      coveragePass,
		},
		{
			name:      "issue-repro: English paraphrase still passes",
			criterion: "all tests must pass",
			readBack:  "I will run tests to verify everything passes",
			want:      coveragePass,
		},
		{
			name:      "unrelated Chinese read-back stays missing",
			criterion: "所有测试必须通过",
			readBack:  "我会先重构数据库连接池代码",
			want:      coverageMiss,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := "## Acceptance Criteria\n1. " + tc.criterion + "\n"
			resp := readBackMarker + "\n" + tc.readBack + "\n"
			rep := compareReadBack(task, resp)
			if len(rep.Verdicts) != 1 {
				t.Fatalf("expected exactly 1 criterion verdict, got %+v", rep)
			}
			if got := rep.Verdicts[0]; got != tc.want {
				t.Fatalf("coverage = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestContentWordSet_CJKBigramsWellFormed(t *testing.T) {
	ws := contentWordSet("测试必须")
	for _, bg := range []string{"测试", "试必", "必须"} {
		if !ws[bg] {
			t.Fatalf("missing bigram %q in %v", bg, ws)
		}
	}

	ws = contentWordSet("跑 tests 跑")
	if !ws["跑"] {
		t.Fatal("single-char CJK run must register the lone character")
	}
	if !ws["test"] && !ws["tests"] {
		t.Fatalf("latin token in mixed input lost: %v", ws)
	}

	if ws := contentWordSet("pass"); !ws["pass"] {
		t.Fatal("latin-only regression")
	}
}
