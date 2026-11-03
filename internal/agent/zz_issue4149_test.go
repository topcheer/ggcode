package agent

import "testing"

func TestDetectNegativeFeedbackCJKFalsePositives4149(t *testing.T) {
	cases := []string{

		"服务为什么停下了？",
		"进程停下了吗？",
		"帮我停一下 nginx 再重启",

		"调一下垃圾回收的参数",
		"讲一下垃圾回收的原理",

		"我计算了一下，发现超时了",
		"预算了五十块，够吗",
		"结算页面有个 bug",
		"我打算了下个迭代",

		"我是说中文的用户，请用中文回复",

		"这个参数没用上",
		"我没用过这个库",

		"这两列数据不对齐",

		"部署没成功吗？",
		"磁盘坏了，数据还能恢复吗",

		"请区别动态路由和静态路由",
		"让重庆来的同事审一下 PR",
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
		{"停一下！你改错了", negCatRejection},
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
