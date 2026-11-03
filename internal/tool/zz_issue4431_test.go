package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestIssue4431_StopCompletedJobErrors(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())
	started, err := mgr.Start(context.Background(), "true", false, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.Wait(context.Background(), started.ID, 5*time.Second, 10, 0); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if _, err := mgr.Stop(started.ID); err == nil {
		t.Fatal("stopping an already-completed job must return an error (tool-description contract)")
	} else if !strings.Contains(err.Error(), "already finished") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIssue4431_WriteToNonReadingCommandTimesOut(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises the 10s stdin write deadline")
	}
	mgr := NewCommandJobManager(t.TempDir())
	started, err := mgr.Start(context.Background(), "sleep 30", false, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop(started.ID)
	big := strings.Repeat("x", 256*1024)
	_, err = mgr.Write(started.ID, big, false)
	if err == nil {
		t.Fatal("write to a non-reading command must time out")
	}
	if !strings.Contains(err.Error(), "not reading stdin") {
		t.Fatalf("expected the explicit not-reading error, got: %v", err)
	}
}
