package wailskit

// #2180: the desktop registry declares Secret-ness explicitly (best
// practice), but a name that drifts OUT of the single-source wide table
// would silently render in cleartext if any code path ever fell back
// to name heuristics. Pin: every registry Secret field must hit the
// single source.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/secretfield"
)

func TestIMPlatformRegistrySecretFieldsHitSingleSource(t *testing.T) {
	checked := 0
	for _, p := range GetIMPlatformRegistry() {
		for _, f := range p.Fields {
			if f.Secret {
				checked++
				if !secretfield.LooksLikeSecretField(f.Key) {
					t.Errorf("%s.%s is Secret:true but misses the single-source table - rename or extend secretfield", p.ID, f.Key)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("registry has no Secret fields - the pin lost its subject")
	}
}
