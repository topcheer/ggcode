package main

import (
	"fmt"
	"os"
)

const desktopTempFileMode os.FileMode = 0o600

func createTempPath(prefix, suffix string) (string, error) {
	tmp, err := os.CreateTemp("", prefix+"-*"+suffix)
	if err != nil {
		return "", fmt.Errorf("creating temp path: %w", err)
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("closing temp path: %w", err)
	}
	return name, nil
}

func writeTempDataFile(prefix, suffix string, data []byte) (string, error) {
	tmp, err := os.CreateTemp("", prefix+"-*"+suffix)
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	name := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(name)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return "", fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Chmod(desktopTempFileMode); err != nil {
		cleanup()
		return "", fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("closing temp file: %w", err)
	}
	return name, nil
}
