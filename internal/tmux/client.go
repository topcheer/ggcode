package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Environment describes the current tmux attachment.
type Environment struct {
	Available bool
	InTmux    bool
	Version   string
	Session   string
	Window    string
	WindowID  string
	PaneID    string
	PaneIndex string
	ClientTTY string
}

func (e *Environment) Label() string {
	if e == nil || !e.InTmux {
		return ""
	}
	if e.Session == "" && e.Window == "" && e.PaneID == "" {
		return "tmux"
	}
	parts := []string{"tmux"}
	if e.Session != "" || e.Window != "" {
		label := e.Session
		if e.Window != "" {
			if label != "" {
				label += ":"
			}
			label += e.Window
		}
		parts = append(parts, label)
	}
	if e.PaneID != "" {
		parts = append(parts, e.PaneID)
	}
	return strings.Join(parts, " ")
}

// Pane is a tmux pane known to ggcode.
type Pane struct {
	ID        string
	Purpose   string
	Command   string
	Workspace string
	Alive     bool
	CreatedAt time.Time
}

type SplitRequest struct {
	Workspace  string
	Command    string
	Purpose    string
	Horizontal bool
	Size       string
}

type PopupRequest struct {
	Workspace string
	Command   string
	Width     string
	Height    string
}

// Client wraps tmux CLI operations. Use exec.Command args directly; do not shell-quote tmux args.
type Client struct {
	bin string
}

func NewClient() *Client { return &Client{bin: "tmux"} }

func (c *Client) Detect(ctx context.Context) (*Environment, error) {
	if c == nil {
		c = NewClient()
	}
	env := &Environment{InTmux: os.Getenv("TMUX") != ""}
	if _, err := exec.LookPath(c.bin); err != nil {
		return env, err
	}
	env.Available = true
	version, _ := c.output(ctx, "-V")
	env.Version = strings.TrimSpace(version)
	if !env.InTmux {
		return env, nil
	}
	format := strings.Join([]string{"#{session_name}", "#{window_index}:#{window_name}", "#{window_id}", "#{pane_id}", "#{pane_index}", "#{client_tty}"}, "\t")
	out, err := c.output(ctx, "display-message", "-p", format)
	if err != nil {
		return env, err
	}
	fields := strings.Split(strings.TrimSpace(out), "\t")
	if len(fields) > 0 {
		env.Session = fields[0]
	}
	if len(fields) > 1 {
		env.Window = fields[1]
	}
	if len(fields) > 2 {
		env.WindowID = fields[2]
	}
	if len(fields) > 3 {
		env.PaneID = fields[3]
	}
	if len(fields) > 4 {
		env.PaneIndex = fields[4]
	}
	if len(fields) > 5 {
		env.ClientTTY = fields[5]
	}
	return env, nil
}

func (c *Client) Split(ctx context.Context, req SplitRequest) (*Pane, error) {
	if c == nil {
		c = NewClient()
	}
	if strings.TrimSpace(req.Workspace) == "" {
		return nil, errors.New("workspace is required")
	}
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		cmd = os.Getenv("SHELL")
		if cmd == "" {
			cmd = "/bin/sh"
		}
	}
	args := []string{"split-window"}
	if req.Horizontal {
		args = append(args, "-h")
	} else {
		args = append(args, "-v")
	}
	if req.Size != "" {
		args = append(args, "-l", req.Size)
	}
	args = append(args, "-c", req.Workspace, "-P", "-F", "#{pane_id}")
	if req.Command != "" {
		args = append(args, shellCommand(cmd))
	} else {
		args = append(args, cmd)
	}
	out, err := c.output(ctx, args...)
	if err != nil {
		return nil, err
	}
	return &Pane{ID: strings.TrimSpace(out), Purpose: req.Purpose, Command: cmd, Workspace: req.Workspace, Alive: true, CreatedAt: time.Now()}, nil
}

func (c *Client) Popup(ctx context.Context, req PopupRequest) error {
	if c == nil {
		c = NewClient()
	}
	if strings.TrimSpace(req.Workspace) == "" {
		return errors.New("workspace is required")
	}
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		cmd = os.Getenv("SHELL")
		if cmd == "" {
			cmd = "/bin/sh"
		}
	}
	width := req.Width
	if width == "" {
		width = "90%"
	}
	height := req.Height
	if height == "" {
		height = "80%"
	}
	args := []string{"display-popup", "-E", "-w", width, "-h", height, "-d", req.Workspace}
	if req.Command != "" {
		args = append(args, shellCommand(cmd))
	} else {
		args = append(args, cmd)
	}
	_, err := c.output(ctx, args...)
	return err
}

func (c *Client) Focus(ctx context.Context, paneID string) error {
	_, err := c.output(ctx, "select-pane", "-t", paneID)
	return err
}

func (c *Client) Capture(ctx context.Context, paneID string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}
	return c.output(ctx, "capture-pane", "-p", "-t", paneID, "-S", fmt.Sprintf("-%d", lines))
}

func (c *Client) PaneExists(ctx context.Context, paneID string) bool {
	if strings.TrimSpace(paneID) == "" {
		return false
	}
	_, err := c.output(ctx, "display-message", "-p", "-t", paneID, "#{pane_id}")
	return err == nil
}

func (c *Client) KillPane(ctx context.Context, paneID string) error {
	if strings.TrimSpace(paneID) == "" {
		return errors.New("pane id is required")
	}
	_, err := c.output(ctx, "kill-pane", "-t", paneID)
	return err
}

func (c *Client) ListPaneIDs(ctx context.Context) (map[string]struct{}, error) {
	out, err := c.output(ctx, "list-panes", "-F", "#{pane_id}")
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{})
	for _, line := range strings.Split(out, "\n") {
		id := strings.TrimSpace(line)
		if id != "" {
			ids[id] = struct{}{}
		}
	}
	return ids, nil
}

func (c *Client) output(ctx context.Context, args ...string) (string, error) {
	if c == nil {
		c = NewClient()
	}
	cmd := exec.CommandContext(ctx, c.bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func shellCommand(cmd string) string {
	return shellPath() + " -lc " + strconv.Quote(cmd)
}

func shellPath() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}
