package usage

// #4141: MinimaxProbe must show the exhausted state, not hide it.
// Old gates (`remaining > 0` on the 5h window; `status==1 && remaining > 0`
// on weekly) dropped the window entirely when the quota hit exactly 0% -
// the panel went blank exactly when the user was throttled and checking
// usage most. Existence signals (EndTimeMs / weekly status) must decide
// whether a window is rendered; remaining==0 then renders as 100% used.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const minimaxRemainsPath = "/v1/api/openplatform/coding_plan/remains"

func minimaxServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != minimaxRemainsPath {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(body))
	}))
}

// Exhausted 5h tier (remaining exactly 0, end time present) must still
// render as a 100%-used window with its reset time - not be omitted.
func TestMinimaxExhausted5hStillShown(t *testing.T) {
	srv := minimaxServer(t, `{"model_remains":[{"model_name":"general","current_interval_remaining_percent":0,"end_time":1760000000000}]}`)
	defer srv.Close()
	info, err := MinimaxProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Windows) != 1 {
		t.Fatalf("exhausted 5h window must still be shown, got %+v", info.Windows)
	}
	w := info.Windows[0]
	if w.Label != "5h" || w.UsedPercent != 100 {
		t.Fatalf("want 5h 100%% used, got %+v", w)
	}
	if !w.ResetsAt.Equal(time.UnixMilli(1760000000000)) {
		t.Fatalf("reset time = %v, want %v", w.ResetsAt, time.UnixMilli(1760000000000))
	}
}

// Exhausted weekly tier (status active, remaining exactly 0) must render
// as 100% used - the status field alone proves the tier exists.
func TestMinimaxExhaustedWeeklyStillShown(t *testing.T) {
	srv := minimaxServer(t, `{"model_remains":[{"model_name":"general","current_interval_remaining_percent":40,"end_time":1760000000000,"current_weekly_status":1,"current_weekly_remaining_percent":0,"weekly_end_time":1760100000000}]}`)
	defer srv.Close()
	info, err := MinimaxProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Windows) != 2 {
		t.Fatalf("want 5h + weekly windows, got %+v", info.Windows)
	}
	if info.Windows[1].Label != "weekly" || info.Windows[1].UsedPercent != 100 {
		t.Fatalf("exhausted weekly must show 100%% used, got %+v", info.Windows[1])
	}
	if !info.Windows[1].ResetsAt.Equal(time.UnixMilli(1760100000000)) {
		t.Fatalf("weekly reset = %v", info.Windows[1].ResetsAt)
	}
}

// A truly absent 5h tier (remaining 0 AND no end time - zero-valued JSON
// fields or a non-general-only payload) stays hidden: existence gate must
// not regress into showing empty windows for missing data.
func TestMinimaxAbsent5hStillHidden(t *testing.T) {
	srv := minimaxServer(t, `{"model_remains":[{"model_name":"general","current_interval_remaining_percent":0,"end_time":0,"current_weekly_status":0}]}`)
	defer srv.Close()
	info, err := MinimaxProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Windows) != 0 {
		t.Fatalf("absent tiers (no end time, weekly inactive) must stay hidden, got %+v", info.Windows)
	}
}

// Healthy state must not regress: partial remaining renders as before,
// inactive weekly tier stays hidden.
func TestMinimaxHealthyUnchanged(t *testing.T) {
	srv := minimaxServer(t, `{"model_remains":[{"model_name":"general","current_interval_remaining_percent":62.5,"end_time":1760000000000,"current_weekly_status":0,"current_weekly_remaining_percent":80}]}`)
	defer srv.Close()
	info, err := MinimaxProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Windows) != 1 {
		t.Fatalf("want only the 5h window (weekly status 0), got %+v", info.Windows)
	}
	w := info.Windows[0]
	if w.Label != "5h" || w.UsedPercent != 37.5 {
		t.Fatalf("want 5h 37.5%% used, got %+v", w)
	}
}

// Non-general meters (video etc.) are skipped entirely - unchanged.
func TestMinimaxNonGeneralSkipped(t *testing.T) {
	srv := minimaxServer(t, `{"model_remains":[{"model_name":"video","current_interval_remaining_percent":10,"end_time":1760000000000}]}`)
	defer srv.Close()
	info, err := MinimaxProbe{}.Fetch(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Windows) != 0 {
		t.Fatalf("non-general meters must be skipped, got %+v", info.Windows)
	}
}
