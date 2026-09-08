package agent

import (
	"testing"

	"github.com/patriceckhart/zot/packages/provider"
)

func TestCopilotDefaultUsesSonnet5(t *testing.T) {
	id := defaultModelForProvider("github-copilot")
	if id != "claude-sonnet-5" {
		t.Fatalf("Copilot default = %q, want claude-sonnet-5", id)
	}
	if _, err := provider.FindModel("github-copilot", id); err != nil {
		t.Fatalf("Copilot default missing from catalog: %v", err)
	}
}
