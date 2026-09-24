package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/patriceckhart/zot/packages/agent/modes"
	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

func TestLiveInteractiveAgentUsesReplacementAgentForSessionResume(t *testing.T) {
	startup := core.NewAgent(nil, "startup-model", "", nil)
	startup.SetMessages([]provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "startup transcript"}},
	}})

	replacement := core.NewAgent(nil, "replacement-model", "", nil)
	iv := modes.NewInteractive(modes.InteractiveConfig{Agent: replacement})

	resumed := []provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "resumed transcript"}},
	}}
	liveInteractiveAgent(iv, startup).SetMessages(resumed)

	if got := firstMessageText(replacement.Messages()); got != "resumed transcript" {
		t.Fatalf("replacement agent transcript = %q, want resumed transcript", got)
	}
	if got := firstMessageText(startup.Messages()); got != "startup transcript" {
		t.Fatalf("startup agent transcript changed to %q", got)
	}
}

func TestBindAgentSession(t *testing.T) {
	ag := core.NewAgent(nil, "@preset/flash", "", nil)
	bindAgentSession(ag, &core.Session{ID: "sess-xyz"})
	if ag.SessionID != "sess-xyz" {
		t.Fatalf("SessionID = %q; want sess-xyz", ag.SessionID)
	}
	bindAgentSession(ag, &core.Session{ID: "other"})
	if ag.SessionID != "other" {
		t.Fatalf("SessionID = %q; want other after rebind", ag.SessionID)
	}
	bindAgentSession(ag, nil)
	if ag.SessionID != "other" {
		t.Fatalf("nil session mutated SessionID to %q", ag.SessionID)
	}
}

func TestRotateInteractiveSessionPreservesOldTranscriptAndResetsAgent(t *testing.T) {
	root := t.TempDir()
	cwd := t.TempDir()
	old, err := core.NewSession(root, cwd, "test-provider", "old-model", "test")
	if err != nil {
		t.Fatal(err)
	}
	ag := core.NewAgent(nil, "new-model", "", nil)
	ag.SetMessages([]provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "keep this conversation"}},
	}})
	ag.SeedCost(provider.Usage{InputTokens: 12, CostUSD: 0.25})
	ag.SeedLastTurnUsage(provider.Usage{InputTokens: 12})
	ag.QueueMessage("drop queued prompt")

	fresh, err := rotateInteractiveSession(root, cwd, "test-provider", "new-model", "test", ag, old, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if fresh.ID == old.ID || fresh.Path == old.Path {
		t.Fatalf("session was not rotated: old=%s new=%s", old.Path, fresh.Path)
	}
	if ag.SessionID != fresh.ID {
		t.Fatalf("agent session ID = %q, want %q", ag.SessionID, fresh.ID)
	}
	if len(ag.Messages()) != 0 {
		t.Fatalf("new transcript is not empty: %+v", ag.Messages())
	}
	if got := ag.Cost(); got != (provider.Usage{}) {
		t.Fatalf("new session cost = %+v, want zero", got)
	}
	if got := ag.LastTurnUsage(); got != (provider.Usage{}) {
		t.Fatalf("new session last usage = %+v, want zero", got)
	}
	if got := ag.QueuedMessageCount(); got != 0 {
		t.Fatalf("queued messages = %d, want zero", got)
	}

	persisted, messages, err := core.OpenSession(old.Path)
	if err != nil {
		t.Fatalf("open previous session: %v", err)
	}
	defer persisted.Close()
	if got := firstMessageText(messages); got != "keep this conversation" {
		t.Fatalf("previous transcript = %q", got)
	}
}

func TestRotateInteractiveSessionCreationFailureLeavesAgentUntouched(t *testing.T) {
	ag := core.NewAgent(nil, "model", "", nil)
	ag.SetMessages([]provider.Message{{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "still active"}}}})
	blockedRoot := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blockedRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := rotateInteractiveSession(blockedRoot, t.TempDir(), "test", "model", "test", ag, nil, 0); err == nil {
		t.Fatal("rotateInteractiveSession returned nil error for invalid root")
	}
	if got := firstMessageText(ag.Messages()); got != "still active" {
		t.Fatalf("agent transcript changed to %q after failed rotation", got)
	}
}

func TestLiveInteractiveAgentFallsBackBeforeInteractiveConstruction(t *testing.T) {
	startup := core.NewAgent(nil, "startup-model", "", nil)
	if got := liveInteractiveAgent(nil, startup); got != startup {
		t.Fatalf("fallback agent = %p, want %p", got, startup)
	}
}

func firstMessageText(messages []provider.Message) string {
	if len(messages) == 0 || len(messages[0].Content) == 0 {
		return ""
	}
	text, _ := messages[0].Content[0].(provider.TextBlock)
	return text.Text
}
