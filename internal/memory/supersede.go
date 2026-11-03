package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	supersedeSidecarFile = ".superseded.json"

	maxSupersedeTargets = 3
)

var replacementMarkers = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\breplaces\b`),
	regexp.MustCompile(`(?i)\bsupersedes\b`),
	regexp.MustCompile(`(?i)\binstead of\b`),
	regexp.MustCompile(`(?i)\bswitched to\b`),
	regexp.MustCompile(`(?i)\bmigrated? to\b`),
	regexp.MustCompile(`(?i)\bnow use\b`),
	regexp.MustCompile(`(?i)\bno longer\b`),
	regexp.MustCompile(`(?i)\bdeprecated\b`),
	regexp.MustCompile(`取代`),
	regexp.MustCompile(`改用`),
	regexp.MustCompile(`替换为`),
	regexp.MustCompile(`不再`),
	regexp.MustCompile(`弃用`),
}

var (
	parentheticalRe = regexp.MustCompile(`\([^\n)]*\)`)
	markerClauseRe  = regexp.MustCompile(`(?i)[,;.]?\s*(?:instead of|replaces|supersedes|switched to|migrated to|now use|no longer|deprecated)\b[^\n]*`)
	cjkMarkerClause = regexp.MustCompile(`[,;。；]?\s*(?:取代|改用|替换为|不再|弃用)[^\n]*`)
)

func stripReplacementProse(content string) string {
	s := parentheticalRe.ReplaceAllString(content, " ")
	s = markerClauseRe.ReplaceAllString(s, " ")
	s = cjkMarkerClause.ReplaceAllString(s, " ")
	return s
}

type SupersedeRecord struct {
	By string    `json:"by"`
	At time.Time `json:"at"`
}

type supersedeIndex struct {
	Entries map[string]*SupersedeRecord `json:"entries"`
}

func hasReplacementSemantics(content string) bool {
	for _, re := range replacementMarkers {
		if re.MatchString(content) {
			return true
		}
	}
	return false
}

func (am *AutoMemory) DetectSupersession(key, content string) []string {
	if !hasReplacementSemantics(content) {
		return nil
	}

	newClaims := extractClaims(stripReplacementProse(content))
	if len(newClaims) == 0 {
		return nil
	}

	metas, err := am.collectMetas()
	if err != nil {
		debug.Log("memory", "supersede detect: failed to read dir %s: %v", am.dir, err)
		return nil
	}

	selfKey := disambiguateKey(key, sanitizeKey(key))

	var olds []string
	for _, m := range metas {
		if m.Key == selfKey {
			continue
		}
		path := filepath.Join(am.dir, m.Key+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		existingClaims := extractClaims(string(data))
		if len(existingClaims) == 0 {
			continue
		}
		for subject, newVal := range newClaims {
			existingVal, ok := existingClaims[subject]
			if !ok {
				continue
			}
			if claimsConflict(newVal, existingVal) {
				olds = append(olds, m.Key)
				break
			}
		}
		if len(olds) >= maxSupersedeTargets {
			break
		}
	}
	return olds
}

func (am *AutoMemory) ApplySupersession(key string, oldKeys []string) error {
	idx := am.loadSuperseded()
	if idx.Entries == nil {
		idx.Entries = make(map[string]*SupersedeRecord)
	}
	now := time.Now().UTC()
	for _, old := range oldKeys {
		if old == "" || old == key {
			continue
		}
		idx.Entries[old] = &SupersedeRecord{By: key, At: now}
	}

	delete(idx.Entries, key)
	return am.saveSuperseded(idx)
}

func (am *AutoMemory) loadSuperseded() supersedeIndex {
	var idx supersedeIndex
	data, err := os.ReadFile(filepath.Join(am.dir, supersedeSidecarFile))
	if err != nil {
		return idx
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		debug.Log("memory", "supersede sidecar corrupt in %s: %v", am.dir, err)
		return supersedeIndex{}
	}
	return idx
}

func (am *AutoMemory) saveSuperseded(idx supersedeIndex) error {
	if idx.Entries == nil {
		idx.Entries = make(map[string]*SupersedeRecord)
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(am.dir, 0755); err != nil {
		return err
	}

	path := filepath.Join(am.dir, supersedeSidecarFile)
	tmp, err := os.CreateTemp(am.dir, supersedeSidecarFile+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return err
	}
	muAny, _ := writeMu.LoadOrStore(path, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func (am *AutoMemory) SupersededSet() map[string]bool {
	idx := am.loadSuperseded()
	if len(idx.Entries) == 0 {
		return nil
	}
	set := make(map[string]bool, len(idx.Entries))
	for k := range idx.Entries {
		set[k] = true
	}
	return set
}

func FormatSupersedeNote(oldKeys []string) string {
	if len(oldKeys) == 0 {
		return ""
	}
	shown := oldKeys
	if len(shown) > maxSupersedeTargets {
		shown = shown[:maxSupersedeTargets]
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Superseded %d older %s (retired from prompt, kept on disk for history): ",
		len(oldKeys), pluralMemory(len(oldKeys))))
	b.WriteString(strings.Join(shown, ", "))
	return b.String()
}

func pluralMemory(n int) string {
	if n == 1 {
		return "memory"
	}
	return "memories"
}
