package extensions

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/agent/extproto"
	"github.com/patriceckhart/zot/packages/provider"
)

func TestCallToolDispatchAndRefusal(t *testing.T) {
	m, ext, frames, replies := toolPeer(t)
	ext.callTool = true
	m.SetToolCaller(func(ctx context.Context, origin, name string, args json.RawMessage) extproto.ToolResultFromHost {
		if origin != ext.Manifest.Name || name != "read" {
			t.Errorf("unexpected request: %s %s %s", origin, name, args)
		}
		return extproto.ToolResultFromHost{Content: []extproto.ContentBlock{{Type: "text", Text: "ok"}}}
	})
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "one", Name: "read", Args: json.RawMessage(`{"path":"a"}`)})
	var result extproto.ToolResultFromHost
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &result); err != nil || result.ID != "one" || result.IsError || result.Content[0].Text != "ok" {
		t.Fatalf("result: %+v, %v", result, err)
	}
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "two", Name: "read", Args: json.RawMessage(`{}`), ParentID: "unknown"})
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &result); err != nil || !result.IsError || !strings.Contains(result.Content[0].Text, "parent") {
		t.Fatalf("parent refusal: %+v, %v", result, err)
	}
	ext.mu.Lock()
	ext.outboundCalls = map[string]outboundCall{"parent": {ctx: context.Background(), chain: []string{"ask"}}}
	ext.mu.Unlock()
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "three", Name: "read", Args: json.RawMessage(`{}`)})
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &result); err != nil || !result.IsError || !strings.Contains(result.Content[0].Text, "parent_id") {
		t.Fatalf("missing parent refusal: %+v, %v", result, err)
	}
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "four", ParentID: "parent", Name: "ask", Args: json.RawMessage(`{}`)})
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &result); err != nil || !result.IsError || !strings.Contains(result.Content[0].Text, "cycle") {
		t.Fatalf("cycle refusal: %+v, %v", result, err)
	}
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "five", ParentID: "parent", Name: "read", Args: json.RawMessage(`{}`)})
	result = extproto.ToolResultFromHost{}
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &result); err != nil || result.IsError || result.Content[0].Text != "ok" {
		t.Fatalf("nested call: %+v, %v", result, err)
	}
	ext.mu.Lock()
	ext.outboundCalls["parent"] = outboundCall{ctx: context.Background(), chain: []string{"one", "two", "three", "four"}}
	ext.mu.Unlock()
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "six", ParentID: "parent", Name: "read", Args: json.RawMessage(`{}`)})
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &result); err != nil || !result.IsError || !strings.Contains(result.Content[0].Text, "depth") {
		t.Fatalf("depth refusal: %+v, %v", result, err)
	}
}

func TestCallToolRequiresOptIn(t *testing.T) {
	m, _, frames, replies := toolPeer(t)
	m.SetToolCaller(func(context.Context, string, string, json.RawMessage) extproto.ToolResultFromHost {
		t.Error("unadvertised call reached tool caller")
		return extproto.ToolResultFromHost{}
	})
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "one", Name: "read", Args: json.RawMessage(`{}`)})
	var result extproto.ToolResultFromHost
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &result); err != nil || !result.IsError || !strings.Contains(result.Content[0].Text, "opt in") {
		t.Fatalf("opt-in refusal: %+v, %v", result, err)
	}
}

func TestCallToolToSameExtension(t *testing.T) {
	m, ext, frames, replies := toolPeer(t)
	ext.callTool = true
	m.SetToolCaller(func(ctx context.Context, _, name string, args json.RawMessage) extproto.ToolResultFromHost {
		tool, err := NewTool(m, ToolInfo{Name: name, Extension: ext.Manifest.Name}).Execute(ctx, args, nil)
		if err != nil {
			return extproto.ToolResultFromHost{IsError: true}
		}
		return extproto.ToolResultFromHost{IsError: tool.IsError, Content: []extproto.ContentBlock{{Type: "text", Text: tool.Content[0].(provider.TextBlock).Text}}}
	})
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "one", Name: "ask", Args: json.RawMessage(`{}`)})
	call := toolCallFrame(t, frames)
	sendToolFrame(t, replies, extproto.ToolResultFromExt{Type: "tool_result", ID: call.ID, Content: []extproto.ContentBlock{{Type: "text", Text: "answer"}}})
	var result extproto.ToolResultFromHost
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &result); err != nil || result.IsError || result.Content[0].Text != "answer" {
		t.Fatalf("round trip: %+v, %v", result, err)
	}
}

