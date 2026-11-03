//go:build darwin

package tool

import (
	"context"
	"testing"
)

func TestIterm2DroppedControlCounted1691(t *testing.T) {
	if got := countDroppedControlRunes("ok\x1b[Ax\x07"); got != 2 {
		t.Fatalf("dropped control count = %d, want 2", got)
	}
	if got := countDroppedControlRunes("plain \t tab \n nl \r ok"); got != 0 {
		t.Fatalf("tab/nl/cr must not count, got %d", got)
	}
}

func TestIMWaitForHealthyCtxCancel1691(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var it IMTool
	if it.waitForHealthy(ctx, "tg", 0) {
		t.Fatal("cancelled ctx must abort immediately")
	}
}
