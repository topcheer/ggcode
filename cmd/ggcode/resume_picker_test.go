package main

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/session"
)

func makeSession(id, workspace, title string) *session.Session {
	return &session.Session{
		ID:        id,
		Workspace: workspace,
		Title:     title,
		UpdatedAt: time.Now(),
	}
}

func TestGroupResumePickerSessions(t *testing.T) {
	sessions := []*session.Session{
		makeSession("1", "/home/user/proj", "Session 1"),
		makeSession("2", "/home/user/other", "Session 2"),
		makeSession("3", "/home/user/proj", "Session 3"),
		nil,
	}
	current, others := groupResumePickerSessions(sessions, "/home/user/proj")
	if len(current) != 2 {
		t.Errorf("expected 2 current, got %d", len(current))
	}
	if len(others) != 1 {
		t.Errorf("expected 1 other, got %d", len(others))
	}
}

func TestGroupResumePickerSessions_Empty(t *testing.T) {
	current, others := groupResumePickerSessions(nil, "/home/user/proj")
	if len(current) != 0 || len(others) != 0 {
		t.Error("expected empty for nil sessions")
	}
}

func TestGroupResumePickerSessions_NoWorkspace(t *testing.T) {
	sessions := []*session.Session{makeSession("1", "", "S1")}
	current, others := groupResumePickerSessions(sessions, "")
	if len(current) != 0 {
		t.Errorf("expected 0 current with empty workspace, got %d", len(current))
	}
	if len(others) != 1 {
		t.Errorf("expected 1 other, got %d", len(others))
	}
}

func TestNewResumePickerModel(t *testing.T) {
	current := []*session.Session{makeSession("1", "/a", "S1")}
	others := []*session.Session{makeSession("2", "/b", "S2")}
	m := newResumePickerModel(current, others, "/a")
	if m.cursorGroup != resumePickerCurrentWorkspace {
		t.Errorf("expected cursor on current, got %d", m.cursorGroup)
	}
}

func TestNewResumePickerModel_OthersOnly(t *testing.T) {
	others := []*session.Session{makeSession("2", "/b", "S2")}
	m := newResumePickerModel(nil, others, "/a")
	if m.cursorGroup != resumePickerOtherWorkspaces {
		t.Errorf("expected cursor on others, got %d", m.cursorGroup)
	}
}

func TestResumePickerMove(t *testing.T) {
	sessions := make([]*session.Session, 3)
	for i := range sessions {
		sessions[i] = makeSession("s"+string(rune('0'+i)), "/a", "S"+string(rune('0'+i)))
	}
	m := newResumePickerModel(sessions, nil, "/a")
	if m.cursorIndex != 0 {
		t.Errorf("expected initial cursor at 0, got %d", m.cursorIndex)
	}
	m.move(1)
	if m.cursorIndex != 1 {
		t.Errorf("expected cursor at 1 after move(1), got %d", m.cursorIndex)
	}
	m.move(-1)
	if m.cursorIndex != 0 {
		t.Errorf("expected cursor at 0 after move(-1), got %d", m.cursorIndex)
	}
	// Wrap around
	m.move(-1)
	if m.cursorIndex != 2 {
		t.Errorf("expected cursor at 2 after wrap, got %d", m.cursorIndex)
	}
}

func TestResumePickerMove_Empty(t *testing.T) {
	m := resumePickerModel{}
	m.move(1) // should not panic
}

func TestResumePickerVisibleItems(t *testing.T) {
	current := []*session.Session{makeSession("1", "/a", "S1")}
	others := []*session.Session{makeSession("2", "/b", "S2")}
	m := newResumePickerModel(current, others, "/a")
	items := m.visibleItems()
	if len(items) != 2 {
		t.Errorf("expected 2 visible items, got %d", len(items))
	}
}

func TestResumePickerSelectedItem(t *testing.T) {
	sessions := []*session.Session{makeSession("1", "/a", "S1")}
	m := newResumePickerModel(sessions, nil, "/a")
	item := m.selectedItem()
	if item == nil {
		t.Fatal("expected non-nil selected item")
	}
	if item.session.ID != "1" {
		t.Errorf("expected ID '1', got %q", item.session.ID)
	}
}

func TestResumePickerSelectedItem_Empty(t *testing.T) {
	m := resumePickerModel{}
	item := m.selectedItem()
	if item != nil {
		t.Error("expected nil for empty model")
	}
}

func TestResumePickerPageInfo(t *testing.T) {
	sessions := make([]*session.Session, 7) // 2 pages
	for i := range sessions {
		sessions[i] = makeSession("s"+string(rune('0'+i)), "/a", "")
	}
	m := newResumePickerModel(sessions, nil, "/a")
	page, total := m.pageInfo(resumePickerCurrentWorkspace)
	if page != 0 {
		t.Errorf("expected page 0, got %d", page)
	}
	if total != 2 {
		t.Errorf("expected 2 total pages, got %d", total)
	}
}

func TestResumePickerPageBounds(t *testing.T) {
	sessions := make([]*session.Session, 7)
	for i := range sessions {
		sessions[i] = makeSession("s"+string(rune('0'+i)), "/a", "")
	}
	m := newResumePickerModel(sessions, nil, "/a")
	start, end, total := m.pageBounds(resumePickerCurrentWorkspace)
	if start != 0 {
		t.Errorf("expected start 0, got %d", start)
	}
	if end != 5 {
		t.Errorf("expected end 5, got %d", end)
	}
	if total != 7 {
		t.Errorf("expected total 7, got %d", total)
	}
}

