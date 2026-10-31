//go:build goolm

package im

import (
	"os"
	"strings"
	"testing"
)

// #4526 pin: the Telegram update loop must isolate per-update panics via
// synchronous safego.Run - a malformed update used to tear down the whole
// safego.Go("im.tg.run") wrapper including the backoff loop, leaving the
// adapter dead until process restart.
func TestIssue4526_PerUpdatePanicIsolation(t *testing.T) {
	b, err := os.ReadFile("tg_adapter.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	if !strings.Contains(s, `safego.Run("im.tg.update", func() { a.handleUpdate(ctx, u) })`) {
		t.Fatal("#4526 regression: per-update isolation missing from the poll loop")
	}
}
