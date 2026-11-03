package memory

import "testing"

func TestIssue3311_ComplaintsNotCaptured(t *testing.T) {

	if isPreferenceSentence("不对，应用启动失败了") {
		t.Fatal("例 A：抱怨句不得入库（'用' 命中 '应用' 子串）")
	}

	if isPreferenceSentence("instead of fixing the cache it broke because tests were stale") {
		t.Fatal("例 B：complaint must not capture ('use' hit inside 'because')")
	}

	if isPreferenceSentence("不对，更换设备后就不行了") {
		t.Fatal("更换 contains 换 - complaint must not capture")
	}
	if isPreferenceSentence("不对，这个任务跑单失败了") {
		t.Fatal("跑单 contains 跑 - complaint must not capture")
	}
	if isPreferenceSentence("no, the house schema is wrong") {
		t.Fatal("house contains use - complaint must not capture")
	}
}

func TestIssue3311_RealCorrectionsStillCapture(t *testing.T) {
	pos := []struct{ name, sent string }{
		{"中文自足标记", "别用 npm 了"},
		{"中文改用", "不对，改用 pnpm 跑测试"},
		{"中文换成", "换成 sqlite 吧"},
		{"中文多字动词", "不对，使用 pnpm 来安装"},
		{"英文 no,use", "no, use pnpm instead"},
		{"英文 instead+run", "instead, run the flaky test serially"},
		{"英文 prefer", "instead of npm, prefer pnpm from now on"},
	}
	for _, p := range pos {
		if !isPreferenceSentence(p.sent) {
			t.Fatalf("%s: 真纠正必须入库: %q", p.name, p.sent)
		}
	}
}
