package agent

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
)

// Ask Quality Gate (r27, research round sa-59 gap).
//
// Research basis: arXiv 2606.03135 "Uncertainty-Aware Clarification in LLM
// Agents with Information Gain" (ICML 2026) and SAGE-Agent (ACL 2026
// Findings, 2026.acl.2028). Both establish that clarification VALUE is a
// selection problem, not a volume problem: training the clarifier on
// Information Gain Reward yields +3.7% task success over no-clarification
// while adding only 0.3 interaction steps (tau-Bench, five backbones), and
// structured uncertainty selection covers 7-39% more of ambiguous tasks
// while ASKING 1.5-2.7x FEWER questions. Zero-gain questions spend a user
// interruption and buy nothing.
//
// ggcode already implements the "ask when you should" side (ambiguity_point,
// input_underspec - which cites the same IGR framework - user_sentiment all
// nudge TOWARD asking). The "don't ask when you shouldn't" side had no
// deterministic gate. This gate adds three checks before ask_user executes:
//
//   (a) Self-answerable block: the question's answer is a codebase or
//       environment FACT (version numbers, file existence, signatures,
//       branch names) - the belief can be updated by read_file/grep without
//       spending a user turn. IGR = 0 by construction.
//   (b) Same-run duplicate block: an identical (fingerprinted) question was
//       already asked this run - repeating it yields zero belief update.
//   (c) Safe-default advisory: the question offers a choice labeled as the
//       safe/default option; a guidance note reminds the agent that if the
//       user does not answer, proceeding with the stated default and
//       declaring the assumption beats re-asking (complements
//       assumption-track, which catches unstated assumptions after the fact).

// askSelfAnswerable patterns match question text whose answer is a
// determinable fact. Deliberately conservative: each pattern must be
// unambiguously codebase/environment-lookupable, never a preference.
var askSelfAnswerable = []*regexp.Regexp{
	// "which/what version of X" - go.mod/package.json/CHANGELOG answer this.
	regexp.MustCompile(`(?i)\b(which|what)\s+(version|release)\b`),
	// "does (the) file X exist" / "is there a file".
	regexp.MustCompile(`(?i)\b(file|directory|folder)\s+\S+\s+(exists|exist)\b`),
	regexp.MustCompile(`(?i)\bdoes\s+the\s+\S+\s+(file|dir|directory)\b`),
	// function/type/method signature questions.
	regexp.MustCompile(`(?i)\b(signature|prototype)\s+of\b`),
	// "which branch" - git answers this.
	regexp.MustCompile(`(?i)\bwhich\s+(branch|commit|tag)\b`),
	// explicit dependency/module resolution.
	regexp.MustCompile(`(?i)\b(go\.mod|package\.json|cargo\.toml)\b.*\b(which|what|version)\b`),
}

// askSafeDefaultLabels mark a choice as the reversible/conservative option;
// when present the gate downgrades interruption urgency instead of blocking.
var askSafeDefaultLabels = []string{
	"default", "recommended", "safe", "preserve", "skip", "current", "keep",
}

// askQualityGateState is per-run: question fingerprints reset each user turn.
type askQualityGateState struct {
	asked map[uint64]struct{}
}

func newAskQualityGateState() *askQualityGateState {
	return &askQualityGateState{asked: make(map[uint64]struct{})}
}

func (s *askQualityGateState) reset() {
	s.asked = make(map[uint64]struct{})
}

// askFingerprint normalizes a question (title + prompt + sorted choice
// labels) into a stable identity for same-run dedup.
func askFingerprint(title, prompt string, choices []string) uint64 {
	norm := func(t string) string {
		return strings.Join(strings.Fields(strings.ToLower(t)), " ")
	}
	parts := []string{norm(title), norm(prompt)}
	sorted := append([]string(nil), choices...)
	for i, c := range sorted {
		sorted[i] = norm(c)
	}
	// order-insensitive choice set: reordering options is the same question.
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	parts = append(parts, strings.Join(sorted, "|"))
	h := fnv.New64a()
	h.Write([]byte(strings.Join(parts, "\x00")))
	return h.Sum64()
}

// askGateQuestion mirrors the ask_user request shape we need (subset).
type askGateQuestion struct {
	Title   string `json:"title"`
	Prompt  string `json:"prompt"`
	Kind    string `json:"kind"`
	Choices []struct {
		Label string `json:"label"`
	} `json:"choices"`
}

type askGateRequest struct {
	Title     string            `json:"title"`
	Questions []askGateQuestion `json:"questions"`
}

// checkAskQualityGate inspects an ask_user invocation BEFORE execution.
// Returns blockMsg != "" to reject the call outright (error tool result),
// or advisory != "" as a soft note to surface alongside the ask.
func (a *Agent) checkAskQualityGate(rawArgs string) (blockMsg, advisory string) {
	g := a.askQualityGate
	if g == nil {
		return "", ""
	}
	var req askGateRequest
	if err := json.Unmarshal([]byte(rawArgs), &req); err != nil || len(req.Questions) == 0 {
		return "", "" // malformed ask handled by the tool's own schema validation
	}
	if msg := g.blockSelfAnswerableQG(req); msg != "" {
		return msg, ""
	}
	if msg := g.blockSameRunDuplicateQG(req); msg != "" {
		return msg, ""
	}
	return "", g.safeDefaultAdvisoryQG(req)
}

// blockSelfAnswerableQG rejects asks whose answers are codebase facts.
// A single self-answerable question poisons the whole batch: the model
// should reformulate rather than partially interrupt the user.
func (g *askQualityGateState) blockSelfAnswerableQG(req askGateRequest) string {
	for _, q := range req.Questions {
		text := q.Title + "\n" + q.Prompt
		for _, re := range askSelfAnswerable {
			if re.MatchString(text) {
				return fmt.Sprintf(
					"[Ask Gate] The question %q is self-answerable from the codebase or environment "+
						"(version/existence/signature/branch facts are lookupable). Do not spend a user "+
						"interruption on it: resolve it yourself with read_file/grep/git and continue. "+
						"Zero-information-gain clarifications cost success rate (arXiv 2606.03135).",
					q.Title)
			}
		}
	}
	return ""
}

// blockSameRunDuplicateQG fingerprints each question and rejects re-asks.
func (g *askQualityGateState) blockSameRunDuplicateQG(req askGateRequest) string {
	for _, q := range req.Questions {
		labels := make([]string, 0, len(q.Choices))
		for _, c := range q.Choices {
			labels = append(labels, c.Label)
		}
		fp := askFingerprint(q.Title, q.Prompt, labels)
		if _, dup := g.asked[fp]; dup {
			return fmt.Sprintf(
				"[Ask Gate] Question %q (or a near-identical variant) was already asked this run. "+
					"Re-asking yields zero belief update. Proceed on the answer already given, or ask a "+
					"genuinely NEW question.",
				q.Title)
		}
		g.asked[fp] = struct{}{}
	}
	return ""
}

// safeDefaultAdvisoryQG downgrades urgency (never blocks) when a choice is
// labeled as the reversible/conservative option.
func (g *askQualityGateState) safeDefaultAdvisoryQG(req askGateRequest) string {
	for _, q := range req.Questions {
		for _, c := range q.Choices {
			label := strings.ToLower(c.Label)
			for _, marker := range askSafeDefaultLabels {
				if strings.Contains(label, marker) {
					return "[Ask Gate] This question exposes a safe default choice. If the user does not " +
						"answer (timeout/autopilot), proceed with the stated default and declare the assumption " +
						"explicitly rather than re-asking."
				}
			}
		}
	}
	return ""
}