func TestNestedCallAcrossHostAndExtension(t *testing.T) {
	m, ext, frames, replies := toolPeer(t)
	ext.callTool = true
	m.SetToolCaller(func(ctx context.Context, _, name string, args json.RawMessage) extproto.ToolResultFromHost {
		if name == "read" {
			chain, _ := ctx.Value(toolChainKey{}).([]string)
			if len(chain) != 2 || chain[0] != "ask" || chain[1] != "read" {
				t.Errorf("nested chain: %v", chain)
			}
			return extproto.ToolResultFromHost{Content: []extproto.ContentBlock{{Type: "text", Text: "loaded"}}}
		}
		result, err := NewTool(m, ToolInfo{Name: name, Extension: ext.Manifest.Name}).Execute(ctx, args, nil)
		if err != nil || result.IsError {
			return extproto.ToolResultFromHost{IsError: true}
		}
		return extproto.ToolResultFromHost{Content: []extproto.ContentBlock{{Type: "text", Text: "done"}}}
	})
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "root", Name: "ask", Args: json.RawMessage(`{}`)})
	parent := toolCallFrame(t, frames)
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "cycle", ParentID: parent.ID, Name: "ask", Args: json.RawMessage(`{}`)})
	var refused extproto.ToolResultFromHost
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &refused); err != nil || refused.ID != "cycle" || !refused.IsError || !strings.Contains(refused.Content[0].Text, "cycle") {
		t.Fatalf("cycle: %+v, %v", refused, err)
	}
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "child", ParentID: parent.ID, Name: "read", Args: json.RawMessage(`{}`)})
	var child extproto.ToolResultFromHost
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &child); err != nil || child.ID != "child" || child.IsError || child.Content[0].Text != "loaded" {
		t.Fatalf("child: %+v, %v", child, err)
	}
	sendToolFrame(t, replies, extproto.ToolResultFromExt{Type: "tool_result", ID: parent.ID, Content: []extproto.ContentBlock{{Type: "text", Text: "ok"}}})
	var root extproto.ToolResultFromHost
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &root); err != nil || root.ID != "root" || root.IsError || root.Content[0].Text != "done" {
		t.Fatalf("root: %+v, %v", root, err)
	}
}

func TestNestedCallUsesParentContext(t *testing.T) {
	m, ext, frames, replies := toolPeer(t)
	ext.callTool = true
	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ext.mu.Lock()
	ext.outboundCalls = map[string]outboundCall{"parent": {ctx: parentCtx, chain: []string{"ask"}}}
	ext.mu.Unlock()
	started := make(chan context.Context, 1)
	m.SetToolCaller(func(ctx context.Context, _, _ string, _ json.RawMessage) extproto.ToolResultFromHost {
		started <- ctx
		<-ctx.Done()
		return extproto.ToolResultFromHost{IsError: true, Content: []extproto.ContentBlock{{Type: "text", Text: ctx.Err().Error()}}}
	})
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "child", ParentID: "parent", Name: "read", Args: json.RawMessage(`{}`)})
	select {
	case ctx := <-started:
		if chain, _ := ctx.Value(toolChainKey{}).([]string); len(chain) != 2 || chain[0] != "ask" || chain[1] != "read" {
			t.Fatalf("chain: %v", chain)
		}
	case <-time.After(time.Second):
		t.Fatal("nested call did not start")
	}
	cancel()
	var result extproto.ToolResultFromHost
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &result); err != nil || !result.IsError || !strings.Contains(result.Content[0].Text, "canceled") {
		t.Fatalf("parent cancellation: %+v, %v", result, err)
	}
}

func TestCallToolCancel(t *testing.T) {
	m, ext, frames, replies := toolPeer(t)
	ext.callTool = true
	started := make(chan struct{})
	m.SetToolCaller(func(ctx context.Context, _, _ string, _ json.RawMessage) extproto.ToolResultFromHost {
		close(started)
		<-ctx.Done()
		return extproto.ToolResultFromHost{IsError: true, Content: []extproto.ContentBlock{{Type: "text", Text: ctx.Err().Error()}}}
	})
	sendToolFrame(t, replies, extproto.CallToolFromExt{Type: "call_tool", ID: "one", Name: "read", Args: json.RawMessage(`{}`)})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("call did not start")
	}
	sendToolFrame(t, replies, extproto.Frame{Type: "call_tool_cancel", ID: "one"})
	var result extproto.ToolResultFromHost
	if err := json.Unmarshal(nextLifecycleFrame(t, frames), &result); err != nil || !result.IsError || !strings.Contains(result.Content[0].Text, "canceled") {
		t.Fatalf("cancel: %+v, %v", result, err)
	}
}
