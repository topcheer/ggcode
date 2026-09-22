# Live Trace Export (OTel GenAI / OTLP)

ggcode can stream every LLM API call and tool execution as **OpenTelemetry
spans** to any OTLP-compatible observability backend — Jaeger, Grafana Tempo,
Langfuse, Datadog, Aspire, Grafana Cloud, etc. — in real time, using the
[OpenTelemetry GenAI semantic conventions](https://github.com/open-telemetry/semantic-conventions-genai).

Export is **opt-in** and completely off by default: no telemetry leaves the
machine unless you configure an endpoint.

> Related: the `/export-trace` slash command writes a one-off JSON trace file
> for the current session; OTLP export is the live, standard-format counterpart.

## Configuration

Add to `~/.ggcode/config.yaml` (or the project instance config):

```yaml
observability:
  otlp:
    endpoint: http://localhost:4318   # /v1/traces is appended automatically
    headers:                          # optional, e.g. for hosted backends
      Authorization: Basic <base64>
    flush_interval_seconds: 5         # optional, default 5 (min 1)
```

Standard environment variables are honored when `endpoint` is unset:

- `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` — used as-is (full URL per OTel spec)
- `OTEL_EXPORTER_OTLP_ENDPOINT` — treated as a base URL, `/v1/traces` appended

Explicit config wins over env vars.

## What gets exported

One span per completed LLM call / tool execution, batched (up to 64 spans per
request) and pushed by a background goroutine. All spans of a session share a
single trace ID. Emission is fire-and-forget: a stalled backend never slows
the agent loop — pending events are dropped (visible in `Stats()`).

**LLM call** → span `chat <model>` (kind CLIENT):

| Attribute | Meaning |
|---|---|
| `gen_ai.operation.name` | `chat` |
| `gen_ai.system` | vendor (lowercased) |
| `gen_ai.request.model` | model name |
| `gen_ai.usage.input_tokens` / `gen_ai.usage.output_tokens` | token usage |
| `gen_ai.usage.cache_read.input_tokens` / `gen_ai.usage.cache_creation.input_tokens` | cache tokens (when > 0) |
| `ggcode.ttft_ms` / `ggcode.think_time_ms` | time-to-first-token / reasoning time |
| `ggcode.turn_index` | conversation turn |

**Tool execution** → span `execute_tool <name>` (kind INTERNAL), same
`ggcode.turn_index`; failed tools carry an OTLP ERROR status with the error
message and `error.type: tool_execution_error`.

## Try it locally with Jaeger

```bash
docker run --rm -p 16686:16686 -p 4318:4318 jaegertracing/all-in-one
# in another terminal
ggcode   # with observability.otlp.endpoint pointing at http://localhost:4318
# open http://localhost:16686, pick service "ggcode"
```

For Langfuse, point the endpoint at `https://<your-langfuse>/api/public/otel`
with the basic-auth `Authorization` header from the Langfuse project settings.

## Notes & limitations

- The exporter is dependency-free (OTLP/HTTP JSON encoding, no OTel SDK).
- One trace per ggcode process/session; turn spans are flattened (each
  LLM/tool span is a root of the shared session trace).
- Failure to export is logged via the `metrics` debug category; data is
  dropped rather than retried (a single POST attempt per batch).
- Headless/IM daemon sessions export too (wired in `cmd/ggcode/daemon.go`).
