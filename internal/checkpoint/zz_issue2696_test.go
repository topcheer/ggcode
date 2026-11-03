package checkpoint

import "testing"

func TestIssue2696_ClearResetsFileExisted(t *testing.T) {
	m := NewManager(50)
	m.SaveWithExistence("/x", "", "content", "write_file", false)

	if mf := m.ModifiedFiles(); len(mf) != 1 || !mf[0].IsNew {
		t.Fatalf("session A: expected /x IsNew=true, got %+v", mf)
	}

	m.Clear()

	m.SaveWithExistence("/x", "content", "content2", "edit_file", true)

	if mf := m.ModifiedFiles(); len(mf) != 1 {
		t.Fatalf("expected 1 modified file, got %+v", mf)
	} else if mf[0].IsNew {
		t.Fatal("post-Clear edit of a pre-existing file reported IsNew=true: stale fileExisted leaked across sessions (#2696)")
	}

	m2 := NewManager(50)
	m2.SaveWithExistence("/y", "old", "new", "edit_file", true)
	m2.Clear()
	m2.SaveWithExistence("/y", "", "created", "write_file", false)
	if mf := m2.ModifiedFiles(); len(mf) != 1 || !mf[0].IsNew {
		t.Fatalf("fresh creation after Clear must be IsNew=true, got %+v", mf)
	}
}

func TestIssue2696_ClearDropsMapEntries(t *testing.T) {
	m := NewManager(50)
	m.SaveWithExistence("/a", "", "c", "write_file", false)
	m.SaveWithExistence("/b", "", "c", "write_file", false)
	m.Clear()
	m.mu.Lock()
	leaked := len(m.fileExisted)
	m.mu.Unlock()
	if leaked != 0 {
		t.Fatalf("fileExisted retains %d entries after Clear; unbounded cross-session growth (#2696)", leaked)
	}
}
