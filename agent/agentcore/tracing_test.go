package agentcore

import (
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

func TestBuildAgentCoreResource_ServiceNameFollowsConvention(t *testing.T) {
	res, err := buildAgentCoreResource("MyAgent")
	if err != nil {
		t.Fatalf("buildAgentCoreResource: %v", err)
	}
	want := "MyAgent.DEFAULT"
	got, _ := findAttribute(res.Attributes(), "service.name")
	if got != want {
		t.Errorf("service.name = %q, want %q", got, want)
	}
}

func TestBuildAgentCoreResource_PreservesDefaultAttributes(t *testing.T) {
	res, err := buildAgentCoreResource("Foo")
	if err != nil {
		t.Fatalf("buildAgentCoreResource: %v", err)
	}
	// The merged default Resource carries telemetry SDK metadata.
	if _, ok := findAttribute(res.Attributes(), "telemetry.sdk.name"); !ok {
		t.Error("expected telemetry.sdk.name from default resource")
	}
}

func TestStripScheme(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"localhost:4317":         "localhost:4317",
		"http://localhost:4318":  "localhost:4318",
		"https://otel.example":   "otel.example",
		"https://otel.example/v": "otel.example/v",
	}
	for in, want := range cases {
		if got := stripScheme(in); got != want {
			t.Errorf("stripScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetupTracing_RequiresRuntimeName(t *testing.T) {
	_, _, err := SetupTracing(nil, "")
	if err == nil {
		t.Fatal("expected error for empty runtime name")
	}
	if !strings.Contains(err.Error(), "runtimeName") {
		t.Errorf("error %q should mention runtimeName", err)
	}
}

func findAttribute(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}
