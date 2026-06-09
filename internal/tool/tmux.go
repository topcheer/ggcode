package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/tmux"
)

// TmuxTool lets the agent manage tmux panes when ggcode is running inside tmux.
type TmuxTool struct {
	WorkingDir string
	Client     *tmux.Client

	mu    sync.Mutex
	panes map[string]tmux.Pane
}

func NewTmuxTool(workingDir string) *TmuxTool {
	return &TmuxTool{WorkingDir: workingDir, Client: tmux.NewClient(), panes: make(map[string]tmux.Pane)}
}

func (t *TmuxTool) Clone() Tool {
	if t == nil {
		return NewTmuxTool("")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	panes := make(map[string]tmux.Pane, len(t.panes))
	for id, pane := range t.panes {
		panes[id] = pane
	}
	return &TmuxTool{WorkingDir: t.WorkingDir, Client: t.Client, panes: panes}
}

func (t *TmuxTool) Name() string { return "tmux" }

func (t *TmuxTool) Description() string {
	return "Manage tmux panes from inside a tmux session: inspect status, create panes, capture pane output, refresh managed pane state, focus panes, and close panes. Use this when the user asks to run or inspect work in tmux rather than ordinary shell commands."
}

func (t *TmuxTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["status", "split", "popup", "list", "refresh", "capture", "focus", "close"],
				"description": "tmux action to perform. Use capture to read output from a pane; refresh before relying on stale managed pane state."
			},
			"command": {
				"type": "string",
				"description": "Command to run for split or popup. Omit for an interactive shell."
			},
			"pane_id": {
				"type": "string",
				"description": "Target tmux pane id for capture, focus, or close (for example %3)."
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
			"height": {
				"type": "string",
				"description": "For popup: popup height. Defaults to 80%."
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
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t == nil {
		t = NewTmuxTool("")
	}
	if t.Client == nil {
		t.Client = tmux.NewClient()
	}
	if t.panes == nil {
		t.panes = make(map[string]tmux.Pane)
	}

	action := strings.ToLower(strings.TrimSpace(args.Action))
	if action == "" {
		return Result{IsError: true, Content: "action is required"}, nil
	}

	env, err := t.Client.Detect(ctx)
	if action == "status" {
		return t.statusResult(env, err), nil
	}
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux detect failed: %v", err)}, nil
	}
	if env == nil || !env.Available || !env.InTmux {
		return Result{IsError: true, Content: "tmux is not available in this terminal session"}, nil
	}

	switch action {
	case "split":
		return t.executeSplit(ctx, args.Command, args.Purpose, args.Horizontal, args.Size), nil
	case "popup":
		return t.executePopup(ctx, args.Command, args.Width, args.Height), nil
	case "list":
		return Result{Content: t.managedPaneText()}, nil
	case "refresh":
		return t.executeRefresh(ctx), nil
	case "capture":
		return t.executeCapture(ctx, args.PaneID, args.Lines), nil
	case "focus":
		return t.executeFocus(ctx, args.PaneID), nil
	case "close":
		return t.executeClose(ctx, args.PaneID), nil
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unsupported tmux action %q", args.Action)}, nil
	}
}

func (t *TmuxTool) statusResult(env *tmux.Environment, detectErr error) Result {
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
	t.mu.Lock()
	managed := len(t.panes)
	t.mu.Unlock()
	return Result{Content: fmt.Sprintf("tmux: %s\nversion: %s\nworkspace: %s\nmanaged panes: %d", env.Label(), env.Version, t.workspace(), managed)}
}

func (t *TmuxTool) executeSplit(ctx context.Context, command, purpose string, horizontal *bool, size string) Result {
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
	pane, err := t.Client.Split(ctx, tmux.SplitRequest{
		Workspace:  t.workspace(),
		Command:    command,
		Purpose:    purpose,
		Horizontal: isHorizontal,
		Size:       size,
	})
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux split failed: %v", err)}
	}
	t.mu.Lock()
	t.panes[pane.ID] = *pane
	t.mu.Unlock()
	return Result{Content: fmt.Sprintf("tmux pane created: %s (%s)", pane.ID, purpose)}
}

