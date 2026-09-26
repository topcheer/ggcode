package config

import (
	"os"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// The external-file full rewriters must hold the per-file lock keyed by the
// target path. Every other writer of these files (patchExternalFile,
// Config.Save()'s saveExternalSections pass, migrateSectionToExternal)
// synchronizes on the same lockConfigFile mutex, so a saver that skips the
// lock can silently clobber a concurrent writer's entry with a stale
// full-section snapshot (atomic rename prevents torn files, not lost
// updates).
//
// Regression shape: each saver below is invoked while the file lock is held
// externally; it must block until the lock is released instead of writing
// immediately. If a saver ever drops its lock, the write completes in
// microseconds and the first select catches it.

func TestExternalFileSaversHonorPerFileLock(t *testing.T) {
	tests := []struct {
		name string
		path func(dir string) string
		save func(dir string) error
	}{
		{
			name: "SaveVendors",
			path: VendorsPath,
			save: func(dir string) error {
				return SaveVendors(dir, map[string]VendorConfig{
					"lock-test-vendor": {
						DisplayName: "Lock Test",
						Endpoints: map[string]EndpointConfig{
							"ep": {Protocol: "openai", BaseURL: "https://lock-test.invalid"},
						},
					},
				})
			},
		},
		{
			name: "SaveIMConfig",
			path: IMPath,
			save: func(dir string) error {
				return SaveIMConfig(dir, &IMConfig{
					Adapters: map[string]IMAdapterConfig{
						"locktest": {Platform: "qq", Enabled: true},
					},
				})
			},
		},
		{
			name: "SaveMCPServers",
			path: MCPServersPath,
			save: func(dir string) error {
				return SaveMCPServers(dir, []MCPServerConfig{
					{Name: "lock-test-server", Command: "true"},
				})
			},
		},
		{
			name: "SaveMCPDeleted",
			path: MCPDeletedPath,
			save: func(dir string) error {
				return SaveMCPDeleted(dir, []string{"lock-test-tombstone"})
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := tc.path(dir)

			release := lockConfigFile(path)

			done := make(chan error, 1)
			go func() { done <- tc.save(dir) }()

			select {
			case err := <-done:
				t.Fatalf("%s wrote while the per-file lock was held (err=%v) - lock is missing", tc.name, err)
			case <-time.After(150 * time.Millisecond):
			}

			release()

			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("%s failed after lock release: %v", tc.name, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s did not complete after lock release", tc.name)
			}
		})
	}
}

// Concurrent full-section rewrites of the same external file must all
// succeed and leave a parseable file; the per-file lock serializes the
// write window so no intermediate snapshot can be overwritten out of order.
func TestSaveIMConfigConcurrentWritersStayConsistent(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				im := &IMConfig{
					Adapters: map[string]IMAdapterConfig{
						"writer": {Platform: "qq", Enabled: true, OutputMode: "summary"},
					},
				}
				if err := SaveIMConfig(dir, im); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent SaveIMConfig failed: %v", err)
	}
	raw := map[string]interface{}{}
	data, err := os.ReadFile(IMPath(dir))
	if err != nil {
		t.Fatalf("im.yaml unreadable after concurrent saves: %v", err)
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("im.yaml unparseable after concurrent saves: %v", err)
	}
}
