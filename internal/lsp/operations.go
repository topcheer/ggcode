package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type WorkspaceSymbol struct {
	Name string
	Kind int
	Path string
	Range
}

type TextEdit struct {
	Range   Range
	NewText string
}

type FileEdit struct {
	Path  string
	Edits []TextEdit
}

type CodeAction struct {
	Title   string
	Kind    string
	Command string
	Edits   []FileEdit
}

type rawTextEdit struct {
	Range   rawRange `json:"range"`
	NewText string   `json:"newText"`
}

type rawWorkspaceEdit struct {
	Changes         map[string][]rawTextEdit `json:"changes"`
	DocumentChanges []struct {
		// #1588-B: rename/delete entries carry NO edits key - the typed
		// struct had no kind field, so those entries silently parsed to
		// empty Edits and the loop skipped them, leaving callers with
		// "No rename edits returned" that masked "the server returned an
		// unsupported change form" (typescript-language-server
		// Move-to-file class). Track kind so unsupported forms surface.
		Kind         string `json:"kind"`
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		// #1769: rename/create/delete carry the TARGET uri at the TOP level
		// (LSP WorkspaceEdit_structure) - without these the unsupported-kind
		// note could not say which file was involved.
		URI    string        `json:"uri"`
		OldURI string        `json:"oldUri"`
		NewURI string        `json:"newUri"`
		Edits  []rawTextEdit `json:"edits"`
	} `json:"documentChanges"`
}

func WorkspaceSymbols(ctx context.Context, workspace, query string) ([]WorkspaceSymbol, error) {
	resolved, ok := ResolveServerForWorkspace(workspace)
	if !ok {
		return nil, fmt.Errorf("no supported LSP server detected for workspace %s", workspace)
	}
	session, err := globalSessions.acquire(ctx, workspace, resolved)
	if err != nil {
		return nil, err
	}
	session.opMu.Lock()
	defer session.opMu.Unlock()
	var raw json.RawMessage
	if err := session.client.call(ctx, "workspace/symbol", map[string]any{"query": query}, &raw); err != nil {
		return nil, err
	}
	return parseWorkspaceSymbols(raw), nil
}

// renameResult carries a rename reply: the applied edits plus the
// unsupported-kinds note from parseWorkspaceEdit (#3037: the note travels as
// a return value; the old package-global channel raced between concurrent
// callers, letting workspace B's Take swallow workspace A's note).
type renameResult struct {
	edits []FileEdit
	note  string
}

func RenameEdits(ctx context.Context, workspace, path string, pos Position, newName string) ([]FileEdit, string, error) {
	res, err := withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) (renameResult, error) {
		var raw json.RawMessage
		if err := session.client.call(ctx, "textDocument/rename", map[string]any{
			"textDocument": map[string]any{"uri": docURI},
			"position":     toLSPPosition(pos),
			"newName":      newName,
		}, &raw); err != nil {
			return renameResult{}, err
		}
		edits, note := parseWorkspaceEdit(raw)
		return renameResult{edits: edits, note: note}, nil
	})
	if err != nil {
		return nil, "", err
	}
	return res.edits, res.note, nil
}

func CodeActions(ctx context.Context, workspace, path string, rng Range) ([]CodeAction, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]CodeAction, error) {
		diagnostics, _ := session.publishedDiagnostics(docURI)
		var raw json.RawMessage
		if err := session.client.call(ctx, "textDocument/codeAction", map[string]any{
			"textDocument": map[string]any{"uri": docURI},
			"range":        toLSPRange(rng),
			"context": map[string]any{
				"diagnostics": diagnosticsToLSP(diagnostics),
			},
		}, &raw); err != nil {
			return nil, err
		}
		return parseCodeActions(raw), nil
	})
}

