package auto

import (
	"testing"
)

func TestIsDev(t *testing.T) {
	tests := []struct {
		key  string
		val  string
		want bool
	}{
		{"ENV", "development", true},
		{"ENV", "dev", true},
		{"ENV", "local", true},
		{"ENV", "DEV", true},
		{"ENV", "LOCAL", true},
		{"ENV", "Development", true},
		{"ENV", "production", false},
		{"ENV", "prod", false},
		{"ENV", "staging", false},
		{"ENV", "", false},
		{"APP_ENV", "dev", true},
		{"APP_ENV", "local", true},
		{"APP_ENV", "production", false},
		{"ENVIRONMENT", "development", true},
		{"ENVIRONMENT", "local", true},
		{"ENVIRONMENT", "production", false},
	}

	for _, tt := range tests {
		t.Run(tt.key+"="+tt.val, func(t *testing.T) {
			// Clear all env vars to isolate.
			t.Setenv("APP_ENV", "")
			t.Setenv("ENV", "")
			t.Setenv("ENVIRONMENT", "")
			t.Setenv(tt.key, tt.val)
			if got := isDev(); got != tt.want {
				t.Errorf("isDev() with %s=%q = %v, want %v", tt.key, tt.val, got, tt.want)
			}
		})
	}
}

func TestIsDev_Precedence(t *testing.T) {
	// APP_ENV takes precedence over ENV.
	t.Setenv("APP_ENV", "production")
	t.Setenv("ENV", "dev")
	t.Setenv("ENVIRONMENT", "dev")
	if isDev() {
		t.Error("expected isDev()=false when APP_ENV=production, even if ENV=dev")
	}
}
