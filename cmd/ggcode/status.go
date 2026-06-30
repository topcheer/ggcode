package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/runfile"
)

// newStatusCmd creates the "ggcode status" subcommand for discovering and
// querying running ggcode instances from external processes.
//
// Usage:
//
//	ggcode status              — list all running instances (default: list)
//	ggcode status list         — same as above
//	ggcode status get [workspace] — detailed status for a specific instance
//	ggcode status list --agent — show only agent busy/idle state
//	ggcode status list --im    — show only IM adapter status
//	ggcode status list --mobile — show only mobile connection status
//	ggcode status list --json  — machine-readable JSON output
func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show running ggcode instances and their status",
		Long: `Discover running ggcode instances on this machine and query their runtime state.

Each running instance writes a port file to ~/.ggcode/run/<hash>.json containing
its WebUI address, auth token, and PID. This command reads those files and queries
the /api/status endpoint for live state.

Examples:
  ggcode status                  List all running instances
  ggcode status list             Same as above
  ggcode status list --agent     Show agent busy/idle for all instances
  ggcode status list --im        Show IM adapter status
  ggcode status list --mobile    Show mobile connection status
  ggcode status list --json      JSON output for scripting
  ggcode status get              Get current workspace's status (detailed)
  ggcode status get /path/to/ws  Get specific workspace's status (detailed)`,
	}

	var flagAgent bool
	var flagIM bool
	var flagMobile bool
	var flagJSON bool

	cmd.PersistentFlags().BoolVar(&flagAgent, "agent", false, "show only agent busy/idle state")
	cmd.PersistentFlags().BoolVar(&flagIM, "im", false, "show only IM adapter status")
	cmd.PersistentFlags().BoolVar(&flagMobile, "mobile", false, "show only mobile connection status")
	cmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "output in JSON format")

	cmd.AddCommand(newStatusListCmd(&flagAgent, &flagIM, &flagMobile, &flagJSON))
	cmd.AddCommand(newStatusGetCmd(&flagJSON))

	// Default action when no subcommand: run list
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runStatusList(cmd.OutOrStdout(), flagAgent, flagIM, flagMobile, flagJSON)
	}

	return cmd
}

func newStatusListCmd(flagAgent, flagIM, flagMobile, flagJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all running ggcode instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatusList(cmd.OutOrStdout(), *flagAgent, *flagIM, *flagMobile, *flagJSON)
		},
	}
}

func newStatusGetCmd(flagJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "get [workspace]",
		Short: "Get detailed status for a specific workspace",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workspace := ""
			if len(args) > 0 {
				workspace = args[0]
			} else {
				var err error
				workspace, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("get current directory: %w", err)
				}
			}
			return runStatusGet(cmd.OutOrStdout(), workspace, *flagJSON)
		},
	}
}

// instanceStatus holds a port file plus its live status (or error).
type instanceStatus struct {
	Port   runfile.PortFile   `json:"port_file"`
	Status *runtimeStatusJSON `json:"status,omitempty"`
	Error  string             `json:"error,omitempty"`
}

// runStatusList discovers all running instances and prints their status.
func runStatusList(out io.Writer, filterAgent, filterIM, filterMobile, jsonOut bool) error {
	instances, err := runfile.ReadAll()
	if err != nil {
		// No run directory or no instances — not an error
		if jsonOut {
			fmt.Fprintln(out, "[]")
		} else {
			fmt.Fprintln(out, "No running ggcode instances found.")
		}
		return nil
	}

	// Query each instance's /api/status for live state
	var results []instanceStatus
	for _, pf := range instances {
		is := instanceStatus{Port: pf}
		status, err := fetchStatus(pf.Addr, pf.Token)
		if err != nil {
			is.Error = err.Error()
		} else {
			is.Status = status
		}
		results = append(results, is)
	}

	if jsonOut {
		if results == nil {
			results = []instanceStatus{}
		}
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Fprintln(out, string(data))
		return nil
	}

	if len(results) == 0 {
		fmt.Fprintln(out, "No running ggcode instances found.")
		return nil
	}

	// Determine output mode
	if filterAgent {
		printAgentStatus(out, results)
	} else if filterIM {
		printIMStatus(out, results)
	} else if filterMobile {
		printMobileStatus(out, results)
	} else {
		printFullStatus(out, results)
	}
	return nil
}

