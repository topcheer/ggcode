package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/util"
)

const (
	secureConfigDirMode  os.FileMode = 0o700
	secureConfigFileMode os.FileMode = 0o600
)

func ensureSecureConfigDir(dir string) error {
	if err := os.MkdirAll(dir, secureConfigDirMode); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := os.Chmod(dir, secureConfigDirMode); err != nil {
		return fmt.Errorf("chmod config directory: %w", err)
	}
	return nil
}

func writeSecureConfigFile(path string, data []byte) error {
	if err := ensureSecureConfigDir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := util.AtomicWriteFile(path, data, secureConfigFileMode); err != nil {
		return err
	}
	if err := os.Chmod(path, secureConfigFileMode); err != nil {
		return fmt.Errorf("chmod config file: %w", err)
	}
	return nil
}
