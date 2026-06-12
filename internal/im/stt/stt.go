package stt

import (
	"context"
	"time"
)

type Request struct {
	MIME       string
	Name       string
	Path       string
	DataBase64 string
	Metadata   map[string]string
}

type Result struct {
	Text       string
	Provider   string
	Model      string
	Confidence float64
	Duration   time.Duration
	Metadata   map[string]string
}

type Transcriber interface {
	Transcribe(context.Context, Request) (Result, error)
}
