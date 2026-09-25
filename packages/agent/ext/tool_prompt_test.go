package ext

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/agent/extproto"
)

func TestToolPromptResponse(t *testing.T) {
	h := newHarness("shortcut")
	h.ext.Command("commit", "commit", func(args string) Response {
		return ToolPrompt("skill", map[string]string{"name": "developer:commit"}, args)
	})
	done := make(chan error, 1)
	go func() { done <- h.ext.Run() }()
	t.Cleanup(func() {
		_ = h.hostW.Close()
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
	h.sendToExt(t, extproto.HelloAckFromHost{Type: "hello_ack"})
	h.drainUntil(t, "ready")
	h.sendToExt(t, extproto.CommandInvokedFromHost{Type: "command_invoked", ID: "1", Name: "commit", Args: "and push"})
	frame := h.drainUntil(t, "command_response")
	var decoded extproto.CommandResponseFromExt
	if err := json.Unmarshal(frame.raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != "1" || decoded.Action != "tool_prompt" || decoded.Prompt != "and push" || decoded.ToolName != "skill" || string(decoded.ToolArgs) != `{"name":"developer:commit"}` {
		t.Fatalf("response = %+v", decoded)
	}
	if got := ToolPrompt("skill", []string{"not", "object"}, "request"); got.Error == "" {
		t.Fatalf("invalid args accepted: %+v", got)
	}
}
