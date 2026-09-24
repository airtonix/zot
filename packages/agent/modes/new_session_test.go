package modes

import (
	"context"
	"errors"
	"testing"

	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

func TestNewSessionUnavailableWithoutPersistence(t *testing.T) {
	i := NewInteractive(InteractiveConfig{})
	if i.slashCancelsActiveTurn("/new") {
		t.Fatal("unavailable /new would cancel the active turn")
	}
	i.runSlash(context.Background(), "/new")

	if i.statusErr != "sessions are disabled by --no-session" {
		t.Fatalf("status error = %q", i.statusErr)
	}
}

func TestNewSessionResetsConversationView(t *testing.T) {
	ag := core.NewAgent(nil, "model", "", nil)
	ag.SetMessages([]provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "old conversation"}},
	}})
	calls := 0
	var gotProvider, gotModel string
	gate := core.NewConfirmGate(nil)
	gate.AllowAll()
	i := NewInteractive(InteractiveConfig{
		Agent:       ag,
		Provider:    "test-provider",
		Model:       "active-model",
		ConfirmGate: gate,
		NewSession: func(providerName, model string) error {
			calls++
			gotProvider, gotModel = providerName, model
			ag.SetMessages(nil)
			return nil
		},
	})
	i.cumUsage = provider.Usage{InputTokens: 10}
	i.lastCtxInput = 10
	i.extNotes = []string{"old notice"}

	i.runSlash(context.Background(), "/new")

	if calls != 1 {
		t.Fatalf("NewSession calls = %d, want 1", calls)
	}
	if gotProvider != "test-provider" || gotModel != "active-model" {
		t.Fatalf("NewSession model = %q/%q", gotProvider, gotModel)
	}
	if len(ag.Messages()) != 0 || len(i.view.Messages) != 0 {
		t.Fatal("new session retained the previous transcript")
	}
	if i.cumUsage != (provider.Usage{}) || i.lastCtxInput != 0 {
		t.Fatalf("usage was not reset: cumulative=%+v context=%d", i.cumUsage, i.lastCtxInput)
	}
	if len(i.extNotes) != 0 {
		t.Fatalf("extension notices were not cleared: %v", i.extNotes)
	}
	if decision := gate.DecideToolCall(core.ToolCallConfirmation{Name: "bash"}); decision.Allow {
		t.Fatal("new session retained session-scoped confirmation approval")
	}
	if i.statusOK != "started new session" || i.statusErr != "" {
		t.Fatalf("status = %q, %q", i.statusOK, i.statusErr)
	}
}

func TestNewSessionFailureKeepsConversationView(t *testing.T) {
	ag := core.NewAgent(nil, "model", "", nil)
	messages := []provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "still active"}},
	}}
	ag.SetMessages(messages)
	i := NewInteractive(InteractiveConfig{
		Agent:      ag,
		NewSession: func(string, string) error { return errors.New("disk full") },
	})

	i.runSlash(context.Background(), "/new")

	if len(ag.Messages()) != 1 || len(i.view.Messages) != 1 {
		t.Fatal("failed session creation cleared the current transcript")
	}
	if i.statusErr != "start new session: disk full" {
		t.Fatalf("status error = %q", i.statusErr)
	}
}
