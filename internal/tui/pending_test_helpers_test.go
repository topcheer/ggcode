package tui

// Test-only convenience method. The committed coverage tests enqueue plain text
// messages via a single-argument enqueue; the production API exposes
// enqueueWithImages/enqueueHidden. This thin wrapper bridges the two so the
// tests compile against the current API without polluting production callers.
func (q *pendingQueue) enqueue(text string) int {
	return q.enqueueWithImages(text, nil)
}
