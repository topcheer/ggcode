package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Edits []rawTextEdit `json:"edits"`
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

func RenameEdits(ctx context.Context, workspace, path string, pos Position, newName string) ([]FileEdit, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]FileEdit, error) {
		var raw json.RawMessage
		if err := session.client.call(ctx, "textDocument/rename", map[string]any{
			"textDocument": map[string]any{"uri": docURI},
			"position":     toLSPPosition(pos),
			"newName":      newName,
		}, &raw); err != nil {
			return nil, err
		}
		return parseWorkspaceEdit(raw), nil
	})
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

func parseWorkspaceEdit(raw json.RawMessage) []FileEdit {
	var edit rawWorkspaceEdit
	if err := json.Unmarshal(raw, &edit); err != nil {
		return nil
	}
	grouped := make(map[string][]TextEdit)
	for uri, edits := range edit.Changes {
		path := uriToPath(uri)
		for _, e := range edits {
			grouped[path] = append(grouped[path], TextEdit{Range: toRange(e.Range), NewText: e.NewText})
		}
	}
	for _, change := range edit.DocumentChanges {
		path := uriToPath(change.TextDocument.URI)
		for _, e := range change.Edits {
			grouped[path] = append(grouped[path], TextEdit{Range: toRange(e.Range), NewText: e.NewText})
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
	return out
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
			Edits:   parseWorkspaceEdit(mustRaw(item.Edit)),
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
