package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	oscillationReversalThreshold = 2

	oscillationMaxTracked = 30

	oscillationSigMaxLen = 2048

	oscillationMaxWarnings = 1
)

type oscillationState struct {
	signatures map[string][]sigEntry
	fired      int
}

type sigEntry struct {
	sig     string
	isNew   bool
	iterTag int
}

func newOscillationState() *oscillationState {
	return &oscillationState{
		signatures: make(map[string][]sigEntry),
	}
}

func (o *oscillationState) reset() {
	o.signatures = make(map[string][]sigEntry)
	o.fired = 0
}

func contentSig(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > oscillationSigMaxLen {
		text = text[:oscillationSigMaxLen]
	}
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:8])
}

func (o *oscillationState) recordEdit(toolName string, args json.RawMessage, iteration int) {
	if len(args) == 0 {
		return
	}
	var parsed map[string]any
	if json.Unmarshal(args, &parsed) != nil {
		return
	}

	switch toolName {
	case "edit_file":
		path, oldT, newT := extractEditContent(parsed)
		if path == "" {
			return
		}
		o.addSignature(path, oldT, newT, iteration)

	case "multi_edit_file":
		path, edits := extractMultiEditContent(parsed)
		if path == "" || len(edits) == 0 {
			return
		}
		var allOld, allNew strings.Builder
		for _, e := range edits {
			allOld.WriteString(e.oldText)
			allNew.WriteString(e.newText)
		}
		o.addSignature(path, allOld.String(), allNew.String(), iteration)

	case "multi_file_edit":

		o.recordMultiFileEditSignatures(parsed, iteration)

	case "batch_replace":

		o.recordBatchReplaceSignatures(parsed, iteration)
	}
}

func (o *oscillationState) recordMultiFileEditSignatures(parsed map[string]any, iteration int) {
	type fileEdit struct {
		Path  string `json:"path"`
		Edits []struct {
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		} `json:"edits"`
	}
	var files []fileEdit
	raw, ok := parsed["files"]
	if !ok {
		return
	}
	b, _ := json.Marshal(raw)
	if json.Unmarshal(b, &files) != nil {
		return
	}
	for _, f := range files {
		var allOld, allNew strings.Builder
		for _, e := range f.Edits {
			allOld.WriteString(e.OldText)
			allNew.WriteString(e.NewText)
		}
		if f.Path != "" && allOld.Len()+allNew.Len() > 0 {
			o.addSignature(f.Path, allOld.String(), allNew.String(), iteration)
		}
	}
}

func (o *oscillationState) recordBatchReplaceSignatures(parsed map[string]any, iteration int) {
	pattern, _ := parsed["pattern"].(string)
	if pattern == "" {
		return
	}
	replacement, _ := parsed["replacement"].(string)
	rawList, ok := parsed["files"].([]any)
	if !ok {
		return
	}
	for _, item := range rawList {
		if p, ok := item.(string); ok && p != "" {
			o.addSignature(p, pattern, replacement, iteration)
		}
	}
}

type multiEditPair struct {
	oldText string
	newText string
}

func extractEditContent(args map[string]any) (path, oldText, newText string) {
	if p, ok := args["path"].(string); ok {
		path = p
	}
	if p, ok := args["file_path"].(string); ok {
		path = p
	}
	if v, ok := args["old_text"].(string); ok {
		oldText = v
	}
	if v, ok := args["new_text"].(string); ok {
		newText = v
	}
	return
}

func extractMultiEditContent(args map[string]any) (path string, edits []multiEditPair) {
	if p, ok := args["path"].(string); ok {
		path = p
	}
	if p, ok := args["file_path"].(string); ok {
		path = p
	}
	if files, ok := args["edits"].([]any); ok {
		for _, f := range files {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			var pair multiEditPair
			if v, ok := fm["old_text"].(string); ok {
				pair.oldText = v
			}
			if v, ok := fm["new_text"].(string); ok {
				pair.newText = v
			}
			if pair.oldText != "" || pair.newText != "" {
				edits = append(edits, pair)
			}
		}
	}
	return
}

func (o *oscillationState) addSignature(path, oldText, newText string, iter int) {
	if len(o.signatures) >= oscillationMaxTracked {
		if _, exists := o.signatures[path]; !exists {
			return
		}
	}

	oldSig := contentSig(oldText)
	newSig := contentSig(newText)

	if oldSig == newSig || (oldText == "" && newText == "") {
		return
	}

	sigs := o.signatures[path]

	if oldText != "" {
		sigs = append(sigs, sigEntry{sig: oldSig, isNew: false, iterTag: iter})
	}
	if newText != "" {
		sigs = append(sigs, sigEntry{sig: newSig, isNew: true, iterTag: iter})
	}

	if len(sigs) > 20 {
		sigs = sigs[len(sigs)-20:]
	}

	o.signatures[path] = sigs
}

func (o *oscillationState) countReversals(path string) int {
	sigs := o.signatures[path]
	if len(sigs) < 2 {
		return 0
	}

	reversals := 0
	seenAsOld := make(map[string]int)
	for _, s := range sigs {
		if s.isNew {
			if seenAsOld[s.sig] > 0 {

				reversals++
				seenAsOld[s.sig]--
			}
		} else {
			seenAsOld[s.sig]++
		}
	}
	return reversals
}

func (o *oscillationState) check() string {
	if o.fired >= oscillationMaxWarnings {
		return ""
	}

	var oscillatingFiles []string
	for path := range o.signatures {
		if o.countReversals(path) >= oscillationReversalThreshold {
			oscillatingFiles = append(oscillatingFiles, path)
		}
	}

	if len(oscillatingFiles) == 0 {
		return ""
	}

	o.fired++
	debug.Log("edit-oscillation", "oscillation detected on %d files: %v", len(oscillatingFiles), oscillatingFiles)

	fileList := oscillatingFiles
	if len(fileList) > 3 {
		fileList = fileList[:3]
	}

	var b strings.Builder
	b.WriteString("[edit-oscillation] Detected semantic back-and-forth on ")
	switch len(oscillatingFiles) {
	case 1:
		b.WriteString("1 file")
	default:
		fmt.Fprintf(&b, "%d files", len(oscillatingFiles))
	}
	b.WriteString(":\n")
	for _, f := range fileList {
		fmt.Fprintf(&b, "  - %s\n", f)
	}
	b.WriteString("\n")
	b.WriteString("You are oscillating between two approaches - editing the same content back and forth ")
	b.WriteString("without resolving the underlying trade-off. This wastes tokens and risks instability.\n")
	b.WriteString("Recommended actions:\n")
	b.WriteString("1. Step back and re-read the file holistically to understand the current state\n")
	b.WriteString("2. Decide which approach is correct and commit to it - do not partially apply both\n")
	b.WriteString("3. If unsure which approach is better, explain the trade-off to the user rather than continuing to flip\n")

	return b.String()
}
