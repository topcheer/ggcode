//go:build windows

package sessionidx

import "path/filepath"

// NewTransport builds the platform-default leader transport rooted at
// baseDir. On Windows the endpoint is a loopback listener whose
// ephemeral port is published at baseDir/idxd.discovery.
func NewTransport(baseDir string) Transport {
	return &LoopbackTransport{discPath: filepath.Join(baseDir, "idxd.discovery")}
}
