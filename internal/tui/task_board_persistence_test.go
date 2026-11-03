package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/task"
)

func TestTaskBoardHelpersRoundTrip(t *testing.T) {
	m := &Model{taskMgr: task.NewManager()}
	created := m.taskMgr.Create("step one", "detail", "doing", nil)

	ses := &session.Session{}
	m.snapshotTasksInto(ses)
	if len(ses.TasksJSON) == 0 {
		t.Fatal("snapshotTasksInto did not populate session.TasksJSON")
	}

	if len(ses.TasksEnvJSON) == 0 {
		t.Fatal("snapshotTasksInto did not populate session.TasksEnvJSON")
	}
	if fp := decodeEnvFingerprint(ses.TasksEnvJSON); fp == nil || fp.Head == "" {
		t.Errorf("TasksEnvJSON not a decodable fingerprint: %s", ses.TasksEnvJSON)
	}

	m2 := &Model{taskMgr: task.NewManager()}
	m2.restoreTasksFromSession(ses)
	got := m2.taskMgr.List()
	if len(got) != 1 || got[0].ID != created.ID || got[0].Subject != "step one" {
		t.Errorf("board not restored faithfully: %+v", got)
	}
}

func TestTaskBoardHelpersEmptyAndNilSafety(t *testing.T) {
	m := &Model{taskMgr: task.NewManager()}
	m.taskMgr.Create("t", "", "", nil)
	m.restoreTasksFromSession(&session.Session{})
	if got := m.taskMgr.List(); len(got) != 0 {
		t.Errorf("expected board reset on empty session, got %d tasks", len(got))
	}

	nilMgr := &Model{}
	nilMgr.snapshotTasksInto(&session.Session{})
	nilMgr.restoreTasksFromSession(&session.Session{})
	nilSes := &Model{taskMgr: task.NewManager()}
	nilSes.snapshotTasksInto(nil)
	nilSes.restoreTasksFromSession(nil)
}
