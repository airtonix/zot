package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/patriceckhart/zot/packages/provider"
)

type outcomeTool struct {
	result     ToolResult
	err        error
	panicValue bool
	called     bool
	args       json.RawMessage
}

func (t *outcomeTool) Name() string            { return "outcome" }
func (t *outcomeTool) Description() string     { return "test" }
func (t *outcomeTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (t *outcomeTool) Execute(_ context.Context, args json.RawMessage, _ func(string)) (ToolResult, error) {
	t.called = true
	t.args = append(json.RawMessage(nil), args...)
	if t.panicValue {
		panic("test panic")
	}
	return t.result, t.err
}

func TestToolResultLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, status                             string
		err                                      error
		isError, panicValue, block, cancelBefore bool
		resultStatus                             string
	}{
		{name: "success", status: "completed"},
		{name: "error", status: "failed", err: errors.New("failure")},
		{name: "error result", status: "failed", isError: true},
		{name: "panic", status: "failed", panicValue: true},
		{name: "guard", status: "blocked", block: true},
		{name: "tool policy", status: "blocked", err: &ToolPolicyError{Err: errors.New("denied")}},
		{name: "cancel", status: "cancelled", err: context.Canceled},
		{name: "deadline", status: "timed_out", err: context.DeadlineExceeded},
		{name: "cancel before execution", status: "cancelled", cancelBefore: true},
		{name: "tool timeout result", status: "timed_out", isError: true, resultStatus: "timed_out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := &outcomeTool{err: tc.err, result: ToolResult{IsError: tc.isError, Status: tc.resultStatus}, panicValue: tc.panicValue}
			a := NewAgent(nil, "test", "", NewRegistry(tool))
			rewritten := json.RawMessage(`{"effective":true}`)
			a.BeforeToolExecute = func(provider.ToolCallBlock) (bool, string, json.RawMessage) { return !tc.block, "blocked", rewritten }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelBefore {
				cancel()
			}
			var events []EvToolResult
			msg, hadError := a.executeTools(ctx, provider.Message{Content: []provider.Content{provider.ToolCallBlock{ID: "call", Name: tool.Name(), Arguments: json.RawMessage(`{}`)}}}, func(e AgentEvent) {
				if result, ok := e.(EvToolResult); ok {
					events = append(events, result)
				}
			})
			if len(events) != 1 {
				t.Fatalf("got %d results", len(events))
			}
			e := events[0]
			if e.ID != "call" || e.Name != tool.Name() || e.Status != tc.status || e.Executed != tool.called {
				t.Fatalf("wrong event: %+v", e)
			}
			if !tc.cancelBefore && string(e.Args) != string(rewritten) {
				t.Fatalf("wrong effective args: %s", e.Args)
			}
			if tool.called && string(e.Args) != string(tool.args) {
				t.Fatal("reported args differ from execution")
			}
			if tool.called == (tc.block || tc.cancelBefore) {
				t.Fatal("unexpected execution")
			}
			if hadError != (tc.status != "completed") {
				t.Fatalf("hadError = %v", hadError)
			}
			if len(msg.Content) != 1 || msg.Content[0].(provider.ToolResultBlock).CallID != e.ID {
				t.Fatal("transcript result pairing lost")
			}
		})
	}
}

func TestUnknownToolHasTerminalEvent(t *testing.T) {
	a := NewAgent(nil, "test", "", nil)
	var got EvToolResult
	a.runOneTool(context.Background(), provider.ToolCallBlock{ID: "missing", Name: "missing"}, func(e AgentEvent) { got = e.(EvToolResult) })
	if got.Status != "failed" || got.Executed || !got.Result.IsError {
		t.Fatalf("wrong result: %+v", got)
	}
}

func TestPromptSubmitBeforeQueueConsumption(t *testing.T) {
	a := NewAgent(nil, "test", "", nil)
	var events []AgentEvent
	a.OnEvent = func(e AgentEvent) { events = append(events, e) }
	if a.QueueMessage("  ") {
		t.Fatal("empty queue accepted")
	}
	if !a.QueueMessage(" queued ") {
		t.Fatal("queue rejected")
	}
	if len(events) != 1 {
		t.Fatalf("events = %v", events)
	}
	got := events[0].(EvPromptSubmit)
	if got.Text != "queued" || !got.Queued {
		t.Fatalf("event = %+v", got)
	}
	a.appendQueuedAsUser(a.drainQueuedMessages(), a.OnEvent)
	count := 0
	for _, e := range events {
		if _, ok := e.(EvPromptSubmit); ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate submission: %d", count)
	}
}

func TestCompactionFailureHasPairedEvents(t *testing.T) {
	a := NewAgent(nil, "test", "", nil)
	var events []EvCompact
	a.OnEvent = func(e AgentEvent) { events = append(events, e.(EvCompact)) }
	if _, err := a.Compact(context.Background(), 0, nil); err == nil {
		t.Fatal("empty compaction succeeded")
	}
	if len(events) != 2 {
		t.Fatalf("events = %v", events)
	}
	if events[0].Phase != "pre" || events[1].Phase != "post" || events[0].ID == "" || events[0].ID != events[1].ID || events[1].Status != "failed" || events[1].Err == nil {
		t.Fatalf("events = %+v", events)
	}
}
