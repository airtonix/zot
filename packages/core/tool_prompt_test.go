package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/patriceckhart/zot/packages/provider"
)

type preludeClient struct {
	messages []provider.Message
	calls    int
}

func (c *preludeClient) Name() string { return "prelude" }
func (c *preludeClient) Stream(_ context.Context, req provider.Request) (<-chan provider.Event, error) {
	c.calls++
	c.messages = append([]provider.Message(nil), req.Messages...)
	ch := make(chan provider.Event, 1)
	ch <- provider.EventDone{Stop: provider.StopEnd, Message: provider.Message{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "done"}}}}
	close(ch)
	return ch, nil
}

func TestPromptWithToolPairsResultBeforeModelAndPersists(t *testing.T) {
	client := &preludeClient{}
	tool := &recordingTool{}
	a := NewAgent(client, "test", "", Registry{"echo": tool})
	session, err := NewSession(t.TempDir(), t.TempDir(), "test", "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	a.OnMessageAppended = func(msg provider.Message) {
		if err := session.AppendMessage(msg); err != nil {
			t.Error(err)
		}
	}
	call := provider.ToolCallBlock{ID: "ext-prompt-1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}
	var events []AgentEvent
	if err := a.PromptWithTool(context.Background(), "and push", call, "my-ext", func(ev AgentEvent) { events = append(events, ev) }); err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 || len(client.messages) != 3 || client.messages[0].Role != provider.RoleUser || client.messages[1].Role != provider.RoleAssistant || client.messages[2].Role != provider.RoleTool {
		t.Fatalf("model input: calls=%d messages=%+v", client.calls, client.messages)
	}
	gotCall := client.messages[1].Content[0].(provider.ToolCallBlock)
	gotResult := client.messages[2].Content[0].(provider.ToolResultBlock)
	if gotCall.ID != gotResult.CallID || gotResult.IsError || client.messages[1].Meta["origin_extension"] != "my-ext" || string(tool.lastArgs) != string(call.Arguments) {
		t.Fatalf("call/result mismatch: %v %v", gotCall, gotResult)
	}
	if _, ok := events[1].(EvToolCall); !ok {
		t.Fatalf("missing call event: %T", events[1])
	}
	if _, ok := events[2].(EvToolResult); !ok {
		t.Fatalf("missing result event: %T", events[2])
	}
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, msgs, err := OpenSession(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if len(msgs) < 3 || msgs[1].Meta["origin_extension"] != "my-ext" || msgs[1].Meta["synthetic_tool_call"] != "true" || msgs[2].Content[0].(provider.ToolResultBlock).CallID != gotCall.ID {
		t.Fatalf("restored transcript: %+v", msgs)
	}
}

func TestPromptWithToolCancellationKeepsPair(t *testing.T) {
	client := &preludeClient{}
	tool := &blockingTool{started: make(chan struct{}), release: make(chan struct{})}
	a := NewAgent(client, "test", "", Registry{"echo": tool})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- a.PromptWithTool(ctx, "request", provider.ToolCallBlock{ID: "id", Name: "echo", Arguments: json.RawMessage(`{}`)}, "ext", nil)
	}()
	<-tool.started
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("error = %v", err)
	}
	msgs := a.Messages()
	if client.calls != 0 || len(msgs) != 3 || msgs[1].Content[0].(provider.ToolCallBlock).ID != msgs[2].Content[0].(provider.ToolResultBlock).CallID || !msgs[2].Content[0].(provider.ToolResultBlock).IsError {
		t.Fatalf("cancelled transcript: calls=%d messages=%+v", client.calls, msgs)
	}
}

func TestPromptWithToolDeniedAndInvalid(t *testing.T) {
	client := &preludeClient{}
	tool := &recordingTool{}
	a := NewAgent(client, "test", "", Registry{"echo": tool})
	a.BeforeToolExecute = func(provider.ToolCallBlock) (bool, string, json.RawMessage) { return false, "denied", nil }
	call := provider.ToolCallBlock{ID: "id", Name: "echo", Arguments: json.RawMessage(`{}`)}
	if err := a.PromptWithTool(context.Background(), "request", call, "ext", nil); err != nil {
		t.Fatal(err)
	}
	if tool.lastArgs != nil || !client.messages[2].Content[0].(provider.ToolResultBlock).IsError {
		t.Fatalf("guard bypassed: %+v", client.messages)
	}
	before := len(a.Messages())
	call.Arguments = json.RawMessage(`[]`)
	if err := a.PromptWithTool(context.Background(), "request", call, "ext", nil); err == nil || len(a.Messages()) != before {
		t.Fatalf("invalid input mutated transcript: %v", err)
	}
}
