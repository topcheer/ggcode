package usage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
