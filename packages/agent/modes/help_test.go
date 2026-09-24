package modes

import (
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/tui"
)

func TestHelpShowsLlamaOnlyWhenConfigured(t *testing.T) {
	without := strings.Join(renderHelpBlock(tui.Theme{}, 80, false, true, nil), "\n")
	if strings.Contains(without, "/llama") {
		t.Fatalf("help exposed /llama without login: %q", without)
	}
	with := strings.Join(renderHelpBlock(tui.Theme{}, 80, true, true, nil), "\n")
	if !strings.Contains(with, "/llama") {
		t.Fatalf("help omitted /llama with login: %q", with)
	}
}

func TestHelpHidesNewWhenSessionsAreDisabled(t *testing.T) {
	without := strings.Join(renderHelpBlock(tui.Theme{}, 80, false, false, nil), "\n")
	if strings.Contains(without, "/new") {
		t.Fatalf("help exposed /new with sessions disabled: %q", without)
	}
	with := strings.Join(renderHelpBlock(tui.Theme{}, 80, false, true, nil), "\n")
	if !strings.Contains(with, "/new") {
		t.Fatalf("help omitted /new with sessions enabled: %q", with)
	}
}

func TestHelpShowsConfiguredCustomKeys(t *testing.T) {
	bindings, issues := compileKeymap(map[string]string{
		"Ctrl+S": "/skill:review",
		"bogus":  "/help",
	})
	if len(issues) != 1 {
		t.Fatalf("compileKeymap issues = %v, want one for bogus", issues)
	}
	help := strings.Join(renderHelpBlock(tui.Theme{}, 80, false, true, bindings), "\n")
	if !strings.Contains(help, "custom keys:") || !strings.Contains(help, "ctrl+s") || !strings.Contains(help, "/skill:review") {
		t.Fatalf("help omitted configured custom key: %q", help)
	}
	if strings.Contains(help, "bogus") {
		t.Fatalf("help advertised an invalid binding: %q", help)
	}
}