func TestResumePickerGroupSessions(t *testing.T) {
	current := []*session.Session{makeSession("1", "/a", "S1")}
	others := []*session.Session{makeSession("2", "/b", "S2")}
	m := newResumePickerModel(current, others, "/a")
	if len(m.groupSessions(resumePickerCurrentWorkspace)) != 1 {
		t.Error("expected 1 current session")
	}
	if len(m.groupSessions(resumePickerOtherWorkspaces)) != 1 {
		t.Error("expected 1 other session")
	}
}

func TestClampResumePage(t *testing.T) {
	tests := []struct {
		page, total, want int
	}{
		{0, 3, 0},
		{2, 3, 2},
		{3, 3, 2},
		{-1, 3, 0},
		{0, 0, 0},
		{5, 3, 2},
	}
	for _, tt := range tests {
		got := clampResumePage(tt.page, tt.total)
		if got != tt.want {
			t.Errorf("clampResumePage(%d, %d) = %d, want %d", tt.page, tt.total, got, tt.want)
		}
	}
}

func TestFormatResumePickerSession(t *testing.T) {
	ses := &session.Session{ID: "abc123", Title: "My Session", UpdatedAt: time.Now()}
	got := formatResumePickerSession(ses, "")
	if got == "" {
		t.Error("expected non-empty")
	}
	if !contains(got, "My Session") {
		t.Errorf("expected title in output: %s", got)
	}
}

func TestFormatResumePickerSession_Nil(t *testing.T) {
	got := formatResumePickerSession(nil, "")
	if got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
}

func TestFormatResumePickerSession_NoTitle(t *testing.T) {
	ses := &session.Session{ID: "abc", Title: ""}
	got := formatResumePickerSession(ses, "")
	if !contains(got, "Untitled session") {
		t.Errorf("expected 'Untitled session', got %s", got)
	}
}

func TestCompactWorkspaceLabel(t *testing.T) {
	got := compactWorkspaceLabel("/home/user/myproject")
	if got == "" {
		t.Error("expected non-empty")
	}
	got = compactWorkspaceLabel("")
	if got != "" {
		t.Errorf("expected empty for empty path, got %q", got)
	}
}

func TestResumePickerView(t *testing.T) {
	current := []*session.Session{makeSession("1", "/a", "S1")}
	m := newResumePickerModel(current, nil, "/a")
	v := m.View()
	if v.Content == "" {
		t.Error("expected non-empty view")
	}
}

func TestResumePickerInit(t *testing.T) {
	m := resumePickerModel{}
	cmd := m.Init()
	if cmd != nil {
		t.Error("expected nil Init cmd")
	}
}

func TestResumePickerTurnPage(t *testing.T) {
	sessions := make([]*session.Session, 12)
	for i := range sessions {
		sessions[i] = makeSession("s"+string(rune('0'+i)), "/a", "")
	}
	m := newResumePickerModel(sessions, nil, "/a")
	m.turnPage(1) // go to page 2
	if m.currentPage != 1 {
		t.Errorf("expected page 1, got %d", m.currentPage)
	}
	m.turnPage(-1) // back to page 1
	if m.currentPage != 0 {
		t.Errorf("expected page 0, got %d", m.currentPage)
	}
}

func TestResumePickerTurnPage_SinglePage(t *testing.T) {
	sessions := []*session.Session{makeSession("1", "/a", "S1")}
	m := newResumePickerModel(sessions, nil, "/a")
	m.turnPage(1) // single page, should not change
	if m.currentPage != 0 {
		t.Errorf("expected page 0, got %d", m.currentPage)
	}
}

func TestResumePickerGroupPageRange(t *testing.T) {
	sessions := make([]*session.Session, 7)
	for i := range sessions {
		sessions[i] = makeSession("s"+string(rune('0'+i)), "/a", "")
	}
	m := newResumePickerModel(sessions, nil, "/a")
	start, end := m.groupPageRange(resumePickerCurrentWorkspace)
	if start != 0 || end != 5 {
		t.Errorf("expected (0,5), got (%d,%d)", start, end)
	}
	m.currentPage = 1
	start, end = m.groupPageRange(resumePickerCurrentWorkspace)
	if start != 5 || end != 7 {
		t.Errorf("expected (5,7), got (%d,%d)", start, end)
	}
}

func TestResumePickerGroupPageRange_Empty(t *testing.T) {
	m := resumePickerModel{}
	start, end := m.groupPageRange(resumePickerCurrentWorkspace)
	if start != 0 || end != 0 {
		t.Errorf("expected (0,0) for empty, got (%d,%d)", start, end)
	}
}

func TestResumePickerTotalPages(t *testing.T) {
	sessions := make([]*session.Session, 7)
	for i := range sessions {
		sessions[i] = makeSession("s"+string(rune('0'+i)), "/a", "")
	}
	m := newResumePickerModel(sessions, nil, "/a")
	if m.totalPages(resumePickerCurrentWorkspace) != 2 {
		t.Error("expected 2 pages for 7 items with pageSize=5")
	}
	m2 := resumePickerModel{}
	if m2.totalPages(resumePickerCurrentWorkspace) != 0 {
		t.Error("expected 0 pages for empty")
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
