package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Instruction-driven per-entry forgetting (r10, sa-160).
//
// 2026 memory architecture surveys list "how agents forget" alongside
// remembering as a first-class engineering challenge, but every existing
// forgetting channel here is passive: TTL expiry, date-horizon, dedup,
// supersession. The only user-facing controls were /memory list (see) and
// /memory clear (nuke everything) — the user who says "forget the pnpm
// note, it's wrong now" had no per-entry lever.
//
// Design follows the #779 only-GC-deletes doctrine (and the r100
// date-horizon precedent): ForgetKey does NOT remove the file. It marks
// the key in a sidecar (user-forgotten.json), the prompt loader filters
// marked keys out (same pattern as the SupersededSet filter in
// loadForPrompt), and GarbageCollect is the only thing that physically
// removes the file. RestoreKey clears the mark — soft delete is
// reversible, which is the selling point over /memory clear.

// forgottenFileName is the sidecar storing user-forgotten keys, keyed by
// the sanitized+disambiguated filename stem (same convention as the usage
// sidecar, so it matches MemoryMeta.Key directly).
const forgottenFileName = "user-forgotten.json"

// userForgotten is the persisted sidecar payload.
type userForgotten struct {
	// Entries maps the filename stem -> time the user forgot it.
	Entries map[string]time.Time `json:"entries"`
}

// loadForgotten reads the sidecar (best-effort; missing file = nothing
// forgotten, corrupt file = treated as missing with a debug log, matching
// loadConsolidationState's fail-open posture).
func (am *AutoMemory) loadForgotten() userForgotten {
	out := userForgotten{Entries: map[string]time.Time{}}
	data, err := os.ReadFile(filepath.Join(am.dir, forgottenFileName))
	if err != nil {
		return out
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Entries == nil {
		debug.Log("memory", "forget: sidecar unreadable, treating as empty: %v", err)
		return userForgotten{Entries: map[string]time.Time{}}
	}
	return out
}

// saveForgotten persists the sidecar atomically (temp+rename, same as
// saveConsolidationState).
func (am *AutoMemory) saveForgotten(f userForgotten) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(am.dir, forgottenFileName+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(am.dir, forgottenFileName))
}

// ForgottenSet returns the set of forgotten filename stems for O(1)
// filtering. Empty map when nothing is forgotten (the common case —
// callers gate on len > 0 like the SupersededSet filter).
func (am *AutoMemory) ForgottenSet() map[string]bool {
	f := am.loadForgotten()
	out := make(map[string]bool, len(f.Entries))
	for k := range f.Entries {
		out[k] = true
	}
	return out
}

// ForgetKey marks a single memory entry as user-forgotten: it stops being
// injected into prompts immediately, stays on disk (restorable) until the
// next GarbageCollect physically digests it. The key existence check and
// error wording mirror DeleteMemory so both surfaces feel the same.
func (am *AutoMemory) ForgetKey(key string) error {
	// Same resolution as DeleteMemory (#775): a non-injective key must
	// resolve to the hash-suffixed file it was saved under.
	safe := disambiguateKey(key, sanitizeKey(key))
	path := filepath.Join(am.dir, safe+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("memory %q not found", key)
	}
	f := am.loadForgotten()
	if _, dup := f.Entries[safe]; dup {
		return nil // idempotent: already forgotten
	}
	f.Entries[safe] = time.Now()
	if err := am.saveForgotten(f); err != nil {
		return err
	}
	debug.Log("memory", "forget: marked %q as user-forgotten (GC will digest)", safe)
	return nil
}

// RestoreKey clears the forget mark, returning the entry to prompt
// injection. Errors when the key carries no mark (nothing to restore) —
// a key that was never forgotten or has already been digested by GC.
func (am *AutoMemory) RestoreKey(key string) error {
	safe := disambiguateKey(key, sanitizeKey(key))
	f := am.loadForgotten()
	if _, ok := f.Entries[safe]; !ok {
		return fmt.Errorf("memory %q is not forgotten", key)
	}
	delete(f.Entries, safe)
	return am.saveForgotten(f)
}

// ListForgotten returns the forgotten keys, oldest first, formatted as
// "key (forgotten <duration> ago)" lines for direct display.
func (am *AutoMemory) ListForgotten() []string {
	f := am.loadForgotten()
	type kv struct {
		key string
		at  time.Time
	}
	items := make([]kv, 0, len(f.Entries))
	for k, at := range f.Entries {
		items = append(items, kv{k, at})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, fmt.Sprintf("%s (forgotten %s ago)", it.key, time.Since(it.at).Round(time.Minute)))
	}
	return out
}

// forgottenLine returns one display line for a forgotten key, or "" when
// the key is not forgotten. Used by list views that annotate entries.
func (am *AutoMemory) forgottenLine(key string) string {
	f := am.loadForgotten()
	at, ok := f.Entries[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%s [forgotten %s ago]", key, time.Since(at).Round(time.Minute)))
}
