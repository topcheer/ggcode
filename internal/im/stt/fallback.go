package stt

import (
	"context"

	"github.com/topcheer/ggcode/internal/debug"
)

// FallbackTranscriber tries the primary transcriber first, then falls back to the secondary.
type FallbackTranscriber struct {
	primary   Transcriber
	secondary Transcriber
}

// NewFallback creates a transcriber that tries primary first, then secondary on failure.
// Either can be nil and will be skipped.
func NewFallback(primary, secondary Transcriber) *FallbackTranscriber {
	return &FallbackTranscriber{primary: primary, secondary: secondary}
}

func (f *FallbackTranscriber) Transcribe(ctx context.Context, req Request) (Result, error) {
	if f.primary != nil {
		result, err := f.primary.Transcribe(ctx, req)
		if err == nil && result.Text != "" {
			return result, nil
		}
		if err != nil {
			debug.Log("stt", "primary STT failed: %v, trying fallback", err)
		}
	}
	if f.secondary != nil {
		return f.secondary.Transcribe(ctx, req)
	}
	return Result{}, nil
}