// runStatusGet fetches and prints detailed status for one workspace.
// If the workspace has multiple running sessions, all are listed.
func runStatusGet(out io.Writer, workspace string, jsonOut bool) error {
	instances, err := runfile.ReadForWorkspace(workspace)
	if err != nil || len(instances) == 0 {
		return fmt.Errorf("no running instance for workspace %q", workspace)
	}

	if jsonOut {
		type result struct {
			PortFile runfile.PortFile   `json:"port_file"`
			Status   *runtimeStatusJSON `json:"status,omitempty"`
			Error    string             `json:"error,omitempty"`
		}
		var results []result
		for _, pf := range instances {
			r := result{PortFile: pf}
			st, err := fetchStatus(pf.Addr, pf.Token)
			if err != nil {
				r.Error = err.Error()
			} else {
				r.Status = st
			}
			results = append(results, r)
		}
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Fprintln(out, string(data))
		return nil
	}

	for i, pf := range instances {
		if i > 0 {
			fmt.Fprintln(out)
			fmt.Fprintln(out, "---")
			fmt.Fprintln(out)
		}
		status, err := fetchStatus(pf.Addr, pf.Token)
		if err != nil {
			fmt.Fprintf(out, "Workspace:       %s\n", pf.Workspace)
			fmt.Fprintf(out, "Session:         %s\n", truncate(pf.SessionID, 16))
			fmt.Fprintf(out, "PID:             %d\n", pf.PID)
			fmt.Fprintf(out, "Error:           %v\n", err)
			continue
		}
		printDetailedStatus(out, &pf, status)
	}
	return nil
}

// runtimeStatusJSON mirrors webui.RuntimeStatus for JSON unmarshaling.
type runtimeStatusJSON struct {
	PID            int             `json:"pid"`
	Workspace      string          `json:"workspace"`
	AgentBusy      bool            `json:"agent_busy"`
	PermissionMode string          `json:"permission_mode"`
	Vendor         string          `json:"vendor"`
	Endpoint       string          `json:"endpoint"`
	Model          string          `json:"model"`
	Language       string          `json:"language"`
	IMAdapters     []imAdapterJSON `json:"im_adapters"`
	MobileConn     mobileConnJSON  `json:"mobile"`
}

type imAdapterJSON struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Online  bool   `json:"online"`
	Muted   bool   `json:"muted"`
	Channel string `json:"channel,omitempty"`
}

