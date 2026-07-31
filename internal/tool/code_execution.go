package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/topcheer/ggcode/internal/debug"
)

// readOnlyToolNames is the set of tools that code_execution is allowed to
// invoke from JavaScript. Only read-only tools are exposed — write operations
// (edit_file, write_file, run_command) are intentionally excluded so that
// file modifications still go through the normal per-call permission flow
// with UI diff preview and approval.
var readOnlyToolNames = map[string]bool{
	// File reads
	"read_file":       true,
	"multi_file_read": true,
	// Search
	"search_files": true,
	"grep":         true,
	"glob":         true,
	"code_search":  true, // BM25 semantic search (read-only)
	// Directory listing
	"list_directory": true,
	// Git read-only
	"git_status":      true,
	"git_diff":        true,
	"git_log":         true,
	"git_show":        true,
	"git_blame":       true,
	"git_branch_list": true,
	"git_remote":      true,
	"git_stash_list":  true,
	// LSP read-only (rename excluded — it writes)
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
	// Runtime / metadata
	"runtime":               true,
	"debug_log":             true,
	"list_agents":           true,
	"list_mcp_capabilities": true,
	"read_mcp_resource":     true,
	"task_list":             true,
	"task_get":              true,
	// Web
	"web_search": true,
	"web_fetch":  true,
}

// CodeExecution implements the code_execution tool — a sandboxed JavaScript
// runtime that can batch-call other read-only tools, dramatically reducing
// context window consumption for data-intensive multi-step operations.
//
// The model writes JavaScript code that calls tools via the `tools` object
// (e.g., `await tools.read_file({path: "/foo"})`). Only console.log output
// is returned to the model — intermediate tool results stay inside the
// sandbox and never bloat the context window.
type CodeExecution struct {
	// Registry is used at execution time to look up tools by name.
	// We store the registry reference rather than snapshots so that
	// dynamically registered tools (e.g., MCP tools promoted to the
	// read-only whitelist) are immediately callable.
	Registry *Registry
}

func (CodeExecution) Name() string { return "code_execution" }

