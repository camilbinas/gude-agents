package record_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/execution/internal/conformance"
	"github.com/camilbinas/gude-agents/agent/execution/internal/record"
)

func TestMarshalNeverPersistsTheCallerVersion(t *testing.T) {
	in := conformance.Full("exec-1")
	in.Version = 77
	data, err := record.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	if string(object["version"]) != "0" {
		t.Fatalf("payload version = %s, want 0", object["version"])
	}
	if in.Version != 77 {
		t.Fatal("Marshal modified the caller's execution")
	}
}

func TestUnmarshalStampsTheDurableVersionAndKeepsEveryField(t *testing.T) {
	in := conformance.Full("exec-1")
	data, err := record.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := record.Unmarshal(data, "exec-1", 9)
	if err != nil {
		t.Fatal(err)
	}
	want := in
	want.Version = 9
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("round trip lost state:\n got %+v\nwant %+v", out, want)
	}
}

func TestUnmarshalRejectsAMisplacedOrCorruptRecord(t *testing.T) {
	data, err := record.Marshal(conformance.Full("exec-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := record.Unmarshal(data, "exec-2", 1); err == nil {
		t.Error("a record for exec-1 was returned for exec-2")
	}
	if _, err := record.Unmarshal([]byte(`{broken`), "exec-1", 1); err == nil {
		t.Error("corrupt payload was accepted")
	}
}

func TestErrorMapping(t *testing.T) {
	if !errors.Is(record.NotFound("x"), agent.ErrExecutionNotFound) {
		t.Error("NotFound does not wrap ErrExecutionNotFound")
	}
	if !errors.Is(record.Conflict("save", "x"), agent.ErrExecutionConflict) {
		t.Error("Conflict does not wrap ErrExecutionConflict")
	}
	cause := errors.New("boom")
	wrapped := record.Wrap("load", cause)
	if !errors.Is(wrapped, cause) || errors.Is(wrapped, agent.ErrExecutionConflict) || errors.Is(wrapped, agent.ErrExecutionNotFound) {
		t.Errorf("Wrap = %v; it must keep the cause without classifying it", wrapped)
	}
	if err := record.ValidateID(""); err == nil {
		t.Error("empty ID accepted")
	}
}

func TestVersionAndExpectedBounds(t *testing.T) {
	for _, n := range []int64{0, -1} {
		if _, err := record.Version(n); err == nil {
			t.Errorf("Version(%d) accepted", n)
		}
	}
	if v, err := record.Version(5); err != nil || v != 5 {
		t.Errorf("Version(5) = %d, %v", v, err)
	}
	for _, expected := range []uint64{0, 1 << 63} {
		if err := record.CheckExpected("x", expected); !errors.Is(err, agent.ErrExecutionConflict) {
			t.Errorf("CheckExpected(%d) = %v, want a conflict", expected, err)
		}
	}
	if err := record.CheckExpected("x", 3); err != nil {
		t.Errorf("CheckExpected(3) = %v", err)
	}
}
