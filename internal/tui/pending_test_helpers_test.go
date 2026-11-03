package tui

func (q *pendingQueue) enqueue(text string) int {
	return q.enqueueWithImages(text, nil)
}
