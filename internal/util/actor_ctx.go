package util

import "context"

// ActorKey (r29) carries the identity of the agent EXECUTING the current
// tool call through the tool-execution context. The main agent's loop,
// sub-agent runs and swarm teammates share one tool registry - without an
// explicit identity in ctx, memory provenance (and any future per-actor
// semantics) cannot tell who performed a write.
//
// Lives in util (not tool) because the injector (internal/subagent's
// runner) and the consumer (internal/tool's save_memory) sit on opposite
// sides of an import cycle; both already depend on util.

// actorKeyType is the unexported context key type.
type actorKeyType struct{}

// ActorMain labels the primary agent (no sub-agent/swarm wrapper).
const ActorMain = "main"

// WithActor returns a ctx annotated with the executing actor's identity.
// An empty id keeps the ctx unchanged (legacy callers).
func WithActor(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, actorKeyType{}, id)
}

// ActorFromContext returns the executing actor's identity, or "" when the
// call path never annotated one (main agent before full wiring, tests).
func ActorFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(actorKeyType{}).(string)
	return id
}
