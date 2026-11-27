package util

import "context"

// sessionKeyType is the context key carrying the ggcode session id that a
// tool call belongs to. It is stamped once per agent run (RunStreamWithContent)
// and inherited by everything derived from that context - sub-agent runs,
// parallel tool batches, detached goroutines that keep the parent ctx - so
// per-session resources (the browser profile, see internal/tool/browser.go)
// can be resolved from the call instead of being configured on a shared
// tool instance.
type sessionKeyType struct{}

// WithSessionID returns a context that carries id as the session id. An empty
// id returns ctx unchanged.
func WithSessionID(ctx context.Context, id string) context.Context {
	if ctx == nil || id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKeyType{}, id)
}

// EnsureSessionID stamps id on ctx only when ctx does not carry a session id
// yet. A sub-agent's run inherits the spawning session's id through its
// context; its own (sub-agent) id must not replace it, or every spawn would
// get its own per-session resources.
func EnsureSessionID(ctx context.Context, id string) context.Context {
	if ctx == nil || id == "" || SessionIDFromContext(ctx) != "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKeyType{}, id)
}

// SessionIDFromContext returns the session id carried by ctx, or "".
func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(sessionKeyType{}).(string)
	return id
}
