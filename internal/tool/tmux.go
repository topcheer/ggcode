package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/topcheer/ggcode/internal/tmux"
)

// TmuxTool lets the agent manage tmux panes when ggcode is running inside tmux.
type TmuxTool struct {
	WorkingDir string
	Manager    *tmux.Manager
}

func NewTmuxTool(workingDir string) *TmuxTool {
	return &TmuxTool{WorkingDir: workingDir, Manager: tmux.SharedManager(workingDir)}
}

func (t *TmuxTool) Clone() Tool {
	if t == nil {
		return NewTmuxTool("")
	}
	return &TmuxTool{WorkingDir: t.WorkingDir, Manager: t.manager()}
}

func (t *TmuxTool) Name() string { return "tmux" }

func (t *TmuxTool) Description() string {
	return "Manage tmux panes and workspace layouts from inside a tmux session. Supports status, pane creation, layout setup/save, output capture, focus, and close. Use when the user asks to run or inspect work in tmux."
}

func (t *TmuxTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["status", "split", "popup", "list", "logs", "layouts", "layout", "setup", "save_layout", "delete_layout", "rename_layout", "refresh", "restore", "rerun", "prune", "capture", "focus", "stop", "close"],
				"description": "tmux action to perform. Use logs to capture recent output from all alive managed panes; setup to create missing panes from a workspace layout; save_layout/delete_layout/rename_layout to manage layouts; capture to read output from a pane; stop to kill a pane but keep metadata for restore/rerun; close to kill and forget a pane; restore to recreate stale panes from persisted metadata; rerun to restart a managed pane command; prune to remove stale metadata."
			},
			"command": {
				"type": "string",
				"description": "Command to run for split or popup. Omit for an interactive shell."
			},
			"pane_id": {
				"type": "string",
				"description": "Target tmux pane id or managed pane purpose for capture, focus, stop, close, restore, rerun, or prune (for example %3 or test)."
			},
			"purpose": {
				"type": "string",
				"description": "Short label for a created split pane, such as shell, test, build, verify, or dev."
			},
			"horizontal": {
				"type": "boolean",
				"description": "For split: true creates a right-side split (-h); false creates a bottom split (-v). Default true."
			},
			"size": {
				"type": "string",
				"description": "For split: tmux pane size such as 35% or 20. Defaults to 35%."
			},
			"lines": {
				"type": "integer",
				"description": "For capture: number of recent lines to capture. Defaults to 200."
			},
			"width": {
				"type": "string",
				"description": "For popup: popup width. Defaults to 90%."
			},
			"layout": {
				"type": "string",
				"description": "Layout preset name for layouts/layout/setup/save_layout/delete_layout/rename_layout actions. Defaults to default."
			},
			"new_layout": {
				"type": "string",
				"description": "New layout preset name for rename_layout."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Capturing tmux pane output', '刷新 tmux 面板状态'). You MUST always provide this field."
			}
		},
		"required": ["action", "description"]
	}`)
}

func (t *TmuxTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action     string `json:"action"`
		Command    string `json:"command"`
		PaneID     string `json:"pane_id"`
		Purpose    string `json:"purpose"`
		Horizontal *bool  `json:"horizontal"`
		Size       string `json:"size"`
		Lines      int    `json:"lines"`
		Width      string `json:"width"`
		Height     string `json:"height"`
		Layout     string `json:"layout"`
		NewLayout  string `json:"new_layout"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	action := strings.ToLower(strings.TrimSpace(args.Action))
	if action == "" {
		return Result{IsError: true, Content: "action is required"}, nil
	}

	mgr := t.manager()
	env, err := mgr.Detect(ctx)
	if action == "status" {
		return t.statusResult(mgr, env, err), nil
	}
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux detect failed: %v", err)}, nil
	}
	if env == nil || !env.Available || !env.InTmux {
		return Result{IsError: true, Content: "tmux is not available in this terminal session"}, nil
	}

	switch action {
	case "split":
		return t.executeSplit(ctx, mgr, args.Command, args.Purpose, args.Horizontal, args.Size), nil
	case "popup":
		return t.executePopup(ctx, mgr, args.Command, args.Width, args.Height), nil
	case "list":
		return Result{Content: mgr.ManagedPaneText()}, nil
	case "logs":
		return t.executeLogs(ctx, mgr, args.Lines), nil
	case "layouts":
		return t.executeLayouts(mgr), nil
	case "layout":
		return t.executeLayout(mgr, args.Layout), nil
	case "setup":
		return t.executeSetup(ctx, mgr, args.Layout), nil
	case "save_layout":
		return t.executeSaveLayout(mgr, args.Layout), nil
	case "delete_layout":
		return t.executeDeleteLayout(mgr, args.Layout), nil
	case "rename_layout":
		return t.executeRenameLayout(mgr, args.Layout, args.NewLayout), nil
	case "refresh":
		return t.executeRefresh(ctx, mgr), nil
	case "restore":
		return t.executeRestore(ctx, mgr, args.PaneID, args.Purpose), nil
	case "rerun":
		return t.executeRerun(ctx, mgr, args.PaneID, args.Purpose), nil
	case "prune":
		return t.executePrune(mgr, args.PaneID, args.Purpose), nil
	case "capture":
		return t.executeCapture(ctx, mgr, args.PaneID, args.Lines), nil
	case "focus":
		return t.executeFocus(ctx, mgr, args.PaneID), nil
	case "stop":
		return t.executeStop(ctx, mgr, args.PaneID), nil
	case "close":
		return t.executeClose(ctx, mgr, args.PaneID), nil
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unsupported tmux action %q", args.Action)}, nil
	}
}

