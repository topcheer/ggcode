package tui

import (
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

func copyTextToClipboard(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("clipboard text is empty")
	}
	command, args := clipboardCommand()
	if command == "" {
		return fmt.Errorf("clipboard copy is not supported on %s", runtime.GOOS)
	}
	cmd := exec.Command(command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	if _, err := io.WriteString(stdin, value); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return err
	}
	if err := stdin.Close(); err != nil {
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

func openSystemURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("url is empty")
	}
	// #1763 case 2: callers include OAuth flows where the URL comes from an
	// EXTERNAL MCP server's response (VerificationURI/authorizeURL) - a
	// hostile server could hand back file:// or a custom scheme and let the
	// OS protocol handler dispatch it. Allow only web schemes.
	if u, err := url.Parse(rawURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("refusing to open non-http(s) URL: %q", rawURL)
	}
	command, args := openURLCommand(rawURL)
	if command == "" {
		return fmt.Errorf("opening browser is not supported on %s", runtime.GOOS)
	}
	return exec.Command(command, args...).Start()
}

func clipboardCommand() (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "pbcopy", nil
	case "linux":
		if _, err := exec.LookPath("wl-copy"); err == nil {
			return "wl-copy", nil
		}
		if _, err := exec.LookPath("xclip"); err == nil {
			return "xclip", []string{"-selection", "clipboard"}
		}
	case "windows":
		return "cmd", []string{"/c", "clip"}
	}
	return "", nil
}

func openURLCommand(rawURL string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{rawURL}
	case "linux":
		return "xdg-open", []string{rawURL}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", rawURL}
	}
	return "", nil
}
