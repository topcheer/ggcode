package agent

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
)

var askSelfAnswerable = []*regexp.Regexp{

	regexp.MustCompile(`(?i)\b(file|directory|folder)\s+\S+\s+(exists|exist)\b`),

	regexp.MustCompile(`(?i)\bdoes\s+the\s+\S+\s+(file|dir|directory)\s+(exist|exists)\b`),

	regexp.MustCompile(`(?i)\b(signature|prototype)\s+of\b`),

	regexp.MustCompile(`(?i)\bwhich\s+(branch|commit|tag)\b`),

	regexp.MustCompile(`(?i)\b(go\.mod|package\.json|cargo\.toml)\b.*\b(which|what|version)\b`),
}

var askSafeDefaultLabels = []string{
	"default", "recommended", "safe", "preserve", "skip", "current", "keep",
}

type askQualityGateState struct {
	asked map[uint64]struct{}
}

func newAskQualityGateState() *askQualityGateState {
	return &askQualityGateState{asked: make(map[uint64]struct{})}
}

func (s *askQualityGateState) reset() {
	s.asked = make(map[uint64]struct{})
}

func askFingerprint(title, prompt string, choices []string) uint64 {
	norm := func(t string) string {
		return strings.Join(strings.Fields(strings.ToLower(t)), " ")
	}
	parts := []string{norm(title), norm(prompt)}
	sorted := append([]string(nil), choices...)
	for i, c := range sorted {
		sorted[i] = norm(c)
	}

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

func (a *Agent) checkAskQualityGate(rawArgs string) (blockMsg, advisory string) {
	g := a.askQualityGate
	if g == nil {
		return "", ""
	}
	var req askGateRequest
	if err := json.Unmarshal([]byte(rawArgs), &req); err != nil || len(req.Questions) == 0 {
		return "", ""
	}
	if msg := g.blockSelfAnswerableQG(req); msg != "" {
		return msg, ""
	}
	if msg := g.blockSameRunDuplicateQG(req); msg != "" {
		return msg, ""
	}
	return "", g.safeDefaultAdvisoryQG(req)
}

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
					"Re-asking yields zero belief update. Ask a genuinely NEW question "+
					"(the user has already answered this one).",
				q.Title)
		}
	}
	return ""
}

func (a *Agent) markAskAskedQG(rawArgs string) {
	g := a.askQualityGate
	if g == nil {
		return
	}
	var req askGateRequest
	if err := json.Unmarshal([]byte(rawArgs), &req); err != nil || len(req.Questions) == 0 {
		return
	}
	for _, q := range req.Questions {
		labels := make([]string, 0, len(q.Choices))
		for _, c := range q.Choices {
			labels = append(labels, c.Label)
		}
		g.asked[askFingerprint(q.Title, q.Prompt, labels)] = struct{}{}
	}
}

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
