package agent

import (
	"testing"

	"github.com/patriceckhart/zot/packages/provider"
)

func TestRefreshCopilotAvailabilityWithoutCredentials(t *testing.T) {
	t.Setenv("ZOT_HOME", t.TempDir())
	t.Setenv("COPILOT_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_COPILOT_TOKEN", "")
	provider.SetModelAvailability("github-copilot", []string{})
	t.Cleanup(func() { provider.SetModelAvailability("github-copilot", nil) })
	refreshCopilotModelAvailability()
	if len(provider.ModelsForProvider("github-copilot")) == 0 {
		t.Fatal("account restriction was retained after credentials disappeared")
	}
}
