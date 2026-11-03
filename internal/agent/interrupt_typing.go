package agent

import (
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/provider"
)

type InterruptionKind string

const (
	InterruptionAddition    InterruptionKind = "addition"
	InterruptionRevision    InterruptionKind = "revision"
	InterruptionRetractStop InterruptionKind = "retraction-stop"
	InterruptionInsert      InterruptionKind = "inserted-task"
)

var retractPhrases = map[string]struct{}{

	"stop": {}, "stophalt": {}, "halt": {}, "abort": {}, "cancel": {},
	"cancelit": {}, "cancelthis": {}, "cancelthetask": {}, "stopeverything": {},
	"stopit": {}, "stopthetask": {}, "stopworking": {}, "pleasestop": {},
	"pleasecancel": {}, "nevermind": {}, "forgetit": {}, "dont": {},
	"dontdoit": {}, "callitoff": {},

	"取消": {}, "取消任务": {}, "取消这个任务": {}, "取消了吧": {}, "停止": {},
	"停止任务": {}, "停止当前任务": {}, "别做了": {}, "别做": {}, "算了": {},
	"算了吧": {}, "放弃": {}, "放弃任务": {}, "撤销": {}, "撤销全部": {},
	"不用做了": {}, "不用了": {}, "请取消": {}, "请停止": {}, "请别做了": {},
}

var revisionMarkers = []string{

	"instead,", "actually, do", "change it to", "instead of that", "instead of this",
	"revise the goal", "change the goal", "new direction", "different goal",
	"switch to plan",
	"改成方案", "换成方案", "不要这个了要", "换个思路", "换个方向",
	"改目标", "需求变了", "需求改了",
}

func normalizeRetract(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func classifyInterruption(text string) InterruptionKind {
	if text == "" {
		return InterruptionAddition
	}
	if _, ok := retractPhrases[normalizeRetract(text)]; ok {
		return InterruptionRetractStop
	}
	low := strings.ToLower(text)
	for _, m := range revisionMarkers {
		if strings.Contains(low, m) {
			return InterruptionRevision
		}
	}
	if looksLikeInsertedTask(text) {
		return InterruptionInsert
	}
	return InterruptionAddition
}

const (
	insertMinRunes        = 120
	insertMinSentenceUnit = 2
)

func looksLikeInsertedTask(text string) bool {
	trimmed := strings.TrimSpace(text)
	if len([]rune(trimmed)) < insertMinRunes {
		return false
	}
	units := 0
	for _, r := range trimmed {
		switch r {
		case '.', '!', '?', '。', '！', '？', ';', '；':
			units++
		}
	}
	return units >= insertMinSentenceUnit
}

func firstTextBlock(blocks []provider.ContentBlock) string {
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return b.Text
		}
	}
	return ""
}

func interruptionPreamble(kind InterruptionKind) string {
	switch kind {
	case InterruptionRevision:
		return "New user guidance arrived while you were working, and it REVISES the goal. " +
			"Treat it as higher-priority context: the previous approach is superseded - " +
			"abandon remaining steps that only served the old goal (do not revert completed work " +
			"unless asked), then continue under the revised goal."
	case InterruptionRetractStop:
		return "The user RETRACTED the request. The run stops here - do not start any new work toward " +
			"the original goal. This notice stays in context so a future resume knows the task was " +
			"withdrawn: report what was already done, revert nothing automatically, and await new " +
			"instructions."
	case InterruptionInsert:
		return "New user guidance arrived while you were working, and it appears to be a SEPARATE task " +
			"unrelated to your current main thread. Consider handling it as an insertion rather than a " +
			"goal change: (1) if the task is self-contained, consider delegating it to a sub-agent via " +
			"spawn_agent so your main context stays clean; (2) note the suspended main task in one line " +
			"before switching; (3) after the inserted task completes, resume the suspended main thread " +
			"unless the user says otherwise. If it is actually a small follow-up to the current work, " +
			"just fold it into your plan and continue."
	default:
		return "New user guidance arrived while you were working. Treat it as higher-priority context, adjust your plan immediately if needed, and then continue."
	}
}