func parseWorkspaceSymbols(raw json.RawMessage) []WorkspaceSymbol {
	var probe []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil || len(probe) == 0 {
		return nil
	}
	if _, ok := probe[0]["location"]; ok {
		var list []rawSymbolInformation
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil
		}
		out := make([]WorkspaceSymbol, 0, len(list))
		for _, item := range list {
			out = append(out, WorkspaceSymbol{
				Name:  item.Name,
				Kind:  item.Kind,
				Path:  uriToPath(item.Location.URI),
				Range: toRange(item.Location.Range),
			})
		}
		return out
	}
	var list []struct {
		Name     string   `json:"name"`
		Kind     int      `json:"kind"`
		Location rawRange `json:"location"`
		URI      string   `json:"uri"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil
	}
	out := make([]WorkspaceSymbol, 0, len(list))
	for _, item := range list {
		out = append(out, WorkspaceSymbol{
			Name:  item.Name,
			Kind:  item.Kind,
			Path:  uriToPath(item.URI),
			Range: toRange(item.Location),
		})
	}
	return out
}

// parseWorkspaceEdit extracts file edits from a WorkspaceEdit reply. The
// second return carries the unsupported documentChanges kinds note (#1769);
// it travels as a return value (#3037) so concurrent callers cannot swallow
// each other's notes the way the old package-global channel did.
func parseWorkspaceEdit(raw json.RawMessage) ([]FileEdit, string) {
	var edit rawWorkspaceEdit
	if err := json.Unmarshal(raw, &edit); err != nil {
		return nil, ""
	}
	grouped := make(map[string][]TextEdit)
	seen := make(map[string]struct{})
	var unsupportedWorkspaceChangeKinds []string
	appendEdit := func(path string, edit TextEdit) {
		key := path + "|" + editKey(edit)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		grouped[path] = append(grouped[path], edit)
	}
	for uri, edits := range edit.Changes {
		path := uriToPath(uri)
		for _, e := range edits {
			appendEdit(path, TextEdit{Range: toRange(e.Range), NewText: e.NewText})
		}
	}
	for _, change := range edit.DocumentChanges {
		// #1588-B: kind-bearing entries without edits (rename, delete)
		// previously parsed to empty Edits and skipped silently; surface
		// them so callers can report the unsupported change form instead
		// of a bare "no edits returned".
		if change.Kind != "" && change.Kind != "edit" && len(change.Edits) == 0 {
			// #1769: prefer the top-level uri fields (rename uses oldUri/newUri,
			// create/delete use uri) - TextDocument.URI is absent for these kinds.
			target := firstNonEmptyStr(change.NewURI, change.OldURI, change.URI, change.TextDocument.URI)
			unsupportedWorkspaceChangeKinds = append(unsupportedWorkspaceChangeKinds,
				fmt.Sprintf("%s %s", change.Kind, uriToPath(target)))
			continue
		}
		path := uriToPath(change.TextDocument.URI)
		for _, e := range change.Edits {
			appendEdit(path, TextEdit{Range: toRange(e.Range), NewText: e.NewText})
		}
	}
	paths := make([]string, 0, len(grouped))
	for path := range grouped {
		if strings.TrimSpace(path) == "" {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	out := make([]FileEdit, 0, len(paths))
	for _, path := range paths {
		out = append(out, FileEdit{Path: path, Edits: grouped[path]})
	}
	// #1588-B/#1769: make unsupported documentChanges kinds observable.
	// debug.Log alone was invisible to the agent (ring buffer; /debug only)
	// - the TypeScript Move-to-file case still got the misleading bare
	// "no edits returned". RenameEdits now surfaces the note to the caller.
	note := ""
	if len(unsupportedWorkspaceChangeKinds) > 0 {
		note = strings.Join(unsupportedWorkspaceChangeKinds, ", ")
		debug.Log("lsp", "workspace edit dropped unsupported documentChanges kinds: %s", note)
	}
	return out, note
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// editKey builds a dedup key for a TextEdit: range start/end positions plus
// the replacement text. Servers that populate both WorkspaceEdit.changes and
// WorkspaceEdit.documentChanges with the same edit must not produce duplicates.
func editKey(edit TextEdit) string {
	return fmt.Sprintf("%d:%d-%d:%d:%s",
		edit.Range.Start.Line, edit.Range.Start.Character,
		edit.Range.End.Line, edit.Range.End.Character,
		edit.NewText)
}

func parseCodeActions(raw json.RawMessage) []CodeAction {
	var list []struct {
		Title   string `json:"title"`
		Kind    string `json:"kind"`
		Command struct {
			Command string `json:"command"`
		} `json:"command"`
		Edit rawWorkspaceEdit `json:"edit"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil
	}
	out := make([]CodeAction, 0, len(list))
	for _, item := range list {
		out = append(out, CodeAction{
			Title:   item.Title,
			Kind:    item.Kind,
			Command: item.Command.Command,
			Edits:   codeActionEdits(item.Edit),
		})
	}
	return out
}

func diagnosticsToLSP(diagnostics []Diagnostic) []map[string]any {
	if len(diagnostics) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(diagnostics))
	for _, diag := range diagnostics {
		out = append(out, map[string]any{
			"range":    toLSPRange(diag.Range),
			"severity": diag.Severity,
			"message":  diag.Message,
			"source":   diag.Source,
		})
	}
	return out
}

func parseDocumentHighlights(raw json.RawMessage) []DocumentHighlight {
	var list []struct {
		Range rawRange `json:"range"`
		Kind  int      `json:"kind"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil
	}
	out := make([]DocumentHighlight, 0, len(list))
	for _, item := range list {
		out = append(out, DocumentHighlight{
			Range: toRange(item.Range),
			Kind:  item.Kind,
		})
	}
	return out
}

func toLSPRange(rng Range) map[string]any {
	return map[string]any{
		"start": toLSPPosition(rng.Start),
		"end":   toLSPPosition(rng.End),
	}
}

func mustRaw(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// codeActionEdits adapts parseWorkspaceEdit's dual return (#3037) for the
// CodeActions list builder, which only consumes the edits.
func codeActionEdits(raw rawWorkspaceEdit) []FileEdit {
	edits, _ := parseWorkspaceEdit(mustRaw(raw))
	return edits
}
