package tool

import (
	"os"

	"github.com/topcheer/ggcode/internal/util"
)

// atomicWriteFile is a thin alias for util.AtomicWriteFile, kept so existing
// tool callers don't need to import internal/util directly. See locks.md S4.
func atomicWriteFile(path string, data []byte, defaultMode os.FileMode) error {
	return util.AtomicWriteFile(path, data, defaultMode)
}
