package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/provider"
)

type researchPerspective struct {
	name      string
	keywords  []string
	templates []string
	priority  int
}

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

type ResearchSubQuery struct {
	Perspective string
	Query       string
	// Aspect is the perspective-specific ask WITHOUT the goal text, so the
	// injected plan never re-embeds the (possibly large) task payload once
	// per sub-query (#5388). Renderers fall back to Query when empty.
	Aspect   string
	Priority int
}

const defaultResearchSubQueryCap = 6

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
				Aspect:      strings.TrimSpace(strings.TrimPrefix(tpl, "%s")),
				Priority:    p.priority,
			})
		}
	}
	if !matched {

		subs = append(subs, ResearchSubQuery{
			Perspective: "general",
			Query:       goal,
			Aspect:      "the task as stated above",
			Priority:    5,
		})
	}
	if len(subs) > maxQueries {
		subs = subs[:maxQueries]
	}
	return subs
}

func researchPlanText(subs []ResearchSubQuery) string {
	if len(subs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Research Plan] The task above is the research goal; decompose it into the following perspective-scoped sub-queries and search them individually:\n")
	for i, s := range subs {
		label := s.Aspect
		if label == "" {
			// Backward compatibility: hand-built sub-queries (tests, callers)
			// without Aspect keep rendering their full Query text.
			label = s.Query
		}
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, s.Perspective, label)
	}
	b.WriteString("Track which sub-queries are answered; search the gaps rather than re-searching covered ground. ")
	b.WriteString("(This plan is advisory; adapt it if the evidence redirects you.)")
	return b.String()
}

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

func (p *researchPlanState) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.injected = false
}

func perspectiveKeywords(name string) []string {
	for _, p := range researchPerspectives {
		if p.name == name {
			return p.keywords
		}
	}
	return nil
}

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
