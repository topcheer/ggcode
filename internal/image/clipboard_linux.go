//go:build linux

package image

import (
	"bytes"
	"fmt"
	"os/exec"
)

func ReadClipboard() (Image, error) {
	if img, err := readClipboardWithMIMEProbe("wl-paste", []string{"--no-newline", "--type"}); err == nil {
		return img, nil
	} else if err != nil && err != ErrClipboardImageUnavailable {
		return Image{}, err
	}

	if img, err := readClipboardWithMIMEProbe("xclip", []string{"-selection", "clipboard", "-t"}); err == nil {
		return img, nil
	} else if err != nil && err != ErrClipboardImageUnavailable {
		return Image{}, err
	}

	return Image{}, fmt.Errorf("clipboard image paste requires wl-paste (Wayland) or xclip (X11), or the clipboard does not contain a supported image")
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
