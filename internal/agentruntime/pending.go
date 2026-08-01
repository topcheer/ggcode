package agentruntime

import "sync"

type PendingQueue[T any] struct {
	mu    sync.Mutex
	items []PendingMessage[T]
}

func NewPendingQueue[T any]() *PendingQueue[T] {
	return &PendingQueue[T]{}
}

func (q *PendingQueue[T]) Enqueue(text string, hidden bool, meta T) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, PendingMessage[T]{
		Text:   text,
		Hidden: hidden,
		Meta:   meta,
	})
	return len(q.items)
}

func (q *PendingQueue[T]) Count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *PendingQueue[T]) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = nil
}

func (q *PendingQueue[T]) SnapshotTexts() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}
	out := make([]string, len(q.items))
	for i, item := range q.items {
		out[i] = item.Text
	}
	return out
}

func (q *PendingQueue[T]) Snapshot() []PendingMessage[T] {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}
	return append([]PendingMessage[T](nil), q.items...)
}

func (q *PendingQueue[T]) Consume() (PendingMessage[T], bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		var zero PendingMessage[T]
		return zero, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item, true
}

// PopLastVisible removes and returns the last non-hidden, non-meta item from the queue.
// Returns false if no visible item exists. Used for the Up-arrow de-queue feature:
// when the agent is busy, the user can press Up to pull their last queued message
// back into the input box for editing.
func (q *PendingQueue[T]) PopLastVisible() (PendingMessage[T], bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := len(q.items) - 1; i >= 0; i-- {
		if !q.items[i].Hidden {
			item := q.items[i]
			q.items = append(q.items[:i], q.items[i+1:]...)
			return item, true
		}
	}
	var zero PendingMessage[T]
	return zero, false
}

func (q *PendingQueue[T]) ConsumePrefix(match func(PendingMessage[T]) bool) []PendingMessage[T] {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}
	consumed := 0
	for consumed < len(q.items) {
		if !match(q.items[consumed]) {
			break
		}
		consumed++
	}
	if consumed == 0 {
		return nil
	}
	out := append([]PendingMessage[T](nil), q.items[:consumed]...)
	q.items = q.items[consumed:]
	return out
}
