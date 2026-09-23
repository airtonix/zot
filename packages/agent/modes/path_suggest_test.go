package modes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/tui"
)

func newPathChoiceTestInteractive(t *testing.T) *Interactive {
	t.Helper()
	tmp := t.TempDir()
	for _, name := range []string{"foobar", "foobuz"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	interactive := NewInteractive(InteractiveConfig{CWD: tmp})
	interactive.ed.SetValue("./foo")
	if !interactive.tryPathTabComplete() {
		t.Fatal("first Tab was not consumed")
	}
	if !interactive.tryPathTabComplete() {
		t.Fatal("second Tab was not consumed")
	}
	return interactive
}

func TestPathTabCompletionShowsChoicesAfterCommonPrefix(t *testing.T) {
	interactive := newPathChoiceTestInteractive(t)
	ed := interactive.ed
	if got, want := ed.Value(), "./foob"; got != want {
		t.Fatalf("first Tab completed to %q, want %q", got, want)
	}

	popup := interactive.pathSuggest
	if !popup.Active(ed.Value()) {
		t.Fatal("path choice popup is not active for the unchanged token")
	}
	rows := popup.Render(tui.Theme{}, 40)
	rendered := stripANSIBytes(strings.Join(rows, "\n"))
	for _, name := range []string{"foobar", "foobuz"} {
		if !strings.Contains(rendered, name) {
			t.Fatalf("popup omitted %q: %q", name, rendered)
		}
	}

	if interactive.handleKey(context.Background(), tui.Key{Kind: tui.KeyDown}) {
		t.Fatal("navigating the path popup unexpectedly exited interactive mode")
	}
	if interactive.handleKey(context.Background(), tui.Key{Kind: tui.KeyEnter}) {
		t.Fatal("selecting a path choice unexpectedly exited interactive mode")
	}
	if got, want := ed.Value(), "./foobuz"; got != want {
		t.Fatalf("selected path is %q, want %q", got, want)
	}
}

func TestPathChoiceEscDoesNotCancelBusyTurn(t *testing.T) {
	interactive := newPathChoiceTestInteractive(t)
	interactive.busy = true
	canceled := false
	interactive.cancelTurn = func() { canceled = true }

	interactive.handleKey(context.Background(), tui.Key{Kind: tui.KeyEsc})

	if canceled {
		t.Fatal("Esc canceled the active turn instead of dismissing the path popup")
	}
	if interactive.pathSuggest.Active(interactive.ed.Value()) {
		t.Fatal("Esc left the path popup active")
	}
}

func TestPathChoiceDoesNotReopenAfterInputCleared(t *testing.T) {
	interactive := newPathChoiceTestInteractive(t)

	interactive.handleKey(context.Background(), tui.Key{Kind: tui.KeyCtrlC})
	for _, r := range "./foob" {
		interactive.handleKey(context.Background(), tui.Key{Kind: tui.KeyRune, Rune: r})
	}

	if interactive.pathSuggest.Active(interactive.ed.Value()) {
		t.Fatal("stale path popup reopened after Ctrl+C and retyping")
	}
}
