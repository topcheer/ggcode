package agent

// #4149 companion tests: CJK bare negative words have no word boundaries
// (wordContains degenerates to substring matching for CJK), so everyday
// Chinese compounds embedding the triggers fired the sentiment escalation
// ladder on neutral diagnostic questions and ops instructions. These tests
// pin the two fix layers - neutral-carrier/straddle masking and the question
// gate - plus the genuine-feedback cases that must keep firing.

import "testing"

func TestDetectNegativeFeedbackCJKFalsePositives4149(t *testing.T) {
	cases := []string{
		// 停下/停一下 family: diagnostic questions and ops instructions.
		"服务为什么停下了？",       // question gate
		"进程停下了吗？",         // question gate
		"帮我停一下 nginx 再重启", // carrier 帮我停一下
		// 垃圾: GC is a daily topic in a Go repo.
		"调一下垃圾回收的参数", // carrier 垃圾回收
		"讲一下垃圾回收的原理", // carrier 垃圾回收
		// 算了: 计算/预算/结算/打算+了 carriers.
		"我计算了一下，发现超时了", // carrier 计算了
		"预算了五十块，够吗",    // carrier 预算了
		"结算页面有个 bug",   // carrier 结算
		"我打算了下个迭代",     // carrier 打算了
		// 我是说+语言 (#4129 CJK counterpart).
		"我是说中文的用户，请用中文回复", // carrier 我是说中文
		// 没用: neutral "not used" statements.
		"这个参数没用上", // carrier 没用上
		"我没用过这个库", // carrier 没用过
		// 不对: technical misalignment.
		"这两列数据不对齐", // carrier 不对齐
		// 没成功/坏了: diagnostic questions.
		"部署没成功吗？",      // question gate
		"磁盘坏了，数据还能恢复吗", // question gate
		// 别动/重来: embedded mid-compound (straddle carriers).
		"请区别动态路由和静态路由",  // 区别动态 -> 别动
		"让重庆来的同事审一下 PR", // carrier 重庆来
	}
	for _, msg := range cases {
		if got := detectNegativeFeedback(msg); got != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty (neutral)", msg, got)
		}
	}
}

func TestDetectNegativeFeedbackCJKGenuineStillFires(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"停下，别再跑了", negCatFrustration},
		{"停一下！你改错了", negCatRejection}, // 错了 outranks 停一下 in priority order // 停一下 heads the line
		{"不对，重新做", negCatRejection},
		{"错了，第三个文件错了", negCatRejection},
		{"算了，换个方案吧", negCatRedirection},
		{"重来，这次用 UDP", negCatRedirection},
		{"我是说左边那个文件", negCatRedirection},
		{"没用，这个方案完全没用", negCatFrustration},
		{"这代码就是垃圾", negCatFrustration},
		{"还是不行，又失败了", negCatRejection},
	}
	for _, c := range cases {
		if got := detectNegativeFeedback(c.msg); got != c.want {
			t.Errorf("detectNegativeFeedback(%q) = %q, want %q", c.msg, got, c.want)
		}
	}
}

func TestIsCJKQuestionLine(t *testing.T) {
	yes := []string{"服务为什么停下了？", "部署没成功吗？", "行不行呢", "能重启?"}
	no := []string{"不对，重新做", "停一下！", "磁盘坏了，恢复一下"}
	for _, s := range yes {
		if !isCJKQuestionLine(s) {
			t.Errorf("isCJKQuestionLine(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isCJKQuestionLine(s) {
			t.Errorf("isCJKQuestionLine(%q) = true, want false", s)
		}
	}
}
