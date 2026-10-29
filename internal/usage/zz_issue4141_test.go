package usage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// #4141: an EXHAUSTED window (remaining_percent = 0 with a valid end_time)
// is the moment the user most needs the usage signal, yet the old
// `remaining > 0` gate dropped it entirely. The gate must key on window
// existence (end_time), not remaining budget. Structurally absent windows
// (all-zero decode) stay hidden.
func TestIssue4141_ExhaustedWindowsStillShown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"model_remains":[{"model_name":"general","current_interval_remaining_percent":0,"end_time":1789831644997,"current_weekly_status":1,"current_weekly_remaining_percent":0,"weekly_end_time":1789738694871}]}`))
	}))
	defer srv.Close()
	info, err := MinimaxProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Windows) != 2 {
		t.Fatalf("exhausted windows dropped: %+v", info.Windows)
	}
	for i, want := range []float64{100, 100} {
		if info.Windows[i].UsedPercent != want {
			t.Fatalf("window[%d] used = %v, want %v (100%% used on exhaustion)", i, info.Windows[i].UsedPercent, want)
		}
	}
	if info.Windows[0].ResetsAt.IsZero() || info.Windows[1].ResetsAt.IsZero() {
		t.Fatalf("reset timestamps lost: %+v", info.Windows)
	}
}

// Structural absence stays hidden: a "general" row decoded from JSON with
// no quota fields at all must not fabricate windows.
func TestIssue4141_AbsentWindowsStillHidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"model_remains":[{"model_name":"general"}]}`))
	}))
	defer srv.Close()
	info, err := MinimaxProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Windows) != 0 {
		t.Fatalf("absent windows fabricated: %+v", info.Windows)
	}
}
