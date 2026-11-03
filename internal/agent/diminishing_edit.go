package agent

import (
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	diminishingWindow = 6

	diminishingMinEdits = 4

	diminishingTrivialSize = 200

	diminishingShrinkRatio = 0.4
)

type editSizeEntry struct {
	toolName string
	size     int
	filePath string
}

type diminishingEditState struct {
	entries []editSizeEntry
	warned  bool
}

func newDiminishingEditState() *diminishingEditState {
	return &diminishingEditState{}
}

func (s *diminishingEditState) reset() {
	s.entries = nil
	s.warned = false
}

func (s *diminishingEditState) recordEdit(toolName string, size int, filePath string) {
	if size < 0 {
		size = 0
	}
	s.entries = append(s.entries, editSizeEntry{
		toolName: toolName,
		size:     size,
		filePath: filePath,
	})

	if len(s.entries) > diminishingWindow {
		s.entries = s.entries[len(s.entries)-diminishingWindow:]
	}
}

func (s *diminishingEditState) check() string {
	if s.warned {
		return ""
	}
	n := len(s.entries)
	if n < diminishingMinEdits {
		return ""
	}

	mid := n / 2
	if mid < 1 {
		return ""
	}
	earlier := s.entries[:mid]
	recent := s.entries[mid:]

	avgEarlier := avgEditSize(earlier)
	avgRecent := avgEditSize(recent)

	if avgEarlier < diminishingTrivialSize*2 {
		return ""
	}

	ratio := 1.0
	if avgEarlier > 0 {
		ratio = float64(avgRecent) / float64(avgEarlier)
	}

	hasTrivial := false
	for _, e := range recent {
		if e.size <= diminishingTrivialSize {
			hasTrivial = true
			break
		}
	}

	if ratio > diminishingShrinkRatio || !hasTrivial {
		return ""
	}

	s.warned = true
	debug.Log("diminishing_edit", "polish spiral: avg earlier=%d bytes, avg recent=%d bytes (ratio=%.2f), trivial edits present",
		avgEarlier, avgRecent, ratio)

	return fmt.Sprintf("%s%d%s%d%s%.0f%s",
		"Diminishing returns: Your recent edits (avg ", avgRecent, " bytes) are much smaller than earlier edits (avg ", avgEarlier, " bytes) -- a ", (1.0-ratio)*100, "%% decline. "+
			"At least one recent edit is trivially small (whitespace, comments, or minor renames). "+
			"This is a polish spiral: each edit adds less value than the last while consuming context and iterations. "+
			"If the core task is complete, stop editing and summarize what was done. "+
			"If substantive work remains, refocus on the highest-impact change rather than incremental polish.")
}

func avgEditSize(entries []editSizeEntry) int {
	if len(entries) == 0 {
		return 0
	}
	total := 0
	for _, e := range entries {
		total += e.size
	}
	return total / len(entries)
}

func editChangeDistance(oldText, newText string) int {

	n, m := len(oldText), len(newText)
	maxLen := n
	if m > maxLen {
		maxLen = m
	}
	minLen := n
	if m < minLen {
		minLen = m
	}
	p := 0
	for p < minLen && oldText[p] == newText[p] {
		p++
	}
	s := 0
	for s < minLen-p && oldText[n-1-s] == newText[m-1-s] {
		s++
	}
	oldRem := n - p - s
	newRem := m - p - s
	if oldRem < 0 {
		oldRem = 0
	}
	if newRem < 0 {
		newRem = 0
	}
	if oldRem > newRem {
		return oldRem
	}
	return newRem
}

func measureEditSize(toolName string, args json.RawMessage) int {
	if len(args) == 0 {
		return 0
	}

	switch toolName {
	case "edit_file":
		var p struct {
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		}
		if json.Unmarshal(args, &p) != nil {
			return 0
		}

		return editChangeDistance(p.OldText, p.NewText)

	case "multi_edit_file":
		var p struct {
			Edits []struct {
				OldText string `json:"old_text"`
				NewText string `json:"new_text"`
			} `json:"edits"`
		}
		if json.Unmarshal(args, &p) != nil {
			return 0
		}
		total := 0
		for _, e := range p.Edits {
			total += editChangeDistance(e.OldText, e.NewText)
		}
		return total

	case "multi_file_edit":
		var p struct {
			Files []struct {
				Edits []struct {
					OldText string `json:"old_text"`
					NewText string `json:"new_text"`
				} `json:"edits"`
			} `json:"files"`
		}
		if json.Unmarshal(args, &p) != nil {
			return 0
		}
		total := 0
		for _, f := range p.Files {
			for _, e := range f.Edits {
				total += editChangeDistance(e.OldText, e.NewText)
			}
		}
		return total

	case "write_file":
		var p struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(args, &p) != nil {
			return 0
		}
		return len(p.Content)

	case "multi_file_write":
		var p struct {
			Files []struct {
				Content string `json:"content"`
			} `json:"files"`
		}
		if json.Unmarshal(args, &p) != nil {
			return 0
		}
		total := 0
		for _, f := range p.Files {
			total += len(f.Content)
		}
		return total

	default:
		return 0
	}
}

func (a *Agent) diminishingRecordEdit(toolName string, args []byte) {
	if a.diminishingEdit == nil {
		return
	}
	if !productiveEditTools[toolName] {
		return
	}
	size := measureEditSize(toolName, json.RawMessage(args))
	filePath := firstEditFilePath(toolName, args)
	a.diminishingEdit.recordEdit(toolName, size, filePath)
}

func (a *Agent) diminishingCheck() string {
	if a.diminishingEdit == nil {
		return ""
	}
	return a.diminishingEdit.check()
}

func (a *Agent) resetDiminishingEdit() {
	if a.diminishingEdit != nil {
		a.diminishingEdit.reset()
	}
}

func firstEditFilePath(toolName string, args []byte) string {
	if len(args) == 0 {
		return ""
	}
	switch toolName {
	case "edit_file", "write_file":
		var p struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(args, &p) == nil {
			return p.Path
		}
	case "multi_edit_file":
		var p struct {
			Path string `json:"file_path"`
		}
		if json.Unmarshal(args, &p) == nil {
			return p.Path
		}
	case "multi_file_edit":
		var p struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if json.Unmarshal(args, &p) == nil && len(p.Files) > 0 {
			return p.Files[0].Path
		}
	}
	return ""
}
