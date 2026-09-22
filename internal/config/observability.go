package config

// ObservabilityConfig groups opt-in observability export settings. All
// sub-sections are disabled unless explicitly configured; ggcode never
// exports telemetry to a remote endpoint by default.
type ObservabilityConfig struct {
	OTLP OTLPConfig `yaml:"otlp,omitempty" json:"otlp,omitempty"`
}

// OTLPConfig configures live trace export over the OTLP/HTTP JSON protocol
// (OpenTelemetry standard) using the GenAI semantic conventions. Spans for
// LLM API calls and tool executions are pushed in batches so sessions can be
// observed in real time in any OTLP-compatible backend (Jaeger, Grafana
// Tempo, Langfuse, Datadog OTLP intake, ...).
//
// Endpoint may be a base URL (e.g. http://localhost:4318) - /v1/traces is
// appended automatically. When unset, the standard OTEL_EXPORTER_OTLP_ENDPOINT
// / OTEL_EXPORTER_OTLP_TRACES_ENDPOINT environment variables are honored.
type OTLPConfig struct {
	Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	// Headers are sent with every OTLP export request, e.g. authorization
	// headers required by hosted backends (Langfuse, Grafana Cloud, ...).
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// FlushIntervalSeconds controls the batch flush period (default 5,
	// minimum 1). Zero means default; negative values fail validation.
	FlushIntervalSeconds int `yaml:"flush_interval_seconds,omitempty" json:"flush_interval_seconds,omitempty"`
}
