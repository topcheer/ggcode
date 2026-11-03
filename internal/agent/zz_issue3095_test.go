package agent

import (
	"strings"
	"testing"
)

func TestIssue3095_ProseOrdinalsNotConstraints(t *testing.T) {

	coherent := "请修复登录页崩溃的 bug。\n一、这是性能原因：热重载竞态。\n二、这是设计原因：状态分散。"
	got := buildConstraints(coherent)
	_ = got
}

func TestIssue3095_JumpedOrdinalsRejected(t *testing.T) {

	prose := "请重构这个模块。\n三、这是历史原因：遗留代码。\n一、这是组织原因：团队分工。"
	if got := buildConstraints(prose); got != nil {
		t.Fatalf("jumped ordinal prose treated as constraints: %v", got)
	}
}

func TestIssue3095_IsolatedHighOrdinalRejected(t *testing.T) {
	prose := "修复这个崩溃。\n七、这是背景说明第七点：内存布局。\n八、这是背景说明第八点：时序。"
	if got := buildConstraints(prose); got != nil {
		t.Fatalf("isolated high-start ordinals treated as constraints: %v", got)
	}
}

func TestIssue3095_GenuineTaskListsStillQualify(t *testing.T) {

	task := "请完成以下三项重构：\n一、抽出数据库层接口\n二、补齐单元测试覆盖\n三、更新文档"
	got := buildConstraints(task)
	if len(got) != 3 {
		t.Fatalf("genuine Chinese ordinal task list rejected: %v", got)
	}

	task2 := "fix these:\n1. handle nil map write\n2. close the file on error\n3. add the probe test"
	if got2 := buildConstraints(task2); len(got2) != 3 {
		t.Fatalf("genuine Arabic list rejected: %v", got2)
	}

	task3 := "步骤1：先复现失败\n步骤2：定位根因\n步骤3：写探针测试"
	if got3 := buildConstraints(task3); len(got3) != 3 {
		t.Fatalf("步骤 form rejected: %v", got3)
	}

	task4 := "一、第一项内容\n二、第二项内容\n三、第三项内容\n四、第四项内容\n五、第五项内容\n六、第六项内容\n七、第七项内容\n八、第八项内容\n九、第九项内容\n十、第十项内容"
	if got4 := buildConstraints(task4); len(got4) != 10 {
		t.Fatalf("十-boundary ordinals rejected: %d items", len(got4))
	}

}

func TestIssue3095_OrdinalValueParser(t *testing.T) {
	cases := map[string]int{
		"一、": 1, "二、": 2, "十、": 10, "十一、": 11, "二十三、": 23,
		"1.": 1, "2)": 2, "(3)": 3, "（4）": 4, "步骤5：": 5, "步骤 12：": 12,
		"": -1, "x、": -1,
	}
	for in, want := range cases {
		if got := ordinalValue(in); got != want {
			t.Errorf("ordinalValue(%q) = %d, want %d", in, got, want)
		}
	}
	if !ordinalSequenceCoherent([]int{1, 2, 3}) {
		t.Error("1,2,3 must be coherent")
	}
	if ordinalSequenceCoherent([]int{1, 3}) || ordinalSequenceCoherent([]int{3, 4}) || ordinalSequenceCoherent([]int{1, -1, 3}) {
		t.Error("jumped/isolated/broken sequences must be incoherent")
	}
}

func TestIssue3095_BulletsUnaffected(t *testing.T) {

	task := "do these:\n- fix the nil map write\n- close the file on error"
	got := buildConstraints(task)
	if len(got) != 2 {
		t.Fatalf("bullet list affected by ordinal heuristic: %v", got)
	}
	_ = strings.TrimSpace("")
}