func (t *TmuxTool) statusResult(mgr *tmux.Manager, env *tmux.Environment, detectErr error) Result {
	if detectErr != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux detect failed: %v", detectErr)}
	}
	if env == nil {
		return Result{Content: "tmux: not detected"}
	}
	if !env.Available {
		return Result{Content: "tmux: command not found"}
	}
	if !env.InTmux {
		return Result{Content: fmt.Sprintf("tmux: available (%s), not inside a tmux session", env.Version)}
	}
	return Result{Content: fmt.Sprintf("tmux: %s\nversion: %s\nworkspace: %s\nmanaged panes: %d", env.Label(), env.Version, mgr.Workspace(), mgr.Count())}
}

func (t *TmuxTool) executeSplit(ctx context.Context, mgr *tmux.Manager, command, purpose string, horizontal *bool, size string) Result {
	isHorizontal := true
	if horizontal != nil {
		isHorizontal = *horizontal
	}
	if strings.TrimSpace(purpose) == "" {
		purpose = "shell"
		if strings.TrimSpace(command) != "" {
			purpose = "command"
		}
	}
	if strings.TrimSpace(size) == "" {
		size = "35%"
	}
	pane, err := mgr.Split(ctx, tmux.SplitRequest{
		Workspace:  mgr.Workspace(),
		Command:    command,
		Purpose:    purpose,
		Horizontal: isHorizontal,
		Size:       size,
	})
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux split failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("tmux pane created: %s (%s)", pane.ID, purpose)}
}

func (t *TmuxTool) executePopup(ctx context.Context, mgr *tmux.Manager, command, width, height string) Result {
	if err := mgr.Popup(ctx, tmux.PopupRequest{Workspace: mgr.Workspace(), Command: command, Width: width, Height: height}); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux popup failed: %v", err)}
	}
	return Result{Content: "tmux popup opened"}
}

func (t *TmuxTool) executeDeleteLayout(mgr *tmux.Manager, name string) Result {
	layoutName := toolTmuxLayoutName(name)
	if !mgr.DeleteLayout(layoutName) {
		return Result{IsError: true, Content: fmt.Sprintf("tmux layout %q not found", layoutName)}
	}
	return Result{Content: fmt.Sprintf("tmux layout %q deleted", layoutName)}
}

func (t *TmuxTool) executeRenameLayout(mgr *tmux.Manager, oldName, newName string) Result {
	oldLayout := toolTmuxLayoutName(oldName)
	newLayout := toolTmuxLayoutName(newName)
	if err := mgr.RenameLayout(oldLayout, newLayout); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux rename_layout failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("tmux layout %q renamed to %q", oldLayout, newLayout)}
}

func (t *TmuxTool) executeRefresh(ctx context.Context, mgr *tmux.Manager) Result {
	if !mgr.HasPanes() {
		return Result{Content: "tmux managed panes: none"}
	}
	alive, stale, err := mgr.Refresh(ctx)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux refresh failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("tmux panes refreshed: %d alive, %d stale\n%s", alive, stale, mgr.ManagedPaneText())}
}

func (t *TmuxTool) executeLogs(ctx context.Context, mgr *tmux.Manager, lines int) Result {
	if lines <= 0 {
		lines = 50
	}
	captures := mgr.CaptureAll(ctx, lines)
	return Result{Content: tmux.FormatCaptures(captures, lines)}
}

func (t *TmuxTool) executeLayouts(mgr *tmux.Manager) Result {
	names := mgr.ListLayoutNames()
	if len(names) == 0 {
		return Result{Content: "tmux layouts: none"}
	}
	return Result{Content: "tmux layouts:\n- " + strings.Join(names, "\n- ")}
}

func (t *TmuxTool) executeLayout(mgr *tmux.Manager, name string) Result {
	layoutName := toolTmuxLayoutName(name)
	layout := mgr.Layout(layoutName)
	if len(layout) == 0 {
		return Result{Content: fmt.Sprintf("tmux layout %q: empty or not found", layoutName)}
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("tmux layout %q:\n", layoutName))
	for _, pane := range layout {
		b.WriteString(fmt.Sprintf("- [%s] %s\n", pane.Purpose, pane.Command))
	}
	return Result{Content: strings.TrimSpace(b.String())}
}

