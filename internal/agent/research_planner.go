package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/provider"
)

// Research planner (r343 deep-research second component; sa-130). Deep-
// research production systems (Perplexity Sonar Deep Research, Open Deep
// Research, DRA roadmap arXiv:2506.18096; STORM arXiv:2402.14207 for the
// perspective pattern) all front-load a PLANNER that decomposes the goal
// into perspective-scoped sub-queries before retrieval starts, instead of
// letting the agent fire one monolithic search.
//
// Like the r365 gate, this is deterministic and zero-LLM-cost: it derives a
// sub-query plan from the goal text via keyword-based perspective detection
// (STORM's multi-perspective questioning, reduced to a deterministic
// taxonomy) plus fixed query templates. It fires ONCE per run, at the same
// early point where plannerAnalyze runs, and only when the run is in
// research mode (same detectResearchMode signal the overseer uses).

// researchPerspective is one axis of STORM-style multi-perspective
// questioning. Keywords are matched case-insensitively against the goal;
// Chinese and English surfaces are both covered.
type researchPerspective struct {
	name     string
	keywords []string
	// templates are query shapes expanded with the goal text.
	templates []string
	// priority orders perspectives when maxQueries forces a cut.
	priority int
}

// researchPerspectives is the deterministic perspective taxonomy. Order of
// the slice is the fallback order; priority drives the final ordering.
var researchPerspectives = []researchPerspective{
	{
		name:      "technical",
		keywords:  []string{"how", "implement", "architecture", "design", "原理", "实现", "架构", "设计"},
		templates: []string{"%s technical details and mechanisms", "%s implementation approaches"},
		priority:  1,
	},
	{
		name:      "comparison",
		keywords:  []string{"compare", "versus", " vs", "alternative", "对比", "比较", "替代", "选型"},
		templates: []string{"%s alternatives and trade-offs", "%s benchmark or comparison data"},
		priority:  2,
	},
	{
		name:      "practical",
		keywords:  []string{"use", "practice", "example", "tutorial", "使用", "实践", "示例", "教程", "怎么用"},
		templates: []string{"%s real-world usage and examples", "%s common pitfalls and failure modes"},
		priority:  3,
	},
	{
		name:      "risk",
		keywords:  []string{"risk", "security", "problem", "limitation", "风险", "安全", "问题", "局限"},
		templates: []string{"%s known risks and limitations", "%s security or reliability concerns"},
		priority:  4,
	},
}

// ResearchSubQuery is one planned sub-query with the perspective it serves.
type ResearchSubQuery struct {
	Perspective string
	Query       string
	Priority    int
}

// defaultResearchSubQueryCap bounds the plan: deep-research systems that
// fire dozens of sub-searches burn latency and context; the gate only needs
// multi-hop coverage, and 6 sub-queries x 4 perspectives headroom suffices.
const defaultResearchSubQueryCap = 6

// PlanResearchSubQueries decomposes a research goal into perspective-scoped
// sub-queries. Pure function, deterministic, zero I/O. maxQueries <= 0
// falls back to the default cap. An empty/unmatched goal still yields the
// generic set (the goal text itself as one query) so research-mode runs
// always get a usable plan.
func PlanResearchSubQueries(goal string, maxQueries int) []ResearchSubQuery {
	if maxQueries <= 0 {
		maxQueries = defaultResearchSubQueryCap
	}
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return nil
	}
	lower := " " + strings.ToLower(goal) + " "

	matched := false
	var subs []ResearchSubQuery
	for _, p := range researchPerspectives {
		hit := false
		for _, kw := range p.keywords {
			if strings.Contains(lower, kw) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		matched = true
		for _, tpl := range p.templates {
			subs = append(subs, ResearchSubQuery{
				Perspective: p.name,
				Query:       fmt.Sprintf(tpl, goal),
				Priority:    p.priority,
			})
		}
	}
	if !matched {
		// No perspective keyword: the bare goal is still a valid seed query.
		subs = append(subs, ResearchSubQuery{
			Perspective: "general",
			Query:       goal,
			Priority:    5,
		})
	}
	if len(subs) > maxQueries {
		subs = subs[:maxQueries]
	}
	return subs
}

// researchPlanText renders the injection message for a planned run.
func researchPlanText(subs []ResearchSubQuery) string {
	if len(subs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Research Plan] Before searching, decompose this research goal into the following sub-queries ")
	b.WriteString("(perspective-scoped, per deep-research planner practice) and search them individually:\n")
	for i, s := range subs {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, s.Perspective, s.Query)
	}
	b.WriteString("Track which sub-queries are answered; search the gaps rather than re-searching covered ground. ")
	b.WriteString("(This plan is advisory; adapt it if the evidence redirects you.)")
	return b.String()
}

// researchPlanState guards the fire-once semantics of plan injection.
type researchPlanState struct {
	mu       sync.Mutex
	injected bool
}

func (p *researchPlanState) shouldInject() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.injected {
		return false
	}
	p.injected = true
	return true
}

// maybeInjectResearchPlan fires the ONE-TIME research sub-query plan for
// research-mode runs (same detectResearchMode signal the overseer uses).
// The plan text is injected as a user message at the same early point the
// agent-side planner runs, and the planned sub-queries are saved so the
// r365 gate can compute uncovered gaps at synthesis time.
func (a *Agent) maybeInjectResearchPlan(content string) {
	if !detectResearchMode(content) {
		return
	}
	if !a.researchPlan.shouldInject() {
		return
	}
	subs := PlanResearchSubQueries(content, defaultResearchSubQueryCap)
	msg := researchPlanText(subs)
	if msg == "" {
		return
	}
	a.researchPlanSubs = subs
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: msg,
		}},
	})
}
