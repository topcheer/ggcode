package memory

import (
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// ChangeEvent describes one successful mutation of a stored memory entry.
// It is delivered to the registered event sink after the mutation has been
// durably applied (history archived, live file swapped / entry tombstoned).
type ChangeEvent struct {
	// Scope is the scope label registered with the sink ("global" or
	// "project").
	Scope string
	// Op is "save" or "delete".
	Op string
	// Key is the on-disk (sanitized) key of the mutated entry.
	Key string
	// Source names the mutation origin, e.g. "save_memory", "delete_memory",
	// or a curator identifier. Subscribers use it to ignore self-triggered
	// events and avoid feedback loops.
	Source string
	// At is the mutation time.
	At time.Time
}

// SetEventSink registers ch as a non-blocking sink for change events emitted
// after every successful save/delete on this store; scope is stamped onto
// each event. Events are dropped (with a debug log) when ch is full, so slow
// subscribers never block memory writes. Passing a nil channel clears the
// sink.
func (am *AutoMemory) SetEventSink(scope string, ch chan<- ChangeEvent) {
	am.mu.Lock()
	am.eventScope = scope
	am.eventSink = ch
	am.mu.Unlock()
}

// emitChange delivers one change event to the registered sink, dropping it
// when nobody subscribed or the sink buffer is full.
func (am *AutoMemory) emitChange(op, key, source string) {
	am.mu.Lock()
	scope, ch := am.eventScope, am.eventSink
	am.mu.Unlock()
	if ch == nil {
		return
	}
	ev := ChangeEvent{Scope: scope, Op: op, Key: key, Source: source, At: time.Now()}
	select {
	case ch <- ev:
	default:
		debug.Log("memory", "change event dropped (sink full): %s %s/%s", op, scope, key)
	}
}
