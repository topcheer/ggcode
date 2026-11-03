package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

var readOnlyToolNames = map[string]bool{

	"read_file":       true,
	"multi_file_read": true,

	"search_files": true,
	"grep":         true,
	"glob":         true,
	"code_search":  true,

	"list_directory": true,

	"git_status":      true,
	"git_diff":        true,
	"git_log":         true,
	"git_show":        true,
	"git_blame":       true,
	"git_branch_list": true,
	"git_remote":      true,
	"git_stash_list":  true,

	"lsp_hover":                  true,
	"lsp_definition":             true,
	"lsp_references":             true,
	"lsp_document_highlights":    true,
	"lsp_implementation":         true,
	"lsp_workspace_symbols":      true,
	"lsp_diagnostics":            true,
	"lsp_incoming_calls":         true,
	"lsp_outgoing_calls":         true,
	"lsp_prepare_call_hierarchy": true,
	"lsp_code_actions":           true,

	"runtime":               true,
	"debug_log":             true,
	"list_agents":           true,
	"list_mcp_capabilities": true,
	"read_mcp_resource":     true,
	"task_list":             true,
	"task_get":              true,

	"web_search": true,
	"web_fetch":  true,
}

type CodeExecution struct {
	Registry *Registry
}

func (CodeExecution) Name() string { return "code_execution" }

func (CodeExecution) Description() string {
	return `Execute JavaScript code in a sandbox to batch-call read-only tools, reducing context usage for multi-step analysis.

Available tools (call via await tools.NAME(args)):
  read_file, multi_file_read, search_files, grep, glob, code_search, list_directory
  git_status, git_diff, git_log, git_show, git_blame, git_branch_list, git_remote, git_stash_list
  lsp_hover, lsp_definition, lsp_references, lsp_diagnostics, lsp_workspace_symbols, lsp_implementation, lsp_document_highlights, lsp_code_actions, lsp_incoming_calls, lsp_outgoing_calls, lsp_prepare_call_hierarchy
  web_search, web_fetch, runtime, debug_log, list_agents, list_mcp_capabilities, read_mcp_resource, task_list, task_get

MCP tools: tools from read_only MCP servers are bridged into this sandbox:
  await tools['mcp__server__tool']({...})
Discover them with JSON.parse(await tools.mcpList()) → [{name, description, sandbox}] (sandbox:true = callable here).
Fetch one schema on demand: await tools.mcpDescribe('mcp__server__tool') → {name, description, parameters}.
Large MCP results stay inside the sandbox — filter/map/reduce them in JS and console.log only summaries.
Budget: at most 30 MCP tool calls per run.

Tool results are strings — use JSON.parse() if needed. async/await supported.
console.log(...) output is returned to you.

Example:
  const r = await tools.grep({pattern: "TODO", path: "/workspace"});
  console.log(r.split("\\n").length + " matches");

Rules: Only read-only tools (MCP tools only from read_only servers). Execution timeout: 30 seconds.`
}

