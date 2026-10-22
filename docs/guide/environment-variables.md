# Environment Variables

ggcode reads configuration from `GGCODE_*` environment variables. Unless noted otherwise, "set" means any non-empty value. Several boolean switches accept explicit `0`/`false` to force-disable while empty means "use the default".

## Core behavior

| Variable | Purpose | Value semantics |
|----------|---------|-----------------|
| `GGCODE_NO_COAUTHOR` | Do not inject the `Co-Authored-By: ggcode <noreply@ggcode.dev>` trailer into git commit messages. Covers all three injection paths: the `git_commit` tool, `run_command`'s shell-path rewrite, and the system-prompt directive. | Any non-empty value disables injection (same convention as `GGCODE_SKIP_AUTOCONFIG`). |
| `GGCODE_SKIP_AUTOCONFIG` | Skip automatic provider auto-configuration (and the plaintext API key warning flow). | Any non-empty value. |
| `GGCODE_INSECURE` | Skip TLS certificate verification (self-signed gateways). | `1` / `true` / `yes` to enable. Use only against trusted endpoints. |
| `GGCODE_ASCII_LOGO` | Render the ASCII logo on startup (TTY detection override). | Any non-empty value shows the logo. |
| `GGCODE_TMUX_SETUP_LAYOUT` | tmux startup layout for the built-in terminal. Set automatically by the TUI when spawning subprocesses. | Layout name. |
| `GGCODE_ALLOW_PROJECT_MCP` | One-shot approval for workspace-local `.mcp.json` MCP servers (project MCP gate). | `1` to approve for the invocation. |
| `GGCODE_WRAPPER_KIND` | Declares the wrapper context (e.g. npm/brew) for the update flow. | Wrapper identifier. |
| `GGCODE_TRIAL_FORK` | Internal: opt into the experimental trial fork path (daemon/pipe). | Any non-empty value. |

## Provider & model

| Variable | Purpose | Value semantics |
|----------|---------|-----------------|
| `GGCODE_FILES_API` | Force the Anthropic Files API on/off for gateways that do not advertise `/v1/files` forwarding. | `1` force-enable, `0` disable everywhere; empty = auto-detect. |
| `GGCODE_STREAM_IDLE_TIMEOUT` | Idle timeout for streaming LLM responses. | Duration string (e.g. `120s`). |
| `GGCODE_LLM_TAPE` | Record/replay all LLM traffic for deterministic offline runs. | `record:<path>` or `replay:<path>`. |
| `GGCODE_LLM_FAULT` | Fault injection composed with `GGCODE_LLM_TAPE=replay` for resilience testing. | Injection spec (see `internal/provider/fault_inject.go`). |
| `GGCODE_TOOL_TAPE` | Record/replay tool-call results (deterministic replay harness). | `record:<path>` or `replay:<path>`. |
| `GGCODE_GITHUB_COPILOT_CLIENT_ID` | Override the GitHub Copilot device-flow client ID. | Client ID string. |
| `GGCODE_OAUTH_CLIENT_SECRET` | A2A OAuth2 client secret fallback. | Secret string. |
| `GGCODE_ZAI_API_KEY` | Z.ai API key for e2e helpers (fallback when `ZAI_API_KEY` is unset). | API key. |

## Tool runtime

| Variable | Purpose | Value semantics |
|----------|---------|-----------------|
| `GGCODE_TOOL_DEDUP` | Kill switch for the tool-result dedup ledger. | `0` disables the ledger entirely. |
| `GGCODE_TOOL_SEARCH` | Opt in/out of dynamic tool search (schema pruning for very large tool registries). | `on` / `off`; default off. |
| `GGCODE_JOB_MEM_LIMIT` | Per-background-job memory limit. | Bytes (uint). |
| `GGCODE_CODE_INDEX_MAX_TERMS` | Cap on code index terms. | Integer. |
| `GGCODE_BROWSER_PROFILE_GC` | Toggle GC of leaked browser profiles. | `0` disables. |
| `GGCODE_BROWSER_PROFILE_TTL_DAYS` | Age threshold before a stale browser profile is collected. | Days (integer). |

## Debugging & observability

