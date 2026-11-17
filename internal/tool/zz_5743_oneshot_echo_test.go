package tool

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
)

// #5743: cron_create must echo the computed next-fire time and warn loudly
// on same-day overshoot (target moment already passed -> +24h roll).

func Test5743NextFireEchoedInResult(t *testing.T) {
	now := time.Now()
	job := cron.Job{NextFire: now.Add(2 * time.Hour)}
	out := cronCreateSuffix(job, false, 0)
	if !strings.Contains(out, "nextFireEcho") {
		t.Fatalf("create result must echo next fire time, got: %s", out)
	}
	if strings.Contains(out, "WARNING: next fire") {
		t.Fatalf("+2h same-day fire must not warn, got: %s", out)
	}
}

func Test5743SameDayOvershootWarns(t *testing.T) {
	now := time.Now()
	later := now.Add(13 * time.Hour)
	if !sameDayBeyondTwelveHours(later, now) {
		t.Fatalf("+13h ahead must be flagged (now=%s)", now)
	}
	job := cron.Job{NextFire: later}
	out := cronCreateSuffix(job, false, 0)
	if !strings.Contains(out, "WARNING: next fire") {
		t.Fatalf("overshoot must warn, got: %s", out)
	}
	near := now.Add(2 * time.Hour)
	if sameDayBeyondTwelveHours(near, now) {
		t.Fatalf("+2h ahead must NOT be flagged (now=%s)", now)
	}
	tomorrow := now.AddDate(0, 0, 1)
	if sameDayBeyondTwelveHours(tomorrow, now) {
		t.Fatalf("next-day fire must NOT be flagged (now=%s)", now)
	}
}

func Test5743ZeroNextFireNoEcho(t *testing.T) {
	out := cronCreateSuffix(cron.Job{}, false, 0)
	if strings.Contains(out, "nextFireEcho") {
		t.Fatalf("zero NextFire must not echo, got: %s", out)
	}
}
