package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/patriceckhart/zot/packages/agent/extproto"
)

type parentCallKey struct{}

// CallTool asks zot to invoke an active built-in or extension tool. Tool
// failures are returned as ToolResult.IsError, transport failures as err.
// Calls made from tool handlers inherit their parent call for cycle detection
// and cancellation.
func (e *Extension) CallTool(ctx context.Context, name string, args any) (ToolResult, error) {
	if !e.callToolReady.Load() {
		return ToolResult{}, fmt.Errorf("host does not support call_tool")
	}
	if ctx == nil {
		return ToolResult{}, fmt.Errorf("call_tool requires a context")
	}
	data, err := json.Marshal(args)
	if err != nil {
		return ToolResult{}, fmt.Errorf("encode tool args: %w", err)
	}
	if len(data) == 0 || data[0] != '{' {
		return ToolResult{}, fmt.Errorf("call_tool args must be a JSON object")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	id := fmt.Sprintf("sdk-%d", e.callSeq.Add(1))
	ch := make(chan extproto.ToolResultFromHost, 1)
	e.mu.Lock()
	e.pendingCalls[id] = ch
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.pendingCalls, id)
		e.mu.Unlock()
	}()
	parent, _ := ctx.Value(parentCallKey{}).(string)
	if err := e.send(extproto.CallToolFromExt{Type: "call_tool", ID: id, Name: name, Args: data, ParentID: parent}); err != nil {
		return ToolResult{}, err
	}
	select {
	case reply := <-ch:
		out := ToolResult{IsError: reply.IsError}
		for _, block := range reply.Content {
			out.Content = append(out.Content, ToolContent{Type: block.Type, Text: block.Text, MimeType: block.MimeType, Data: block.Data})
		}
		if reply.Truncated {
			out.Content = append(out.Content, Text("[tool result truncated by host]"))
		}
		return out, nil
	case <-ctx.Done():
		_ = e.send(extproto.Frame{Type: "call_tool_cancel", ID: id})
		return ToolResult{}, ctx.Err()
	case <-e.done:
		return ToolResult{}, fmt.Errorf("host disconnected")
	}
}