func (CodeExecution) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"code": {
				"type": "string",
				"description": "JavaScript code to execute. Use ` + "`await tools.NAME(args)`" + ` to call tools. Use ` + "`console.log()`" + ` to return output."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write a concise description of what this code execution does."
			}
		},
		"required": ["code", "description"]
	}`)
}

const codeExecTimeout = 30 * time.Second

const mcpToolNamePrefix = "mcp__"

const maxMCPCallsPerRun = 30

const mcpListDescMaxRunes = 120

func truncateSandboxText(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

const maxStdoutLen = 256 * 1024

const maxStdoutHardLen = 1 << 20

func (c CodeExecution) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.Code) == "" {
		return Result{IsError: true, Content: "code is empty"}, nil
	}
	if c.Registry == nil {
		return Result{IsError: true, Content: "tool registry not available"}, nil
	}

	execCtx, cancel := context.WithTimeout(ctx, codeExecTimeout)
	defer cancel()

	result, err := c.runCode(execCtx, args.Code)
	if err != nil {
		debug.Log("ptc", "execution error: %v", err)

		msg := fmt.Sprintf("Execution error: %v", err)
		if result != nil && result.stdout != "" {
			msg += fmt.Sprintf("\n\nPartial output before failure:\n%s", result.stdout)
		}
		return Result{IsError: true, Content: msg}, nil
	}

	output := result.stdout
	if len(output) > maxStdoutLen {

		output = output[:util.SnapToRuneStart(output, maxStdoutLen)] +
			fmt.Sprintf("\n... (truncated at %d bytes)", maxStdoutLen)
	}
	if output == "" {
		output = "(code executed successfully, but produced no console.log output)"
	}

	if len(result.toolCalls) > 0 {
		var logEntry strings.Builder
		logEntry.WriteString("\n\n[tool calls made by code: ")
		for i, tc := range result.toolCalls {
			if i > 0 {
				logEntry.WriteString(", ")
			}
			logEntry.WriteString(tc)
		}
		logEntry.WriteString("]")
		output += logEntry.String()
	}

	return Result{Content: output}, nil
}

type execResult struct {
	stdout    string
	toolCalls []string
}

func (c CodeExecution) runCode(ctx context.Context, code string) (*execResult, error) {
	vm := goja.New()

	var stdout strings.Builder
	var toolCalls []string
	var toolCallsMu sync.Mutex

	consoleObj := vm.NewObject()
	consoleObj.Set("log", func(call goja.FunctionCall) goja.Value {
		if writeConsoleOutput(&stdout, call.Arguments) {
			vm.Interrupt(fmt.Sprintf("console output exceeded %d bytes limit", maxStdoutHardLen))
		}
		return goja.Undefined()
	})
	consoleObj.Set("error", func(call goja.FunctionCall) goja.Value {
		if writeConsoleOutput(&stdout, call.Arguments) {
			vm.Interrupt(fmt.Sprintf("console output exceeded %d bytes limit", maxStdoutHardLen))
		}
		return goja.Undefined()
	})
	consoleObj.Set("warn", func(call goja.FunctionCall) goja.Value {
		if writeConsoleOutput(&stdout, call.Arguments) {
			vm.Interrupt(fmt.Sprintf("console output exceeded %d bytes limit", maxStdoutHardLen))
		}
		return goja.Undefined()
	})
	consoleObj.Set("info", func(call goja.FunctionCall) goja.Value {
		if writeConsoleOutput(&stdout, call.Arguments) {
			vm.Interrupt(fmt.Sprintf("console output exceeded %d bytes limit", maxStdoutHardLen))
		}
		return goja.Undefined()
	})
	vm.Set("console", consoleObj)

	toolsObj := vm.NewObject()

	injectSandboxTool := func(name string, t Tool, preCall func() error) {
		toolRef := t

		toolsObj.Set(name, func(call goja.FunctionCall) (rv goja.Value) {
			defer func() {
				if r := recover(); r != nil {
					toolCallsMu.Lock()
					toolCalls = append(toolCalls, fmt.Sprintf("%s → panic: %v", name, r))
					toolCallsMu.Unlock()
					rv = rejectPromise(vm, fmt.Errorf("%s panicked: %v", name, r))
				}
			}()
			if preCall != nil {
				if err := preCall(); err != nil {
					toolCallsMu.Lock()
					toolCalls = append(toolCalls, fmt.Sprintf("%s → rejected: %v", name, err))
					toolCallsMu.Unlock()
					return rejectPromise(vm, err)
				}
			}
			var inputJSON json.RawMessage
			if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) && !goja.IsNull(call.Arguments[0]) {

				exported := call.Arguments[0].Export()
				jsonBytes, err := json.Marshal(exported)
				if err != nil {

					return rejectPromise(vm, fmt.Errorf("invalid arguments for %s: %v", name, err))
				}
				inputJSON = jsonBytes
			} else {
				inputJSON = json.RawMessage(`{}`)
			}

			type toolOutcome struct {
				result Result
				err    error
			}
			done := make(chan toolOutcome, 1)
			safego.Go("code_execution.toolExec", func() {
				result, err := toolRef.Execute(ctx, inputJSON)
				done <- toolOutcome{result: result, err: err}
			})

			var result Result
			var err error
			select {
			case oc := <-done:
				result, err = oc.result, oc.err
			case <-ctx.Done():
				toolCallsMu.Lock()
				toolCalls = append(toolCalls, fmt.Sprintf("%s → timeout", name))
				toolCallsMu.Unlock()
				return rejectPromise(vm, fmt.Errorf("%s timed out after %v", name, codeExecTimeout))
			}
			if err != nil {
				toolCallsMu.Lock()
				toolCalls = append(toolCalls, fmt.Sprintf("%s → error: %v", name, err))
				toolCallsMu.Unlock()
				return rejectPromise(vm, fmt.Errorf("%s failed: %v", name, err))
			}

			toolCallsMu.Lock()
			if result.IsError {
				toolCalls = append(toolCalls, fmt.Sprintf("%s → error", name))
			} else {
				toolCalls = append(toolCalls, name)
			}
			toolCallsMu.Unlock()

			if result.IsError {
				return rejectPromise(vm, fmt.Errorf("%s", result.Content))
			}
			return resolvePromise(vm, result.Content)
		})
	}

	for toolName := range readOnlyToolNames {
		t, ok := c.Registry.Get(toolName)
		if !ok {
			continue
		}
		injectSandboxTool(toolName, t, nil)
	}

	mcpCalls := 0
	for _, toolName := range c.Registry.ToolNames() {
		if !strings.HasPrefix(toolName, mcpToolNamePrefix) {
			continue
		}
		t, ok := c.Registry.Get(toolName)
		if !ok {
			continue
		}
		if ss, ok := t.(SandboxSafe); !ok || !ss.SandboxSafe() {
			continue
		}
		if ac, ok := t.(AvailabilityChecker); ok && !ac.Available() {
			continue
		}
		injectSandboxTool(toolName, t, func() error {
			toolCallsMu.Lock()
			defer toolCallsMu.Unlock()
			if mcpCalls >= maxMCPCallsPerRun {
				return fmt.Errorf("MCP call budget exceeded: at most %d MCP tool calls per code_execution run", maxMCPCallsPerRun)
			}
			mcpCalls++
			return nil
		})
	}

	toolsObj.Set("mcpList", func(call goja.FunctionCall) goja.Value {
		type mcpEntry struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Sandbox     bool   `json:"sandbox"`
		}
		var list []mcpEntry
		for _, toolName := range c.Registry.ToolNames() {
			if !strings.HasPrefix(toolName, mcpToolNamePrefix) {
				continue
			}
			t, ok := c.Registry.Get(toolName)
			if !ok {
				continue
			}
			entry := mcpEntry{
				Name:        toolName,
				Description: truncateSandboxText(t.Description(), mcpListDescMaxRunes),
			}
			if ss, ok := t.(SandboxSafe); ok && ss.SandboxSafe() {
				entry.Sandbox = true
			}
			list = append(list, entry)
		}
		b, err := json.Marshal(list)
		if err != nil {
			return rejectPromise(vm, fmt.Errorf("mcpList failed: %v", err))
		}
		return resolvePromise(vm, string(b))
	})

	toolsObj.Set("mcpDescribe", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 || goja.IsUndefined(call.Arguments[0]) || goja.IsNull(call.Arguments[0]) {
			return rejectPromise(vm, fmt.Errorf("mcpDescribe requires an MCP tool name"))
		}
		name := call.Arguments[0].ToString().String()
		if !strings.HasPrefix(name, mcpToolNamePrefix) {
			return rejectPromise(vm, fmt.Errorf("%q is not an MCP tool (expected mcp__server__tool)", name))
		}
		t, ok := c.Registry.Get(name)
		if !ok {
			return rejectPromise(vm, fmt.Errorf("MCP tool %q not found in registry", name))
		}
		b, err := json.Marshal(struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}{Name: t.Name(), Description: t.Description(), Parameters: t.Parameters()})
		if err != nil {
			return rejectPromise(vm, fmt.Errorf("mcpDescribe failed: %v", err))
		}
		return resolvePromise(vm, string(b))
	})

	vm.Set("tools", toolsObj)

	safego.Go("code_execution.ctxCancel", func() {
		<-ctx.Done()
		vm.Interrupt(context.Cause(ctx))
	})

	var asyncErr string
	vm.Set("__captureError", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 {
			asyncErr = call.Arguments[0].ToString().String()
		}
		return goja.Undefined()
	})

	unhandledRejections := make(map[*goja.Promise]string)
	vm.SetPromiseRejectionTracker(func(p *goja.Promise, op goja.PromiseRejectionOperation) {
		switch op {
		case goja.PromiseRejectionReject:
			unhandledRejections[p] = rejectionReasonString(p)
		case goja.PromiseRejectionHandle:
			delete(unhandledRejections, p)
		}
	})
	wrapped := "(async function() {\n" +
		"  try {\n" + code + "\n" +
		"  } catch(e) {\n" +
		"    __captureError(e);\n" +
		"  }\n" +
		"})()"
	_, err := vm.RunString(wrapped)
	if err != nil {

		if ctx.Err() != nil {

			return &execResult{
				stdout:    stdout.String(),
				toolCalls: toolCalls,
			}, fmt.Errorf("execution timed out after %v", codeExecTimeout)
		}
		return &execResult{
			stdout:    stdout.String(),
			toolCalls: toolCalls,
		}, fmt.Errorf("%s", err.Error())
	}

	if asyncErr != "" {
		return &execResult{
			stdout:    stdout.String(),
			toolCalls: toolCalls,
		}, fmt.Errorf("%s", asyncErr)
	}

	if len(unhandledRejections) > 0 {
		msgs := make([]string, 0, len(unhandledRejections))
		for _, m := range unhandledRejections {
			msgs = append(msgs, m)
		}
		sort.Strings(msgs)
		return &execResult{
			stdout:    stdout.String(),
			toolCalls: toolCalls,
		}, fmt.Errorf("unhandled promise rejection (missing await or .catch?): %s", strings.Join(msgs, "; "))
	}

	return &execResult{
		stdout:    stdout.String(),
		toolCalls: toolCalls,
	}, nil
}

func writeConsoleOutput(stdout *strings.Builder, args []goja.Value) (overflow bool) {

	if stdout.Len() >= maxStdoutHardLen {
		return true
	}
	for i, arg := range args {
		if i > 0 {
			stdout.WriteString(" ")
		}
		if goja.IsUndefined(arg) || goja.IsNull(arg) {
			stdout.WriteString("undefined")
			continue
		}

		exported := arg.Export()
		switch v := exported.(type) {
		case string:
			stdout.WriteString(v)
		default:

			jsonBytes, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				stdout.WriteString(fmt.Sprintf("%v", v))
			} else {
				stdout.Write(jsonBytes)
			}
		}
	}
	stdout.WriteString("\n")
	return stdout.Len() >= maxStdoutHardLen
}

func rejectionReasonString(p *goja.Promise) string {
	if p == nil || p.State() != goja.PromiseStateRejected {
		return "unknown reason"
	}
	r := p.Result()
	if r == nil || goja.IsUndefined(r) || goja.IsNull(r) {
		return "unknown reason"
	}
	s := r.ToString().String()
	if s == "" {
		return "unknown reason"
	}
	const maxReasonLen = 200
	if len(s) > maxReasonLen {
		s = s[:maxReasonLen] + "...(truncated)"
	}
	return s
}

func resolvePromise(vm *goja.Runtime, value string) goja.Value {
	promise, resolve, _ := vm.NewPromise()
	resolve(value)
	return vm.ToValue(promise)
}

func rejectPromise(vm *goja.Runtime, err error) goja.Value {
	promise, _, reject := vm.NewPromise()
	reject(err.Error())
	return vm.ToValue(promise)
}
