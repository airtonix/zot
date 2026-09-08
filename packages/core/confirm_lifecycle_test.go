package core

import "testing"

func TestConfirmationDecisionSources(t *testing.T) {
	call := ToolCallConfirmation{ID: "call", Name: "bash"}
	var nilGate *ConfirmGate
	if d := nilGate.DecideToolCall(call); !d.Allow || d.Source != "yolo" {
		t.Fatalf("nil gate: %+v", d)
	}
	if d := NewConfirmGate(nil).DecideToolCall(call); d.Allow || d.Source != "policy" {
		t.Fatalf("no confirmer: %+v", d)
	}
	for _, rememberAll := range []bool{false, true} {
		inner := &recordingConfirmer{replies: []ConfirmDecision{{Allow: true, RememberTool: !rememberAll, RememberAll: rememberAll}}}
		gate := NewConfirmGate(inner)
		if d := gate.DecideToolCall(call); !d.Allow || d.Source != "user" {
			t.Fatalf("first: %+v", d)
		}
		if d := gate.DecideToolCall(call); !d.Allow || d.Source != "remembered" {
			t.Fatalf("remembered: %+v", d)
		}
		if len(inner.calls) != 1 {
			t.Fatal("remembered decision prompted again")
		}
		gate.AllowAll()
		if d := gate.DecideToolCall(call); !d.Allow || d.Source != "yolo" {
			t.Fatalf("explicit yolo: %+v", d)
		}
		gate.Reset()
		if d := gate.DecideToolCall(call); d.Allow || d.Source != "user" {
			t.Fatalf("reset: %+v", d)
		}
	}
}
