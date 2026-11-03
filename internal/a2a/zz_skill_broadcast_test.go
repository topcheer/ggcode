package a2a

import (
	"encoding/json"
	"strings"
	"testing"
)

func newSkillBroadcastTestServer(t *testing.T) *Server {
	t.Helper()
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0}, handler)
	t.Cleanup(func() { srv.Stop() })
	return srv
}

func cardSkills(t *testing.T, srv *Server) []Skill {
	t.Helper()
	srv.cardMu.RLock()
	defer srv.cardMu.RUnlock()
	out := make([]Skill, len(srv.card.Skills))
	copy(out, srv.card.Skills)
	return out
}

func TestSetSkills_ReplacesCardWithLocalSkills(t *testing.T) {
	srv := newSkillBroadcastTestServer(t)

	local := append(DefaultSkills(), Skill{
		ID:          "skill:deploy-to-vercel",
		Name:        "Deploy to Vercel",
		Description: "Ship the current project to Vercel",
		Tags:        []string{"skill", "project"},
		Source:      "project",
	})
	srv.SetSkills(local)

	got := cardSkills(t, srv)
	if len(got) != len(local) {
		t.Fatalf("card skills = %d, want %d (baseline + local)", len(got), len(local))
	}
	last := got[len(got)-1]
	if last.ID != "skill:deploy-to-vercel" || last.Source != "project" {
		t.Fatalf("injected skill = %+v, want id skill:deploy-to-vercel source project", last)
	}

	b, err := json.Marshal(last)
	if err != nil {
		t.Fatalf("marshal skill: %v", err)
	}
	if !strings.Contains(string(b), `"source":"project"`) {
		t.Fatalf("skill JSON missing source field: %s", b)
	}
}

func TestSetSkills_EmptyKeepsDefaultBaseline(t *testing.T) {
	srv := newSkillBroadcastTestServer(t)

	before := len(cardSkills(t, srv))
	if before == 0 {
		t.Fatal("server card has no baseline skills before SetSkills")
	}
	srv.SetSkills(nil)

	after := cardSkills(t, srv)
	if len(after) != before {
		t.Fatalf("empty SetSkills changed card: %d -> %d skills", before, len(after))
	}
}
