package util

import "context"

type actorKeyType struct{}

func WithActor(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, actorKeyType{}, id)
}

func ActorFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(actorKeyType{}).(string)
	return id
}
