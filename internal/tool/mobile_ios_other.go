//go:build !darwin

package tool

import "context"

type iosBackend struct{}

func newIOSBackend() *iosBackend { return nil }

func (b *iosBackend) cleanup() {}

func (b *iosBackend) defaultDevice() string { return "" }

func (b *iosBackend) devices(ctx context.Context) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) boot(ctx context.Context, device string) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) install(ctx context.Context, device, appPath string) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) uninstall(ctx context.Context, device, pkg string) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) launch(ctx context.Context, device, pkg string) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) close(ctx context.Context, device, pkg string) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) snapshot(ctx context.Context, device string) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) screenshot(ctx context.Context, device, format string, quality int, headless bool) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) tap(ctx context.Context, device, ref string, x, y int) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) typeText(ctx context.Context, device, ref, text string, x, y int) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) swipe(ctx context.Context, device, ref string, x, y, endX, endY int) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) press(ctx context.Context, device, key string) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) logs(ctx context.Context, device, pkg string, lines int) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}

func (b *iosBackend) listApps(ctx context.Context, device string) (Result, error) {
	return Result{IsError: true, Content: "iOS simulator support requires macOS"}, nil
}