func (CodeExecution) Description() string {
	return `Execute JavaScript code in a sandbox to batch-call read-only tools. This dramatically reduces context usage for multi-step analysis.

Available tools (call via await tools.NAME(args)):
  tools.read_file({path}) — Read file contents. Returns text.
  tools.multi_file_read({files:[{path,offset,limit}]}) — Read multiple files.
  tools.search_files({pattern, directory, include_pattern, max_results}) — Search file contents by regex.
  tools.grep({pattern, path, output_mode, type, context}) — Ripgrep search. output_mode: "content"|"files_with_matches"|"count".
  tools.glob({pattern, directory}) — Find files by glob pattern.
  tools.list_directory({path}) — List directory entries.
  tools.git_status() — Show working tree status.
  tools.git_diff({cached, file}) — Show changes.
  tools.git_log({count, path}) — Show commit history.
  tools.git_show({revision, file, stat}) — Show commit details.
  tools.git_blame({file}) — Show per-line authorship.
  tools.git_branch_list({remote}) — List branches.
  tools.git_remote({verbose}) — Show remotes.
  tools.git_stash_list() — List stashes.
  tools.web_search({query, max_results}) — Search the web.
  tools.web_fetch({url}) — Fetch a URL.
  tools.code_search({query, max_results}) — BM25 semantic code search.
  tools.lsp_hover({path, line, character}) — Symbol hover/type info.
  tools.lsp_definition({path, line, character}) — Go to definition.
  tools.lsp_references({path, line, character}) — Find references.
  tools.lsp_diagnostics({path}) — File diagnostics.
  tools.lsp_workspace_symbols({query}) — Workspace symbol search.
  tools.lsp_implementation({path, line, character}) — Find implementations.
  tools.lsp_document_highlights({path, line, character}) — Document highlights.
  tools.lsp_code_actions({path, start_line, start_character, end_line, end_character}) — Code actions.
  tools.lsp_incoming_calls({item}) — Incoming calls (from lsp_prepare_call_hierarchy).
  tools.lsp_outgoing_calls({item}) — Outgoing calls.
  tools.lsp_prepare_call_hierarchy({path, line, character}) — Prepare call hierarchy.
  tools.runtime() — Session info, model, mode.
  tools.debug_log({lines, category}) — Read debug log ring buffer.
  tools.list_agents() — List sub-agent runs.
  tools.list_mcp_capabilities({server}) — List MCP server tools/resources.
  tools.read_mcp_resource({server, uri}) — Read MCP resource.
  tools.task_list() — List session tasks.
  tools.task_get({taskId}) — Get task details.

console.log(...) output is returned to you. Tool results are strings — use JSON.parse() if you need structured data. async/await is supported.

Example:
  const results = await tools.search_files({pattern: "TODO", directory: "/workspace"});
  console.log("Found " + results.split("\\n").length + " matches");

Rules:
  - Only read-only tools are available. To edit files or run commands, use the regular tool_use format.
  - Execution timeout: 30 seconds.`
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
				"description": "REQUIRED. Brief activity label shown in the UI in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). Write a concise description of what this code execution does."
			}
		},
		"required": ["code", "description"]
	}`)
}

// codeExecTimeout is the maximum wall-clock time for a single execution.
const codeExecTimeout = 30 * time.Second

// maxStdoutLen caps the stdout capture to prevent unbounded memory if the
// model writes a tight loop with console.log.
const maxStdoutLen = 256 * 1024 // 256 KB

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
		return Result{IsError: true, Content: fmt.Sprintf("Execution error: %v", err)}, nil
	}

	output := result.stdout
	if len(output) > maxStdoutLen {
		output = output[:maxStdoutLen] + fmt.Sprintf("\n... (truncated at %d bytes)", maxStdoutLen)
	}
	if output == "" {
		output = "(code executed successfully, but produced no console.log output)"
	}

	// Append tool call log so the model knows what happened.
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

// execResult holds the output and metadata from a code execution.
type execResult struct {
	stdout    string
	toolCalls []string
}

// runCode creates a goja VM, injects tools, and executes the code.
func (c CodeExecution) runCode(ctx context.Context, code string) (*execResult, error) {
	vm := goja.New()

	var stdout strings.Builder
	var toolCalls []string
	var toolCallsMu sync.Mutex // guards toolCalls (VM runs on one goroutine, but be safe)

	// Inject console object.
	consoleObj := vm.NewObject()
	consoleObj.Set("log", func(call goja.FunctionCall) goja.Value {
		writeConsoleOutput(&stdout, call.Arguments)
		return goja.Undefined()
	})
	consoleObj.Set("error", func(call goja.FunctionCall) goja.Value {
		writeConsoleOutput(&stdout, call.Arguments)
		return goja.Undefined()
	})
	consoleObj.Set("warn", func(call goja.FunctionCall) goja.Value {
		writeConsoleOutput(&stdout, call.Arguments)
		return goja.Undefined()
	})
	consoleObj.Set("info", func(call goja.FunctionCall) goja.Value {
		writeConsoleOutput(&stdout, call.Arguments)
		return goja.Undefined()
	})
	vm.Set("console", consoleObj)

	// Inject tools object: each whitelisted tool becomes an async function.
	toolsObj := vm.NewObject()
	for toolName := range readOnlyToolNames {
		t, ok := c.Registry.Get(toolName)
		if !ok {
			continue // tool not registered (e.g., optional tools like tmux)
		}
		toolRef := t
		name := toolName

		// goja supports async functions via goja.AssertFunction.
		// We wrap each tool call as a promise-returning function.
		toolsObj.Set(name, func(call goja.FunctionCall) (rv goja.Value) {
			defer func() {
				if r := recover(); r != nil {
					toolCallsMu.Lock()
					toolCalls = append(toolCalls, fmt.Sprintf("%s → panic: %v", name, r))
					toolCallsMu.Unlock()
					rv = rejectPromise(vm, fmt.Errorf("%s panicked: %v", name, r))
				}
			}()
			// Convert the JS argument (first positional arg) to JSON.
			var inputJSON json.RawMessage
			if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) && !goja.IsNull(call.Arguments[0]) {
				// Export the JS object to Go interface{}, then marshal to JSON.
				exported := call.Arguments[0].Export()
				jsonBytes, err := json.Marshal(exported)
				if err != nil {
					// Return a rejected promise.
					return rejectPromise(vm, fmt.Errorf("invalid arguments for %s: %v", name, err))
				}
				inputJSON = jsonBytes
			} else {
				inputJSON = json.RawMessage(`{}`)
			}

			// Execute the tool synchronously.
			result, err := toolRef.Execute(ctx, inputJSON)
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

			// Resolve the promise with the tool result content string.
			// If it's an error result, reject the promise so the model's
			// try/catch can handle it.
			if result.IsError {
				return rejectPromise(vm, fmt.Errorf("%s", result.Content))
			}
			return resolvePromise(vm, result.Content)
		})
	}
	vm.Set("tools", toolsObj)

	// Set up context cancellation → vm.Interrupt.
	go func() {
		<-ctx.Done()
		vm.Interrupt(context.Cause(ctx))
	}()

	// Execute the code. We wrap it in an async IIFE so that top-level
	// await works (goja is ES5.1 — no top-level await). A try/catch inside
	// captures runtime errors (including unhandled promise rejections from
	// tool calls) and routes them to a Go variable.
	var asyncErr string
	vm.Set("__captureError", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 {
			asyncErr = call.Arguments[0].ToString().String()
		}
		return goja.Undefined()
	})
	wrapped := "(async function() {\n" +
		"  try {\n" + code + "\n" +
		"  } catch(e) {\n" +
		"    __captureError(e);\n" +
		"  }\n" +
		"})()"
	_, err := vm.RunString(wrapped)
	if err != nil {
		// Check if it was a timeout/interrupt.
		if ctx.Err() != nil {
			return nil, fmt.Errorf("execution timed out after %v", codeExecTimeout)
		}
		return &execResult{
			stdout:    stdout.String(),
			toolCalls: toolCalls,
		}, fmt.Errorf("%s", err.Error())
	}
	// Check for captured async error (unhandled exception in code).
	if asyncErr != "" {
		return &execResult{
			stdout:    stdout.String(),
			toolCalls: toolCalls,
		}, fmt.Errorf("%s", asyncErr)
	}

	return &execResult{
		stdout:    stdout.String(),
		toolCalls: toolCalls,
	}, nil
}

// writeConsoleOutput appends formatted arguments to the stdout builder.
func writeConsoleOutput(stdout *strings.Builder, args []goja.Value) {
	for i, arg := range args {
		if i > 0 {
			stdout.WriteString(" ")
		}
		if goja.IsUndefined(arg) || goja.IsNull(arg) {
			stdout.WriteString("undefined")
			continue
		}
		// Export converts JS values to Go equivalents. Strings stay strings,
		// objects become map[string]interface{}, arrays become []interface{}.
		exported := arg.Export()
		switch v := exported.(type) {
		case string:
			stdout.WriteString(v)
		default:
			// For non-string values, JSON-encode them for readability.
			jsonBytes, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				stdout.WriteString(fmt.Sprintf("%v", v))
			} else {
				stdout.Write(jsonBytes)
			}
		}
	}
	stdout.WriteString("\n")
}

// resolvePromise creates a resolved goja Promise with the given value.
func resolvePromise(vm *goja.Runtime, value string) goja.Value {
	promise, resolve, _ := vm.NewPromise()
	resolve(value)
	return vm.ToValue(promise)
}

// rejectPromise creates a rejected goja Promise with the given error.
func rejectPromise(vm *goja.Runtime, err error) goja.Value {
	promise, _, reject := vm.NewPromise()
	reject(err.Error())
	return vm.ToValue(promise)
}