| Variable | Purpose | Value semantics |
|----------|---------|-----------------|
| `GGCODE_DEBUG` | Enable debug logging. | `1` / `true` / `all` = all categories; comma list (`agent,provider`) = only those categories. |
| `GGCODE_DEBUG_<CATEGORY>` | Enable a single category (e.g. `GGCODE_DEBUG_AGENT=1`). Category suffixes are defined in `internal/debug`. | `1`; `2` / `verbose` / `trace` for higher verbosity (where supported). |
| `GGCODE_DEBUG_BUBBLETEA` | Bubble Tea debug logging. | Set to enable. |
| `GGCODE_PPROF` | Serve `net/http/pprof` on a port. | Port number (e.g. `GGCODE_PPROF=6060 ggcode`). |
| `GGCODE_AUDIT_LEDGER` | Write a verifiable audit ledger of agent runs. | Path to `audit.jsonl`. |
| `GGCODE_TRAINING_EXPORT` | Export trajectories (secret-pattern redacted) for training/analysis. | `1` to enable the sampling outlet. |

## IM & network adapters

| Variable | Purpose | Value semantics |
|----------|---------|-----------------|
| `GGCODE_IM_MATRIX_HOMESERVER` / `GGCODE_IM_MATRIX_ACCESS_TOKEN` | Matrix adapter fallbacks when config omits them. | Homeserver URL / access token. |
| `GGCODE_IM_QQ_BOT_APPSECRET` | QQ bot app secret fallback. | Secret string. |
| `GGCODE_WHISPER_CPP_MODEL` | Local whisper.cpp model path for speech-to-text. | Path to a model file (must exist). |
| `GGCODE_TURN_USERNAME` / `GGCODE_TURN_CREDENTIAL` | WebRTC TURN relay credentials (mobile share tunnels). | Username / password. |
| `GGCODE_RELAY_DB_PATH` | Override the ggcode-relay SQLite database location. | File path. |

## Desktop & A2A

| Variable | Purpose | Value semantics |
|----------|---------|-----------------|
| `GGCODE_WAILSKIT_DISABLE_A2A` | Disable A2A in wailskit desktop (also keeps wailskit tests hermetic). | `1` to disable. |

## Installer & updates

| Variable | Purpose | Value semantics |
|----------|---------|-----------------|
| `GGCODE_INSTALL_BASE_URL` | Override the download base for the installer. | URL. |
| `GGCODE_INSTALL_GOOS` / `GGCODE_INSTALL_GOARCH` | Force the target platform of the installed binary. | GOOS / GOARCH pair. |

## Tests & e2e (opt-in only, safe to ignore in production)

| Variable | Purpose |
|----------|---------|
| `GGCODE_E2E` | `1` enables IM e2e suites (QQ/TG/daemon bridge; also `-tags=integration`). |
| `GGCODE_E2E_API_KEY` / `GGCODE_E2E_BASE_URL` / `GGCODE_E2E_MODEL` | Provider settings for builtin-tools e2e. |
| `GGCODE_E2E_QQ_ADAPTER` / `GGCODE_E2E_QQ_CHANNEL` / `GGCODE_E2E_QQ_CHAT_TYPE` | QQ e2e send target. |
| `GGCODE_E2E_TG_CHAT_ID` | Telegram e2e target chat. |
| `GGCODE_RUN_REAL_COPILOT_ACP_E2E` | `1` runs the real Copilot ACP e2e. |
| `GGCODE_LIVE_SESSION_AUDIT` / `GGCODE_AUDIT_DAYS` / `GGCODE_SESSION_DIR` | Live session audit harness over real session files. |
| `GGCODE_REAL_STORE` | `1` permits perf tests to touch the real session store (read-only + index). |
| `GGCODE_SKIP_NET` | Skips tests needing network access. |
| `GGCODE_TEST_ALLOW_REAL_HOME` | `1` lets a test bypass the real-home write guard (isolate first when possible). |
| `GGCODE_PLUGIN` | Internal plugin test injection. |

## Hook runtime contract (injected, not user-set)

The hook runner injects these into hook processes automatically. They are read by hook scripts, not set by users:

`GGCODE_HOOK_EVENT`, `GGCODE_HOOK_PAYLOAD`, `GGCODE_RAW_INPUT`, `GGCODE_RAW_INPUT_TRUNCATED`, `GGCODE_SESSION_END_REASON`, `GGCODE_SESSION_SOURCE`, `GGCODE_TOOL_NAME`, `GGCODE_TOOL_RESULT`, `GGCODE_TOOL_ERROR`, `GGCODE_TOOL_SUCCESS`, `GGCODE_TOOL_DURATION`.

See the [hooks guide](hooks.md) for the payload schema.
