package modes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/agent/ext"
	"github.com/patriceckhart/zot/packages/agent/extensions"
	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

type toolPromptClient struct{ requests chan provider.Request }

func (*toolPromptClient) Name() string { return "tool-prompt-test" }
func (c *toolPromptClient) Stream(_ context.Context, req provider.Request) (<-chan provider.Event, error) {
	c.requests <- req
	out := make(chan provider.Event, 1)
	out <- provider.EventDone{Stop: provider.StopEnd, Message: provider.Message{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "done"}}}}
	close(out)
	return out, nil
}

type shortcutTool struct{}

func (*shortcutTool) Name() string            { return "skill" }
func (*shortcutTool) Description() string     { return "skill" }
func (*shortcutTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (*shortcutTool) Execute(context.Context, json.RawMessage, func(string)) (core.ToolResult, error) {
	return core.ToolResult{Content: []provider.Content{provider.TextBlock{Text: "instructions"}}}, nil
}

// TestToolPromptExtensionProcess runs only as a child started by the manager.
func TestToolPromptExtensionProcess(t *testing.T) {
	if os.Getenv("ZOT_TOOL_PROMPT_CHILD") != "1" {
		return
	}
	e := ext.New("shortcut", "test")
	e.Command("commit", "commit", func(args string) ext.Response {
		return ext.ToolPrompt("skill", map[string]string{"name": "developer:commit"}, args)
	})
	if err := e.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestToolPromptExtensionCommandWithLocalGemini(t *testing.T) {
	root := t.TempDir()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "extensions", "shortcut")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(map[string]any{"name": "shortcut", "version": "test", "exec": path, "args": []string{"-test.run=^TestToolPromptExtensionProcess$"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extension.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZOT_TOOL_PROMPT_CHILD", "1")
	mgr := extensions.New(root, "", "test", "google", "gemini-3-flash-preview", nil)
	if errs := mgr.Discover(context.Background()); len(errs) > 0 {
		t.Fatal(errs)
	}
	t.Cleanup(func() { mgr.Stop(time.Second) })
	mgr.WaitForReady(2 * time.Second)
	if !mgr.HasCommand("commit") {
		t.Fatal("extension command not registered")
	}
	requests := make(chan json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		requests <- raw
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: "+`{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}`+"\n\n")
	}))
	defer server.Close()
	agent := core.NewAgent(provider.NewGemini("synthetic-test-key", server.URL), "gemini-3-flash-preview", "", core.Registry{"skill": &shortcutTool{}})
	iv := NewInteractive(InteractiveConfig{Agent: agent, Extensions: mgr})
	iv.runCtx = context.Background()
	iv.invokeExtensionCommand(context.Background(), "commit", "and push")
	select {
	case raw := <-requests:
		var wire struct {
			Contents []struct {
				Parts []struct {
					Text         string `json:"text"`
					Signature    string `json:"thoughtSignature"`
					FunctionCall *struct {
						Name string `json:"name"`
					} `json:"functionCall"`
					FunctionResponse *struct {
						Response map[string]string `json:"response"`
					} `json:"functionResponse"`
				} `json:"parts"`
			} `json:"contents"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if len(wire.Contents) != 3 || wire.Contents[0].Parts[0].Text != "and push" || wire.Contents[1].Parts[0].FunctionCall == nil || wire.Contents[1].Parts[0].FunctionCall.Name != "skill" || wire.Contents[1].Parts[0].Signature != "skip_thought_signature_validator" || wire.Contents[2].Parts[0].FunctionResponse == nil || wire.Contents[2].Parts[0].FunctionResponse.Response["output"] != "instructions" {
			t.Fatalf("Gemini request: %+v", wire)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("extension command did not send Gemini request")
	}
}

// This exercises the interactive prelude, core transcript, Gemini request
// serialization, streaming, and session replay without contacting Google.
func TestToolPromptGeminiLocalRoundTripAndResume(t *testing.T) {
	requests := make(chan []byte, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- raw
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: "+`{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}`+"\n\n")
	}))
	defer server.Close()

	client := provider.NewGemini("synthetic-test-key", server.URL)
	agent := core.NewAgent(client, "gemini-3-flash-preview", "", core.Registry{"skill": &shortcutTool{}})
	session, err := core.NewSession(t.TempDir(), t.TempDir(), "google", "gemini-3-flash-preview", "test")
	if err != nil {
		t.Fatal(err)
	}
	agent.OnMessageAppended = func(m provider.Message) {
		if err := session.AppendMessage(m); err != nil {
			t.Error(err)
		}
	}
	iv := NewInteractive(InteractiveConfig{Agent: agent, Extensions: &extensions.Manager{}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	iv.startTurnWithPrelude(ctx, "and push", nil, &toolPromptRequest{
		call: provider.ToolCallBlock{ID: "ext-prompt-local", Name: "skill", Arguments: json.RawMessage(`{"name":"developer:commit"}`)}, origin: "shortcut",
	})

	var wire struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text         string `json:"text"`
				Signature    string `json:"thoughtSignature"`
				FunctionCall *struct {
					Name string `json:"name"`
				} `json:"functionCall"`
				FunctionResponse *struct {
					Name     string            `json:"name"`
					Response map[string]string `json:"response"`
				} `json:"functionResponse"`
			} `json:"parts"`
		} `json:"contents"`
	}
	select {
	case raw := <-requests:
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Gemini request not sent")
	}
	if len(wire.Contents) != 3 || wire.Contents[0].Parts[0].Text != "and push" || wire.Contents[1].Role != "model" || wire.Contents[1].Parts[0].FunctionCall == nil || wire.Contents[1].Parts[0].FunctionCall.Name != "skill" || wire.Contents[1].Parts[0].Signature != "skip_thought_signature_validator" || wire.Contents[2].Parts[0].FunctionResponse == nil || wire.Contents[2].Parts[0].FunctionResponse.Response["output"] != "instructions" {
		t.Fatalf("Gemini request did not contain one matched synthetic tool exchange: %+v", wire)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		iv.mu.Lock()
		busy := iv.busy
		iv.mu.Unlock()
		if !busy {
			break
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatal("Gemini turn did not finish")
		}
	}
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, msgs, err := core.OpenSession(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if len(msgs) != 4 || msgs[1].Meta["synthetic_tool_call"] != "true" {
		t.Fatalf("restored transcript: %+v", msgs)
	}
	resumed := core.NewAgent(client, "gemini-3-flash-preview", "", core.Registry{"skill": &shortcutTool{}})
	resumed.SetMessages(msgs)
	if err := resumed.Prompt(ctx, "continue", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-requests:
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("resumed Gemini request not sent")
	}
	if len(wire.Contents) != 5 || wire.Contents[1].Parts[0].Signature != "skip_thought_signature_validator" || wire.Contents[2].Parts[0].FunctionResponse.Response["output"] != "instructions" || wire.Contents[4].Parts[0].Text != "continue" {
		t.Fatalf("Gemini replay: %+v", wire)
	}
}

func TestToolPromptSurvivesPreTurnCompaction(t *testing.T) {
	client := &compactQueueClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
		followUpRequest:   make(chan provider.Request, 1),
	}
	agent := core.NewAgent(client, "test-model", "", core.Registry{"skill": &shortcutTool{}})
	agent.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "one"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "two"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "three"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "four"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "five"}}},
	})
	threshold := 70
	iv := NewInteractive(InteractiveConfig{Agent: agent, Extensions: &extensions.Manager{}, Provider: "anthropic", Model: "claude-sonnet-4-5", AutoCompactThreshold: &threshold})
	iv.runCtx = context.Background()
	iv.lastCtxInput = 150000
	iv.startTurnWithPrelude(context.Background(), "and push", nil, &toolPromptRequest{call: provider.ToolCallBlock{ID: "ext-prompt-2", Name: "skill", Arguments: json.RawMessage(`{}`)}, origin: "shortcut"})
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("compaction did not start")
	}
	close(client.releaseCompaction)
	select {
	case req := <-client.followUpRequest:
		if requestUserTextCount(req, "and push") != 1 || len(req.Messages) < 3 || req.Messages[len(req.Messages)-2].Content[0].(provider.ToolCallBlock).Name != "skill" || req.Messages[len(req.Messages)-1].Content[0].(provider.ToolResultBlock).Content[0].(provider.TextBlock).Text != "instructions" {
			t.Fatalf("prelude lost after compaction: %+v", req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("model request not sent")
	}
}

func TestToolPromptStartsModelAfterToolResult(t *testing.T) {
	client := &toolPromptClient{requests: make(chan provider.Request, 1)}
	agent := core.NewAgent(client, "test-model", "", core.Registry{"skill": &shortcutTool{}})
	iv := NewInteractive(InteractiveConfig{Agent: agent, Extensions: &extensions.Manager{}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	iv.startTurnWithPrelude(ctx, "and push", nil, &toolPromptRequest{
		call: provider.ToolCallBlock{ID: "ext-prompt-1", Name: "skill", Arguments: json.RawMessage(`{"name":"developer:commit"}`)}, origin: "shortcut",
	})
	select {
	case req := <-client.requests:
		if len(req.Messages) != 3 || req.Messages[0].Role != provider.RoleUser || req.Messages[1].Role != provider.RoleAssistant || req.Messages[2].Role != provider.RoleTool || req.Messages[2].Content[0].(provider.ToolResultBlock).Content[0].(provider.TextBlock).Text != "instructions" {
			t.Fatalf("model input: %+v", req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("model request not sent")
	}
}
