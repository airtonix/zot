package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/patriceckhart/zot/packages/agent/extensions"
	"github.com/patriceckhart/zot/packages/agent/extproto"
	"github.com/patriceckhart/zot/packages/core"
)

var extensionCallSeq atomic.Uint64

type extensionOriginKey struct{}

// extensionToolCaller keeps host-initiated calls out of model transcripts,
// while sharing the active agent's guard and confirmation path.
func extensionToolCaller(mgr *extensions.Manager, active func() *core.Agent) func(context.Context, string, string, json.RawMessage) extproto.ToolResultFromHost {
	return func(ctx context.Context, origin, name string, args json.RawMessage) extproto.ToolResultFromHost {
		out := extproto.ToolResultFromHost{Name: name}
		ag := active()
		if ag == nil {
			out.IsError = true
			out.Content = []extproto.ContentBlock{{Type: "text", Text: "no active agent"}}
			return out
		}
		id := fmt.Sprintf("ext-call-%d", extensionCallSeq.Add(1))
		defer mgr.TrackToolCall(id, origin)()
		sink := func(ev core.AgentEvent) {
			switch e := ev.(type) {
			case core.EvToolCall:
				mgr.EmitEvent(extproto.EventFromHost{Event: "tool_call", OriginExtension: origin, ToolID: e.ID, ToolName: e.Name, ToolArgs: e.Args})
			case core.EvToolResult:
				mgr.EmitEvent(extproto.EventFromHost{Event: "tool_result", OriginExtension: origin, ToolID: e.ID, ToolName: e.Name, ToolArgs: e.Args, Status: e.Status, Executed: &e.Executed, Result: extensionEventResult(e.Result)})
			}
		}
		res := ag.CallTool(context.WithValue(ctx, extensionOriginKey{}, origin), id, name, args, sink)
		visible := extensionEventResult(res)
		out.Content, out.IsError, out.Truncated = visible.Content, visible.IsError, visible.Truncated
		return out
	}
}
