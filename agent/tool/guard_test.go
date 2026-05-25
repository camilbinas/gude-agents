package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestAllow_ReturnsAllowingDecision(t *testing.T) {
	d := Allow()
	if !d.Allow {
		t.Error("Allow() should produce Allow=true")
	}
	if d.Reason != "" {
		t.Errorf("Allow() reason = %q, want empty", d.Reason)
	}
}

func TestDeny_ReturnsBlockingDecision(t *testing.T) {
	d := Deny("not authorized")
	if d.Allow {
		t.Error("Deny() should produce Allow=false")
	}
	if d.Reason != "not authorized" {
		t.Errorf("Deny() reason = %q, want %q", d.Reason, "not authorized")
	}
}

func TestDenyf_FormattedReason(t *testing.T) {
	d := Denyf("user %s lacks role %s", "alice", "admin")
	if d.Allow {
		t.Error("Denyf() should produce Allow=false")
	}
	if d.Reason != "user alice lacks role admin" {
		t.Errorf("Denyf() reason = %q", d.Reason)
	}
}

type guardInput struct {
	Country string `json:"country"`
	Amount  int    `json:"amount"`
}

func TestWithGuard_DeserializesInputAndDelegates(t *testing.T) {
	var captured guardInput
	tt := &Tool{}
	WithGuard(func(_ context.Context, in guardInput) (Decision, error) {
		captured = in
		return Allow(), nil
	})(tt)

	raw, _ := json.Marshal(guardInput{Country: "DE", Amount: 100})
	d, err := tt.Guard(context.Background(), raw)
	if err != nil {
		t.Fatalf("guard error: %v", err)
	}
	if !d.Allow {
		t.Error("guard should have allowed")
	}
	if captured.Country != "DE" || captured.Amount != 100 {
		t.Errorf("guard received %+v, want {DE 100}", captured)
	}
}

func TestWithGuard_DenyPropagatesReason(t *testing.T) {
	tt := &Tool{}
	WithGuard(func(_ context.Context, in guardInput) (Decision, error) {
		if in.Country == "US" {
			return Deny("US not supported"), nil
		}
		return Allow(), nil
	})(tt)

	raw, _ := json.Marshal(guardInput{Country: "US"})
	d, err := tt.Guard(context.Background(), raw)
	if err != nil {
		t.Fatalf("guard error: %v", err)
	}
	if d.Allow {
		t.Error("guard should have denied US")
	}
	if d.Reason != "US not supported" {
		t.Errorf("guard reason = %q", d.Reason)
	}
}

func TestWithGuard_InvalidJSONIsDeniedNotErrored(t *testing.T) {
	tt := &Tool{}
	WithGuard(func(_ context.Context, _ guardInput) (Decision, error) {
		return Allow(), nil
	})(tt)

	d, err := tt.Guard(context.Background(), json.RawMessage("not-json"))
	if err != nil {
		t.Fatalf("invalid JSON should not return an error, got: %v", err)
	}
	if d.Allow {
		t.Error("invalid JSON should deny the call")
	}
	if d.Reason == "" {
		t.Error("invalid JSON denial should carry a reason")
	}
}

func TestWithGuard_GuardErrorPropagates(t *testing.T) {
	tt := &Tool{}
	wantErr := errors.New("policy backend down")
	WithGuard(func(_ context.Context, _ guardInput) (Decision, error) {
		return Decision{}, wantErr
	})(tt)

	raw, _ := json.Marshal(guardInput{Country: "DE"})
	_, err := tt.Guard(context.Background(), raw)
	if !errors.Is(err, wantErr) {
		t.Errorf("expected guard error to propagate, got %v", err)
	}
}
