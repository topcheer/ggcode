package vcs

import (
	"context"
	"errors"
)

var ErrCheckoutNotSupported = errors.New("checkout not supported for this VCS")

type VCS interface {
	Name() string

	DisplayName() string

	Status(ctx context.Context, dir string) (string, error)

	Diff(ctx context.Context, dir string, cached bool, file string) (string, error)

	Log(ctx context.Context, dir string, count int) (string, error)

	Add(ctx context.Context, dir string, files []string) (string, error)

	Commit(ctx context.Context, dir string, message string) (string, error)

	CurrentBranch(ctx context.Context, dir string) (string, error)

	IsClean(ctx context.Context, dir string) (bool, error)

	Checkout(ctx context.Context, dir, branch string, create bool, startPoint string) (string, error)
}