type mobileConnJSON struct {
	Connected   bool   `json:"connected"`
	RelayURL    string `json:"relay_url,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	ConnectCode string `json:"connect_code,omitempty"`
}

// fetchStatus queries the /api/status endpoint of a running instance.
func fetchStatus(addr, token string) (*runtimeStatusJSON, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	url := "http://" + addr + "/api/status"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var st runtimeStatusJSON
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &st, nil
}

// --- Printers ---

func printFullStatus(out io.Writer, results []instanceStatus) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PID\tWORKSPACE\tSESSION\tMODE\tAGENT\tIM\tMOBILE\tMODEL")
	for _, r := range results {
		ws := shortPath(r.Port.Workspace)
		sess := truncate(r.Port.SessionID, 8)
		mode := r.Port.Mode
		agent := "?"
		imCount := "?"
		mobile := "?"
		model := ""

		if r.Status != nil {
			agent = busyIcon(r.Status.AgentBusy)
			imCount = fmt.Sprintf("%d", len(r.Status.IMAdapters))
			if anyIMOnline(r.Status.IMAdapters) {
				imCount += " (online)"
			}
			mobile = connIcon(r.Status.MobileConn.Connected)
			mode = r.Status.PermissionMode
			model = r.Status.Model
		} else if r.Error != "" {
			agent = "err"
		}

		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Port.PID, ws, sess, mode, agent, imCount, mobile, model)
	}
	w.Flush()
}

func printAgentStatus(out io.Writer, results []instanceStatus) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PID\tWORKSPACE\tSESSION\tAGENT\tMODE\tMODEL")
	for _, r := range results {
		ws := shortPath(r.Port.Workspace)
		sess := truncate(r.Port.SessionID, 8)
		agent := "?"
		mode := r.Port.Mode
		model := ""
		if r.Status != nil {
			agent = busyLabel(r.Status.AgentBusy)
			mode = r.Status.PermissionMode
			model = r.Status.Model
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", r.Port.PID, ws, sess, agent, mode, model)
	}
	w.Flush()
}

func printIMStatus(out io.Writer, results []instanceStatus) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PID\tWORKSPACE\tSESSION\tADAPTER\tTYPE\tONLINE\tCHANNEL")
	for _, r := range results {
		ws := shortPath(r.Port.Workspace)
		sess := truncate(r.Port.SessionID, 8)
		if r.Status != nil && len(r.Status.IMAdapters) > 0 {
			for _, a := range r.Status.IMAdapters {
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
					r.Port.PID, ws, sess, a.Name, a.Type, onlineIcon(a.Online), a.Channel)
			}
		} else if r.Status != nil {
			fmt.Fprintf(w, "%d\t%s\t%s\t(none)\t-\t-\t-\n", r.Port.PID, ws, sess)
		}
	}
	w.Flush()
}

func printMobileStatus(out io.Writer, results []instanceStatus) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PID\tWORKSPACE\tSESSION\tCONNECTED\tSESSION_ID\tURL")
	for _, r := range results {
		ws := shortPath(r.Port.Workspace)
		sess := truncate(r.Port.SessionID, 8)
		conn := "no"
		sid := "-"
		url := "-"
		if r.Status != nil {
			if r.Status.MobileConn.Connected {
				conn = "yes"
			}
			sid = truncate(r.Status.MobileConn.SessionID, 20)
			url = truncate(r.Status.MobileConn.RelayURL, 40)
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", r.Port.PID, ws, sess, conn, sid, url)
	}
	w.Flush()
}

func printDetailedStatus(out io.Writer, pf *runfile.PortFile, st *runtimeStatusJSON) {
	fmt.Fprintf(out, "Workspace:       %s\n", pf.Workspace)
	fmt.Fprintf(out, "Session:         %s\n", pf.SessionID)
	fmt.Fprintf(out, "PID:             %d\n", pf.PID)
	fmt.Fprintf(out, "WebUI:           http://%s\n", pf.Addr)
	fmt.Fprintf(out, "Mode:            %s\n", defaultStr(st.PermissionMode, pf.Mode))
	fmt.Fprintf(out, "\n")

	fmt.Fprintf(out, "Agent:\n")
	fmt.Fprintf(out, "  Busy:          %s\n", busyLabel(st.AgentBusy))
	fmt.Fprintf(out, "  Vendor:        %s\n", st.Vendor)
	fmt.Fprintf(out, "  Endpoint:      %s\n", st.Endpoint)
	fmt.Fprintf(out, "  Model:         %s\n", st.Model)
	fmt.Fprintf(out, "  Language:      %s\n", st.Language)
	fmt.Fprintf(out, "\n")

	fmt.Fprintf(out, "IM Adapters:\n")
	if len(st.IMAdapters) == 0 {
		fmt.Fprintf(out, "  (none)\n")
	} else {
		for _, a := range st.IMAdapters {
			status := "offline"
			if a.Online {
				status = "online"
			}
			if a.Muted {
				status += " (muted)"
			}
			fmt.Fprintf(out, "  %s [%s]: %s", a.Name, a.Type, status)
			if a.Channel != "" {
				fmt.Fprintf(out, " — %s", a.Channel)
			}
			fmt.Fprintln(out)
		}
	}
	fmt.Fprintf(out, "\n")

	fmt.Fprintf(out, "Mobile:\n")
	if st.MobileConn.Connected {
		fmt.Fprintf(out, "  Connected:     yes\n")
		if st.MobileConn.SessionID != "" {
			fmt.Fprintf(out, "  Session ID:    %s\n", st.MobileConn.SessionID)
		}
		if st.MobileConn.RelayURL != "" {
			fmt.Fprintf(out, "  Relay URL:     %s\n", st.MobileConn.RelayURL)
		}
		if st.MobileConn.ConnectCode != "" {
			fmt.Fprintf(out, "  Connect Code:  %s\n", st.MobileConn.ConnectCode)
		}
	} else {
		fmt.Fprintf(out, "  Connected:     no\n")
	}
}

// --- Helpers ---

func busyIcon(busy bool) string {
	if busy {
		return "busy"
	}
	return "idle"
}

func busyLabel(busy bool) string {
	if busy {
		return "busy (working)"
	}
	return "idle"
}

func onlineIcon(online bool) string {
	if online {
		return "online"
	}
	return "offline"
}

func connIcon(connected bool) string {
	if connected {
		return "connected"
	}
	return "—"
}

func anyIMOnline(adapters []imAdapterJSON) bool {
	for _, a := range adapters {
		if a.Online {
			return true
		}
	}
	return false
}

func defaultStr(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