func (t *TmuxTool) executePopup(ctx context.Context, command, width, height string) Result {
	if err := t.Client.Popup(ctx, tmux.PopupRequest{Workspace: t.workspace(), Command: command, Width: width, Height: height}); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux popup failed: %v", err)}
	}
	return Result{Content: "tmux popup opened"}
}

func (t *TmuxTool) executeRefresh(ctx context.Context) Result {
	t.mu.Lock()
	if len(t.panes) == 0 {
		t.mu.Unlock()
		return Result{Content: "tmux managed panes: none"}
	}
	t.mu.Unlock()
	aliveIDs, err := t.Client.ListPaneIDs(ctx)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux refresh failed: %v", err)}
	}
	alive, stale := t.updateAliveState(aliveIDs)
	return Result{Content: fmt.Sprintf("tmux panes refreshed: %d alive, %d stale\n%s", alive, stale, t.managedPaneText())}
}

func (t *TmuxTool) executeCapture(ctx context.Context, paneID string, lines int) Result {
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return Result{IsError: true, Content: "pane_id is required for capture"}
	}
	if lines <= 0 {
		lines = 200
	}
	out, err := t.Client.Capture(ctx, paneID, lines)
	if err != nil {
		t.markPaneAlive(paneID, false)
		return Result{IsError: true, Content: fmt.Sprintf("tmux capture failed: %v", err)}
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		out = "(no output)"
	}
	t.markPaneAlive(paneID, true)
	return Result{Content: fmt.Sprintf("tmux capture %s (last %d lines):\n%s", paneID, lines, out)}
}

func (t *TmuxTool) executeFocus(ctx context.Context, paneID string) Result {
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return Result{IsError: true, Content: "pane_id is required for focus"}
	}
	if err := t.Client.Focus(ctx, paneID); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tmux focus failed: %v", err)}
	}
	t.markPaneAlive(paneID, true)
	return Result{Content: fmt.Sprintf("tmux focused pane: %s", paneID)}
}

func (t *TmuxTool) executeClose(ctx context.Context, paneID string) Result {
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return Result{IsError: true, Content: "pane_id is required for close"}
	}
	if err := t.Client.KillPane(ctx, paneID); err != nil {
		t.markPaneAlive(paneID, false)
		return Result{IsError: true, Content: fmt.Sprintf("tmux close failed: %v", err)}
	}
	t.mu.Lock()
	delete(t.panes, paneID)
	t.mu.Unlock()
	return Result{Content: fmt.Sprintf("tmux pane closed: %s", paneID)}
}

func (t *TmuxTool) managedPaneText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.panes) == 0 {
		return "tmux managed panes: none"
	}
	var b strings.Builder
	b.WriteString("tmux managed panes:\n")
	for _, pane := range t.panes {
		state := "stale"
		if pane.Alive {
			state = "alive"
		}
		b.WriteString(fmt.Sprintf("- %s [%s/%s] %s\n", pane.ID, pane.Purpose, state, pane.Command))
	}
	return strings.TrimSpace(b.String())
}

func (t *TmuxTool) updateAliveState(aliveIDs map[string]struct{}) (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	alive := 0
	stale := 0
	for id, pane := range t.panes {
		_, ok := aliveIDs[id]
		pane.Alive = ok
		t.panes[id] = pane
		if ok {
			alive++
		} else {
			stale++
		}
	}
	return alive, stale
}

func (t *TmuxTool) markPaneAlive(paneID string, alive bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pane, ok := t.panes[paneID]; ok {
		pane.Alive = alive
		t.panes[paneID] = pane
	}
}

func (t *TmuxTool) workspace() string {
	if strings.TrimSpace(t.WorkingDir) != "" {
		return t.WorkingDir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
