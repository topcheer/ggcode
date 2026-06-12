//go:build darwin

package image

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func ReadClipboard() (Image, error) {
	tmpDir, err := os.MkdirTemp("", "ggcode-clipboard-*")
	if err != nil {
		return Image{}, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pngPath := filepath.Join(tmpDir, "clipboard.png")
	if err := writeClipboardImage("«class PNGf»", pngPath); err == nil {
		return ReadFile(pngPath)
	}

	tiffPath := filepath.Join(tmpDir, "clipboard.tiff")
	if err := writeClipboardImage("TIFF picture", tiffPath); err != nil {
		return Image{}, ErrClipboardImageUnavailable
	}
	if err := convertClipboardTIFFToPNG(tiffPath, pngPath); err != nil {
		return Image{}, err
	}
	return ReadFile(pngPath)
}

func writeClipboardImage(formatExpr, outPath string) error {
	script := []string{
		fmt.Sprintf(`set outFile to POSIX file %q`, outPath),
		fmt.Sprintf(`set clipData to the clipboard as %s`, formatExpr),
		`set fileRef to open for access outFile with write permission`,
		`try`,
		`set eof of fileRef to 0`,
		`write clipData to fileRef`,
		`close access fileRef`,
		`on error errMsg number errNum`,
		`try`,
		`close access fileRef`,
		`end try`,
		`error errMsg number errNum`,
		`end try`,
	}
	args := make([]string, 0, len(script)*2)
	for _, line := range script {
		args = append(args, "-e", line)
	}
	cmd := exec.Command("osascript", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return commandOutputError("reading clipboard image", err, output)
	}
	return nil
}

func convertClipboardTIFFToPNG(srcPath, dstPath string) error {
	cmd := exec.Command("sips", "-s", "format", "png", srcPath, "--out", dstPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return commandOutputError("converting clipboard image", err, output)
	}
	return nil
}
