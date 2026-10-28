package approval

import (
	"bytes"
	"encoding/gob"
	"testing"
)

func TestDecisionSurvivesGob(t *testing.T) {
	in := map[string]any{"decision": &Decision{Approved: false, Reason: "not now"}}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var out map[string]any
	if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, ok := out["decision"].(*Decision)
	if !ok {
		t.Fatalf("round trip lost the concrete type: %T", out["decision"])
	}
	if got.Approved || got.Reason != "not now" {
		t.Fatalf("decision not carried through: %+v", got)
	}
}

func TestRejectionIsAValidDecision(t *testing.T) {
	d := Decision{Approved: false}
	if d.Approved {
		t.Fatal("a rejection must not read as an approval")
	}
	if d.Reason != "" {
		t.Fatalf("an empty reason must stay empty, got %q", d.Reason)
	}
}
