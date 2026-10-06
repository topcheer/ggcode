package agent

import (
	"strings"
	"testing"
)

// r392 (AREX constraint-wise audit): structurally-listed multi-requirement
// tasks get a per-item audit prompt; prose and single-item tasks do not.
func TestConstraintAuditListedTask(t *testing.T) {
	c := newConstraintAuditState()
	c.observe("Please do the following:\n1. add retry to the uploader\n2. fix the config parser bug\n3. write tests for both")
	msg := c.checkAndInject()
	if msg == "" {
		t.Fatal("multi-item task must trigger the audit")
	}
	for _, want := range []string{"[done]", "[not done]", "add retry to the uploader", "3"} {
		if !strings.Contains(msg, want) {
			t.Errorf("audit prompt missing %q: %s", want, msg)
		}
	}
	if c.checkAndInject() != "" {
		t.Error("audit is one-shot per run")
	}
}

func TestConstraintAuditBulletList(t *testing.T) {
	c := newConstraintAuditState()
	c.observe("Tasks:\n- add validation\n- remove the legacy path\n- update docs")
	if msg := c.checkAndInject(); msg == "" {
		t.Fatal("bullet list task must trigger")
	}
}

// #3093: Chinese ordinal list forms (步骤1：/ 一、) must be recognized,
// while prose starting with ordinal-like characters must not be.
func TestConstraintAuditChineseOrdinals(t *testing.T) {
	c := newConstraintAuditState()
	c.observe("请完成：\n步骤1：修改配置解析\n步骤2：补充单元测试\n步骤3：更新文档说明")
	msg := c.checkAndInject()
	if msg == "" {
		t.Fatal("步骤-style list must trigger the audit")
	}
	if !strings.Contains(msg, "修改配置解析") || !strings.Contains(msg, "更新文档说明") {
		t.Errorf("audit must list every 步骤 item verbatim: %s", msg)
	}

	c2 := newConstraintAuditState()
	c2.observe("任务清单：\n一、修复上传重试\n二、清理废弃代码\n三、写回归测试")
	if msg := c2.checkAndInject(); msg == "" {
		t.Fatal("一、二、ordinal list must trigger the audit")
	} else if !strings.Contains(msg, "修复上传重试") {
		t.Errorf("audit must list ordinal items verbatim: %s", msg)
	}

	// Prose starting with ordinal-like chars must NOT be list items.
	c3 := newConstraintAuditState()
	c3.observe("一定要修复这个重试逻辑，一定要加日志，一定要有测试")
	if msg := c3.checkAndInject(); msg != "" {
		t.Errorf("prose sentence must not trigger audit, got: %s", msg)
	}
}

func TestConstraintAuditIgnoresNonTasks(t *testing.T) {
	cases := []string{
		"Fix the login bug", // single prose item
		"List all files in the config package:\n- a\n- b", // read-only task
		"Explain this log:\n```go\n1. x\n2. y\n```",       // fenced code only
		"1. only one item here",                           // below min items
	}
	for _, p := range cases {
		c := newConstraintAuditState()
		c.observe(p)
		if msg := c.checkAndInject(); msg != "" {
			t.Errorf("prompt %q must not trigger audit, got: %s", p, msg)
		}
	}
}

func TestConstraintAuditCodeFenceSkipped(t *testing.T) {
	c := newConstraintAuditState()
	c.observe("Do:\n1. real task one\n2. real task two\n```\n3. pasted log line\n4. pasted log line\n```")
	msg := c.checkAndInject()
	if msg == "" {
		t.Fatal("two real items should trigger")
	}
	if strings.Contains(msg, "pasted log") {
		t.Errorf("code-fenced lines must not become constraints: %s", msg)
	}
}

func TestConstraintAuditObserveOnceAndReset(t *testing.T) {
	c := newConstraintAuditState()
	c.observe("1. first task\n2. second task")
	c.observe("1. other\n2. other2") // ignored: first message defines the task
	msg := c.checkAndInject()
	if !strings.Contains(msg, "first task") {
		t.Errorf("first task list must win: %s", msg)
	}
	c.reset()
	if c.checkAndInject() != "" {
		t.Error("reset clears state")
	}
}
