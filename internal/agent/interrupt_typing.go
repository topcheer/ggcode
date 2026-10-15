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
// redirection marker. Insert (a complex task unrelated to the main thread)
// needs a composite signal - length >= 120 runes AND >= 2 sentence units -
// so short clarifications never misclassify; prefer miss-to-Addition over
// false positives. Everything else keeps the existing addition preamble;
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
	// InterruptionInsert: a complex SEPARATE task injected mid-run, unrelated
	// to the current main thread. The preamble suggests delegating it to a
	// sub-agent and resuming the suspended main thread afterwards. Advisory
	// only - misclassification costs one extra suggestion sentence, so the
	// detector errs toward Addition.
	InterruptionInsert InterruptionKind = "inserted-task"
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
	// #4121: bare "make it" removed - it fired on ordinary refinement
	// instructions ("make it more robust") and the Revision preamble made
	// the model abandon valid in-flight work. Explicit redirection forms
	// ("change it to", "revise the goal") remain.
	"revise the goal", "new direction",
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
	if looksLikeInsertedTask(text) {
		return InterruptionInsert
	}
	return InterruptionAddition
}

// insertedTask thresholds: a message this long AND this structured is a
// plausible self-contained task brief rather than an in-thread correction.
const (
	insertMinRunes        = 120
	insertMinSentenceUnit = 2
)

// looksLikeInsertedTask reports whether text plausibly carries a complex
// separate task (not an in-thread clarification). Composite signal, both
// required: total length >= insertMinRunes AND >= insertMinSentenceUnit
// sentence-ending units. Conservative: anything shorter or single-sentence
// stays Addition.
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
