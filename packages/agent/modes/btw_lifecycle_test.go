package modes

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

func TestBtwPreservesContextAwarePolicy(t *testing.T) {
	main := core.NewAgent(nil, "test", "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	main.BeforeToolExecuteContext = func(got context.Context, call provider.ToolCallBlock) (bool, string, json.RawMessage) {
		called = true
		if got != ctx || call.ID != "call" {
			t.Fatal("policy lost call context")
		}
		return false, "blocked", nil
	}
	side := newBtwAgent(main, "", "test")
	if side.BeforeToolExecuteContext == nil {
		t.Fatal("side chat bypasses policy")
	}
	allowed, reason, _ := side.BeforeToolExecuteContext(ctx, provider.ToolCallBlock{ID: "call"})
	if allowed || reason != "blocked" || !called {
		t.Fatal("policy decision changed")
	}
}
