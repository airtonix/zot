package ext

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/agent/extproto"
)

func TestCallToolRoundTrip(t *testing.T) {
	h := newHarness("caller")
	done := make(chan error, 1)
	go func() { done <- h.ext.Run() }()
	t.Cleanup(func() {
		h.hostW.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("SDK did not exit")
		}
	})
	h.drainUntil(t, "hello")
	h.sendToExt(t, extproto.HelloAckFromHost{Type: "hello_ack", Capabilities: []string{"call_tool"}})
	h.drainUntil(t, "ready")
	result := make(chan ToolResult, 1)
	errs := make(chan error, 1)
	go func() {
		r, err := h.ext.CallTool(context.Background(), "skill", map[string]string{"name": "test"})
		result <- r
		errs <- err
	}()
	frame := h.drainUntil(t, "call_tool")
	var call extproto.CallToolFromExt
	if err := json.Unmarshal(frame.raw, &call); err != nil || call.Name != "skill" || string(call.Args) != `{"name":"test"}` {
		t.Fatalf("call: %+v, %v", call, err)
	}
	h.sendToExt(t, extproto.ToolResultFromHost{Type: "tool_result", ID: call.ID, Name: "skill", Content: []extproto.ContentBlock{{Type: "text", Text: "loaded"}}})
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if r := <-result; len(r.Content) != 1 || r.Content[0].Text != "loaded" {
		t.Fatalf("result: %+v", r)
	}
}

func TestCallToolFromToolHandlerCarriesParent(t *testing.T) {
	h := newHarness("caller")
	h.ext.InteractiveTool("ask", "ask", json.RawMessage(`{}`), func(ctx context.Context, _ json.RawMessage) ToolResult {
		res, err := h.ext.CallTool(ctx, "read", map[string]string{"path": "a"})
		if err != nil {
			return TextErrorResult(err.Error())
		}
		return res
	})
	done := make(chan error, 1)
	go func() { done <- h.ext.Run() }()
	t.Cleanup(func() {
		h.hostW.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("SDK did not exit")
		}
	})
	h.drainUntil(t, "hello")
	h.sendToExt(t, extproto.HelloAckFromHost{Type: "hello_ack", Capabilities: []string{"call_tool"}})
	h.drainUntil(t, "ready")
	h.sendToExt(t, extproto.ToolCallFromHost{Type: "tool_call", ID: "parent", Name: "ask", Args: json.RawMessage(`{}`)})
	var call extproto.CallToolFromExt
	if err := json.Unmarshal(h.drainUntil(t, "call_tool").raw, &call); err != nil || call.ParentID != "parent" || call.Name != "read" {
		t.Fatalf("nested request: %+v, %v", call, err)
	}
	h.sendToExt(t, extproto.ToolResultFromHost{Type: "tool_result", ID: call.ID, Name: "read", Content: []extproto.ContentBlock{{Type: "text", Text: "loaded"}}})
	var reply extproto.ToolResultFromExt
	if err := json.Unmarshal(h.drainUntil(t, "tool_result").raw, &reply); err != nil || reply.ID != "parent" || reply.Content[0].Text != "loaded" {
		t.Fatalf("parent result: %+v, %v", reply, err)
	}
}

func TestCallToolCancellation(t *testing.T) {
	h := newHarness("caller")
	done := make(chan error, 1)
	go func() { done <- h.ext.Run() }()
	t.Cleanup(func() {
		h.hostW.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("SDK did not exit")
		}
	})
	h.drainUntil(t, "hello")
	h.sendToExt(t, extproto.HelloAckFromHost{Type: "hello_ack", Capabilities: []string{"call_tool"}})
	h.drainUntil(t, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := h.ext.CallTool(ctx, "read", map[string]string{}); result <- err }()
	var call extproto.CallToolFromExt
	if err := json.Unmarshal(h.drainUntil(t, "call_tool").raw, &call); err != nil {
		t.Fatal(err)
	}
	cancel()
	frame := h.drainUntil(t, "call_tool_cancel")
	if frame.hdr.ID != call.ID {
		t.Fatalf("canceled ID = %q, want %q", frame.hdr.ID, call.ID)
	}
	select {
	case err := <-result:
		if err != context.Canceled {
			t.Fatalf("cancel error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("call did not cancel")
	}
}

func TestCallToolUnsupportedHost(t *testing.T) {
	e := New("caller", "test")
	_, err := e.CallTool(context.Background(), "read", map[string]string{"path": "a"})
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("unsupported host: %v", err)
	}
}
