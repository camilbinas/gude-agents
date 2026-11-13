package tracing

import "testing"

func boolPtr(b bool) *bool { return &b }

func TestInsecureExport(t *testing.T) {
	tests := []struct {
		name   string
		cfg    setupConfig
		envKey string
		envVal string
		want   bool
	}{
		{name: "default is insecure", want: true},
		{name: "option forces secure", cfg: setupConfig{insecure: boolPtr(false)}, want: false},
		{name: "option forces insecure", cfg: setupConfig{insecure: boolPtr(true)}, want: true},
		{name: "traces env false enables TLS", envKey: "OTEL_EXPORTER_OTLP_TRACES_INSECURE", envVal: "false", want: false},
		{name: "traces env true", envKey: "OTEL_EXPORTER_OTLP_TRACES_INSECURE", envVal: "true", want: true},
		{name: "generic env false enables TLS", envKey: "OTEL_EXPORTER_OTLP_INSECURE", envVal: "false", want: false},
		{name: "invalid env falls back to default", envKey: "OTEL_EXPORTER_OTLP_INSECURE", envVal: "maybe", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_INSECURE", "")
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "")
			if tc.envKey != "" {
				t.Setenv(tc.envKey, tc.envVal)
			}
			if got := insecureExport(tc.cfg); got != tc.want {
				t.Errorf("insecureExport() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInsecureExportOptionBeatsEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "false")
	if got := insecureExport(setupConfig{insecure: boolPtr(true)}); !got {
		t.Error("explicit WithInsecure(true) should override env")
	}
}
