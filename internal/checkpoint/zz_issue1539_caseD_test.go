package checkpoint

import "testing"

// TestModifiedFilesIsNewSurvivesEviction pins #1539 case D: a file's IsNew
// answer must survive FIFO eviction of the checkpoint that recorded its
// creation. The manager remembers each file's first-recorded Existed value,
// so the first surviving (mid-life) checkpoint no longer misreports an
// agent-created file as pre-existing.
func TestModifiedFilesIsNewSurvivesEviction(t *testing.T) {
	m := NewManager(3)
	m.StartRun("r1")
	m.SaveWithExistence("created.txt", "", "v1", "write_file", false)
	m.SaveWithExistence("created.txt", "v1", "v2", "edit_file", true)
	m.SaveWithExistence("created.txt", "v2", "v3", "edit_file", true)

	// maxCheckpoints=3: this r2 entry evicts the first non-active-run
	// checkpoint — created.txt's creation record (r1).
	m.StartRun("r2")
	m.Save("other.txt", "x", "y", "edit_file")

	files := m.ModifiedFiles()
	var created *FileSummary
	for i := range files {
		if files[i].Path == "created.txt" {
			created = &files[i]
		}
	}
	if created == nil {
		t.Fatal("created.txt should still appear via its surviving checkpoints")
	}
	if !created.IsNew {
		t.Errorf("IsNew must survive eviction of the creation checkpoint (#1539 case D): got false for created.txt")
	}
}

// Control: the same shape without eviction keeps reporting IsNew correctly
// for both created and pre-existing files.
func TestModifiedFilesIsNewWithoutEviction(t *testing.T) {
	m := NewManager(50)
	m.StartRun("r1")
	m.SaveWithExistence("created.txt", "", "v1", "write_file", false)
	m.Save("preexisting.txt", "old", "new", "edit_file")

	files := m.ModifiedFiles()
	got := map[string]bool{}
	for _, fs := range files {
		got[fs.Path] = fs.IsNew
	}
	if !got["created.txt"] {
		t.Errorf("created.txt should report IsNew=true")
	}
	if got["preexisting.txt"] {
		t.Errorf("preexisting.txt should report IsNew=false")
	}
}
