package a2a

import (
	"context"
	"testing"
	"time"
)

func TestHandleCancelEntryInstalledBeforePublish(t *testing.T) {
	const iterations = 50
	for i := 0; i < iterations; i++ {
		h := NewTaskHandler(t.TempDir(), nil, nil,
			WithTimeout(10*time.Second), WithMaxTasks(8))

		go func() {
			_, _ = h.Handle(context.Background(), SkillFileSearch,
				Message{Parts: []Part{{Kind: "text", Text: "x"}}}, "")
		}()

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			h.mu.Lock()
			var (
				id      string
				hasEntr bool
			)
			for tid, tsk := range h.tasks {
				if tsk.Status.IsTerminal() {
					continue
				}
				id = tid
				_, hasEntr = h.cancels[tid]
			}
			h.mu.Unlock()

			if id == "" {
				time.Sleep(200 * time.Microsecond)
				continue
			}
			if !hasEntr {
				t.Fatalf("iteration %d: non-terminal task %s visible without cancel entry (publish-before-install race regressed)", i, id)
			}
			break
		}
	}
}