func (t *TmuxTool) executeSetup(ctx context.Context, mgr *tmux.Manager, name string) Result {
	layoutName := toolTmuxLayoutName(name)
	created, err := mgr.SetupLayout(ctx, layoutName)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux setup failed: %v", err)}
	}
	if len(created) == 0 {
		return Result{Content: fmt.Sprintf("tmux setup %q: no missing panes", layoutName)}
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("tmux setup %q created panes:\n", layoutName))
	for _, pane := range created {
		b.WriteString(fmt.Sprintf("- %s [%s] %s\n", pane.ID, pane.Purpose, pane.Command))
	}
	return Result{Content: strings.TrimSpace(b.String())}
}

func (t *TmuxTool) executeSaveLayout(mgr *tmux.Manager, name string) Result {
	layoutName := toolTmuxLayoutName(name)
	if err := mgr.SaveLayout(layoutName); err != nil {
		if errors.Is(err, tmux.ErrNoAlivePanes) {
			return Result{Content: "tmux save_layout: no alive panes to save"}
		}
		return Result{IsError: true, Content: fmt.Sprintf("tmux save_layout failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("tmux layout %q saved", layoutName)}
}

func toolTmuxLayoutName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	return name
}

func (t *TmuxTool) executeRestore(ctx context.Context, mgr *tmux.Manager, paneID, purpose string) Result {
	selector := strings.TrimSpace(paneID)
	if selector == "" {
		selector = strings.TrimSpace(purpose)
	}
	results, err := mgr.Restore(ctx, selector)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux restore failed: %v", err)}
	}
	if len(results) == 0 {
		return Result{Content: "tmux restore: no matching stale panes with commands"}
	}
	var b strings.Builder
	b.WriteString("tmux restored panes:\n")
	for _, res := range results {
		b.WriteString(fmt.Sprintf("- %s -> %s [%s] %s\n", res.Old.ID, res.New.ID, res.New.Purpose, res.New.Command))
	}
	return Result{Content: strings.TrimSpace(b.String())}
}

func (t *TmuxTool) executeRerun(ctx context.Context, mgr *tmux.Manager, paneID, purpose string) Result {
	selector := strings.TrimSpace(paneID)
	if selector == "" {
		selector = strings.TrimSpace(purpose)
	}
	if selector == "" {
		return Result{IsError: true, Content: "pane_id or purpose is required for rerun"}
	}
	res, err := mgr.RerunPane(ctx, selector)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux rerun failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("tmux reran pane: %s -> %s [%s] %s", res.Old.ID, res.New.ID, res.New.Purpose, res.New.Command)}
}

func (t *TmuxTool) executePrune(mgr *tmux.Manager, paneID, purpose string) Result {
	selector := strings.TrimSpace(paneID)
	if selector == "" {
		selector = strings.TrimSpace(purpose)
	}
	removed := mgr.Prune(selector)
	if removed == 0 {
		return Result{Content: "tmux prune: no matching stale panes"}
	}
	return Result{Content: fmt.Sprintf("tmux pruned %d stale pane(s)\n%s", removed, mgr.ManagedPaneText())}
}

func (t *TmuxTool) executeCapture(ctx context.Context, mgr *tmux.Manager, selector string, lines int) Result {
	pane, ok := mgr.ResolvePaneSelector(selector)
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("tmux capture failed: no managed pane matches %q", selector)}
	}
	if lines <= 0 {
		lines = 200
	}
	out, err := mgr.Capture(ctx, pane.ID, lines)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux capture failed: %v", err)}
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		out = "(no output)"
	}
	return Result{Content: fmt.Sprintf("tmux capture %s (last %d lines):\n%s", pane.ID, lines, out)}
}

func (t *TmuxTool) executeFocus(ctx context.Context, mgr *tmux.Manager, selector string) Result {
	pane, ok := mgr.ResolvePaneSelector(selector)
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("tmux focus failed: no managed pane matches %q", selector)}
	}
	if err := mgr.Focus(ctx, pane.ID); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux focus failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("tmux focused pane: %s", pane.ID)}
}

func (t *TmuxTool) executeStop(ctx context.Context, mgr *tmux.Manager, selector string) Result {
	pane, err := mgr.StopPane(ctx, selector)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux stop failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("tmux pane stopped: %s [%s] (metadata kept)", pane.ID, pane.Purpose)}
}

func (t *TmuxTool) executeClose(ctx context.Context, mgr *tmux.Manager, selector string) Result {
	pane, ok := mgr.ResolvePaneSelector(selector)
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("tmux close failed: no managed pane matches %q", selector)}
	}
	if err := mgr.Close(ctx, pane.ID); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux close failed: %v", err)}
	}
	return Result{Content: fmt.Sprintf("tmux pane closed: %s", pane.ID)}
}

func (t *TmuxTool) manager() *tmux.Manager {
	if t == nil {
		return tmux.SharedManager("")
	}
	if t.Manager == nil {
		t.Manager = tmux.SharedManager(t.workspace())
	}
	return t.Manager
}

func (t *TmuxTool) workspace() string {
	if t != nil && strings.TrimSpace(t.WorkingDir) != "" {
		return t.WorkingDir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
