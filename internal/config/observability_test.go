package config

import (
	"strings"
	"testing"
)

func TestValidateOTLPObservabilityConfig(t *testing.T) {
	tests := []struct {
		name    string
		otlp    OTLPConfig
		wantErr string
	}{
		{
			name: "zero value is valid",
		},
		{
			name: "valid endpoint passes",
			otlp: OTLPConfig{Endpoint: "http://localhost:4318", FlushIntervalSeconds: 5},
		},
		{
			name:    "malformed endpoint fails",
			otlp:    OTLPConfig{Endpoint: "http://[::bad-url"},
			wantErr: "invalid observability.otlp.endpoint",
		},
		{
			name:    "negative flush interval fails",
			otlp:    OTLPConfig{Endpoint: "http://localhost:4318", FlushIntervalSeconds: -1},
			wantErr: "flush_interval_seconds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOTLPConfig(tt.otlp)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
