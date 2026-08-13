//go:build windows

package tool

import (
	"context"
	"fmt"
)

func executeDesktopControl(ctx context.Context, p desktopParams) (Result, error) {
	// Windows support will be implemented with SendInput via syscall
	// or PowerShell Add-Type. For now, return a descriptive message.
	return Result{}, fmt.Errorf("desktop_control is not yet implemented on Windows")
}
