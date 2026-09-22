package lanchat

import "testing"

func TestNormalizeTeamName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Volumes/new/ggai/ggcode", "ggcode"},
		{"ggcode", "ggcode"},
		{"/home/user/my project", "my-project"},
		{"C:\\work\\My Repo", "my-repo"}, // windows path + space
		{"a@b/c@d", "c-d"},               // '@' forbidden (composite nick split)
		{"---", "dev-team"},              // nothing usable
		{"", "dev-team"},
		{"/x/", "dev-team"},                   // trailing slash -> empty element
		{"/x/GGCODE", "ggcode"},               // lowercase for stable team matching
		{"日本語プロジェクト", "dev-team"},             // all-mapped -> empty -> fallback
		{"/w/two--dash--run", "two-dash-run"}, // collapse consecutive '-'
	}
	for _, tc := range cases {
		if got := NormalizeTeamName(tc.in); got != tc.want {
			t.Errorf("NormalizeTeamName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeTeamNameLengthCap(t *testing.T) {
	long := "a"
	for i := 0; i < 60; i++ {
		long += "b"
	}
	got := NormalizeTeamName(long)
	if len(got) > 32 {
		t.Fatalf("capped length = %d, want <= 32", len(got))
	}
	if got != long[:32] {
		t.Fatalf("cap should preserve prefix, got %q", got)
	}
}

// New sessions on the same workspace must land on the same team so
// team-scoped broadcast reaches them.
func TestNewHubTeamFromWorkspace(t *testing.T) {
	store := NewStore(t.TempDir())
	hub := NewHub("node-ws", "tui", "http://localhost:1", "", store, WorkspaceMeta{Workspace: "/volumes/new/My Project!"})
	if hub.Team() != "my-project" {
		t.Fatalf("hub team = %q, want %q", hub.Team(), "my-project")
	}
	// Empty workspace falls back to the historic default.
	store2 := NewStore(t.TempDir())
	hub2 := NewHub("node-def", "tui", "http://localhost:1", "", store2, WorkspaceMeta{})
	if hub2.Team() != DefaultTeam {
		t.Fatalf("empty workspace team = %q, want %q", hub2.Team(), DefaultTeam)
	}
}
