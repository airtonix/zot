package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/agent/extensions"
	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

func TestExtensionToolCallerHonorsGuardAndTranscript(t *testing.T) {
	tool := &lifecycleTool{}
	ag := core.NewAgent(nil, "test", "", core.Registry{"probe": tool})
	mgr := extensions.New(t.TempDir(), t.TempDir(), "test", "", "", nil)
	var callID string
	ag.BeforeToolExecuteContext = func(_ context.Context, call provider.ToolCallBlock) (bool, string, json.RawMessage) {
		callID = call.ID
		if got := mgr.ToolCallOrigin(call.ID); got != "caller" {
			t.Errorf("call origin = %q", got)
		}
		return false, "denied by policy", nil
	}
	call := extensionToolCaller(mgr, func() *core.Agent { return ag })
	out := call(context.Background(), "caller", "probe", json.RawMessage(`{}`))
	if !out.IsError || len(out.Content) != 1 || !strings.Contains(out.Content[0].Text, "denied by policy") {
		t.Fatalf("policy result: %+v", out)
	}
	if mgr.ToolCallOrigin(callID) != "" {
		t.Fatal("call origin was not cleared")
	}
	if tool.args != nil || len(ag.Messages()) != 0 {
		t.Fatalf("tool ran or transcript changed: args=%s messages=%v", tool.args, ag.Messages())
	}
	out = call(context.Background(), "caller", "missing", json.RawMessage(`{}`))
	if !out.IsError || !strings.Contains(out.Content[0].Text, "unknown tool") {
		t.Fatalf("unknown tool: %+v", out)
	}
}
