//go:build linux

package image

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func ReadClipboard() (Image, error) {
	wlPasteAvailable := commandAvailable("wl-paste")
	xclipAvailable := commandAvailable("xclip")

	if wlPasteAvailable {
		if img, err := readClipboardWithMIMEProbe("wl-paste", []string{"--type"}); err == nil {
			return img, nil
		} else if err != nil && err != ErrClipboardImageUnavailable {
			return Image{}, err
		}
	}

	if xclipAvailable {
		if img, err := readClipboardWithMIMEProbe("xclip", []string{"-selection", "clipboard", "-t"}); err == nil {
			return img, nil
		} else if err != nil && err != ErrClipboardImageUnavailable {
			return Image{}, err
		}
	}

	if !wlPasteAvailable && !xclipAvailable {
		return Image{}, linuxClipboardToolsMissingError()
	}
	return Image{}, linuxClipboardImageUnavailableError(wlPasteAvailable, xclipAvailable)
}

func linuxClipboardToolsMissingError() error {
	sessionType := strings.ToLower(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")))
	switch sessionType {
	case "wayland":
		return fmt.Errorf("clipboard image paste on Wayland requires wl-paste. Install wl-clipboard, then try again")
	case "x11":
		return fmt.Errorf("clipboard image paste on X11 requires xclip. Install xclip, then try again")
	default:
		return fmt.Errorf("clipboard image paste on Linux requires wl-paste (Wayland) or xclip (X11). Install wl-clipboard or xclip, then try again")
	}
}

func linuxClipboardImageUnavailableError(wlPasteAvailable, xclipAvailable bool) error {
	availableTypes := linuxClipboardTypes(wlPasteAvailable, xclipAvailable)
	if len(availableTypes) == 0 {
		return fmt.Errorf("clipboard does not contain a supported image. Copy a PNG, JPEG, GIF, or WebP image and try again")
	}
	return fmt.Errorf("clipboard does not contain a supported image. Available clipboard types: %s. Supported image types: %s", strings.Join(availableTypes, ", "), strings.Join(clipboardImageMIMEs, ", "))
}

func linuxClipboardTypes(wlPasteAvailable, xclipAvailable bool) []string {
	if wlPasteAvailable {
		if types := listClipboardTypes("wl-paste", "--list-types"); len(types) > 0 {
			return types
		}
	}
	if xclipAvailable {
		if types := listClipboardTypes("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"); len(types) > 0 {
			return types
		}
	}
	return nil
}

func listClipboardTypes(name string, args ...string) []string {
	out, err := runClipboardCommand(name, args...)
	if err != nil {
		return nil
	}
	return parseClipboardTypes(string(out))
}

func parseClipboardTypes(raw string) []string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(fields))
	types := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || seen[field] {
			continue
		}
		seen[field] = true
		types = append(types, field)
	}
	return types
}

func readClipboardWithMIMEProbe(command string, argsPrefix []string) (Image, error) {
	if !commandAvailable(command) {
		return Image{}, ErrClipboardImageUnavailable
	}

	for _, mime := range clipboardImageMIMEs {
		args := append(append([]string{}, argsPrefix...), mime)
		args = append(args, clipboardOutputArgs(command)...)
		data, err := runClipboardCommand(command, args...)
		if err != nil {
			continue
		}
		img, err := decodeClipboardImageData(data)
		if err == nil {
			return img, nil
		}
	}
	return Image{}, ErrClipboardImageUnavailable
}

func clipboardOutputArgs(command string) []string {
	switch command {
	case "wl-paste":
		return nil
	default:
		return []string{"-o"}
	}
}

func runClipboardCommand(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, commandOutputError("reading clipboard image", err, stderr.Bytes())
	}
	return out, nil
}
