package agent

// Interruption typing (InterruptBench, arXiv:2604.00892): the three
// formalized user-interruption types are addition (new requirement),
// revision (goal change), and retraction (withdrawal). ggcode's mid-run
// steering channel treated every injected message as generic "new guidance"
// (addition semantics), so a bare retraction ("stop"/"算了") let the loop
// keep burning LLM turns on a withdrawn task, and a revision never told the
// model that old-goal-only artifacts were dead weight.
//
// Classification is deterministic and deliberately narrow: RetractStop fires
// only when the ENTIRE message is a stop phrase (exact set match after
// normalization), so "stop adding tests" or "cancel the timer refactor" -
// which carry task content - stay Addition. Revision needs an explicit
// redirection marker. Everything else keeps the existing addition preamble;
// behavior for addition is byte-identical to pre-typing.

import (
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/provider"
)

// InterruptionKind classifies a mid-run user interruption.
type InterruptionKind string

const (
	// InterruptionAddition: new requirement layered on the current goal.
	InterruptionAddition InterruptionKind = "addition"
	// InterruptionRevision: the user changed the goal; old-goal-only work
	// is superseded.
	InterruptionRevision InterruptionKind = "revision"
	// InterruptionRetractStop: the user withdrew the request entirely.
	// The run aborts without another LLM call; the injected notice survives
	// in context for the next resume.
	InterruptionRetractStop InterruptionKind = "retraction-stop"
)

// retractPhrases: whole-message stop phrases (normalized: lowercased,
// whitespace/punctuation stripped). A message classifies as retraction only
// if its normalized form is IN this set - substring matches are rejected so
// constraint-bearing text ("stop adding tests") can never retract a task.
var retractPhrases = map[string]struct{}{
	// English imperatives (+ leading please / trailing particles absorbed
	// by normalization).
	"stop": {}, "stophalt": {}, "halt": {}, "abort": {}, "cancel": {},
	"cancelit": {}, "cancelthis": {}, "cancelthetask": {}, "stopeverything": {},
	"stopit": {}, "stopthetask": {}, "stopworking": {}, "pleasestop": {},
	"pleasecancel": {}, "nevermind": {}, "forgetit": {}, "dont": {},
	"dontdoit": {}, "callitoff": {},
	// Chinese equivalents.
	"取消": {}, "取消任务": {}, "取消这个任务": {}, "取消了吧": {}, "停止": {},
	"停止任务": {}, "停止当前任务": {}, "别做了": {}, "别做": {}, "算了": {},
	"算了吧": {}, "放弃": {}, "放弃任务": {}, "撤销": {}, "撤销全部": {},
	"不用做了": {}, "不用了": {}, "请取消": {}, "请停止": {}, "请别做了": {},
}

// revisionMarkers: explicit redirection signal inside the message.
var revisionMarkers = []string{
	"instead", "switch to", "rather than", "actually, do", "change it to",
	"make it", "revise the goal", "new direction",
	"改成", "换成", "改用", "改为", "换成方案", "不要这个了要", "换个思路",
}

// normalizeRetract folds a candidate phrase: lowercase, drop all whitespace
// and punctuation (ASCII + CJK) so "Stop!  " / "。算了吧" hit the same entry.
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

// classifyInterruption maps one interruption message to its kind.
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
	return InterruptionAddition
}

// firstTextBlock returns the text of the first non-blank text block
// (non-text blocks carry no directive semantics; empty text stays addition).
func firstTextBlock(blocks []provider.ContentBlock) string {
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return b.Text
		}
	}
	return ""
}

// interruptionPreamble returns the injected framing text for a kind. The
// addition preamble is byte-identical to the pre-typing guidance text so
// existing steering behavior is unchanged.
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
	default:
		return "New user guidance arrived while you were working. Treat it as higher-priority context, adjust your plan immediately if needed, and then continue."
	}
}
