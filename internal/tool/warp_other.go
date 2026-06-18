//go:build !darwin

package tool

import "fmt"

func (w *WarpTool) executeStatus() Result {
	if !warpAvailable() {
		return Result{Content: "warp: not detected (TERM_PROGRAM != WarpTerminal)"}
	}
	return Result{Content: "warp: detected (Linux support not yet implemented)"}
}

func (w *WarpTool) clickMenu(menuItem string) Result {
	return Result{IsError: true, Content: fmt.Sprintf("warp: menu actions not supported on this platform")}
}

func (w *WarpTool) executeInput(text string) Result {
	return Result{IsError: true, Content: "warp: input not supported on this platform"}
}

func (w *WarpTool) executeSendKey(key string, modifiers string) Result {
	return Result{IsError: true, Content: "warp: send_key not supported on this platform"}
}
