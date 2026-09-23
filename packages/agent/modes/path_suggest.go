package modes

import "github.com/patriceckhart/zot/packages/tui"

// pathChoicePopup is shown after a repeated Tab reaches an ambiguous
// filesystem prefix. It deliberately owns only the transient choice state;
// the editor remains the source of truth for the prompt text.
type pathChoicePopup struct {
	completion pathCompletionResult
	cursor     int
}

func newPathChoicePopup() *pathChoicePopup { return &pathChoicePopup{} }

func (p *pathChoicePopup) Open(completion pathCompletionResult) {
	if p == nil {
		return
	}
	p.completion = completion
	p.cursor = 0
}

func (p *pathChoicePopup) Reset() {
	if p == nil {
		return
	}
	p.completion = pathCompletionResult{}
	p.cursor = 0
}

func (p *pathChoicePopup) Active(input string) bool {
	if p == nil || len(p.completion.entries) <= 1 {
		return false
	}
	start := len(input)
	for start > 0 {
		r := input[start-1]
		if r == ' ' || r == '\t' || r == '\n' {
			break
		}
		start--
	}
	if start != p.completion.tokenStart || input[start:] != p.completion.token {
		p.Reset()
		return false
	}
	return true
}

func (p *pathChoicePopup) Up() {
	if p.cursor > 0 {
		p.cursor--
	}
}

func (p *pathChoicePopup) Down() {
	if p.cursor < len(p.completion.entries)-1 {
		p.cursor++
	}
}

// Select replaces the unchanged path token with the highlighted match. It
// returns false when the editor no longer contains the token that opened the
// popup, in which case the caller should let the key continue normally.
func (p *pathChoicePopup) Select(ed *tui.Editor) bool {
	if ed == nil || !p.Active(ed.Value()) {
		return false
	}
	if p.cursor < 0 || p.cursor >= len(p.completion.entries) {
		p.cursor = 0
	}
	entry := p.completion.entries[p.cursor]
	newToken := p.completion.displayParent + entry.name
	if entry.isDir {
		newToken += "/"
	}
	value := ed.Value()
	ed.SetValue(value[:p.completion.tokenStart] + newToken)
	p.Reset()
	return true
}

// Render returns the candidate list and a short key hint. The list is kept
// intentionally small and plain: it complements the existing slash and @
// popups without introducing a second completion model or a new dependency.
func (p *pathChoicePopup) Render(th tui.Theme, width int) []string {
	if p == nil || len(p.completion.entries) <= 1 {
		return nil
	}
	header := "  " + p.completion.displayParent + p.completion.basePrefix
	lines := []string{th.FG256(th.Muted, header), ""}
	const maxVisible = 12
	visible := p.completion.entries
	offset := 0
	if len(visible) > maxVisible {
		if p.cursor >= maxVisible {
			offset = p.cursor - maxVisible + 1
		}
		end := offset + maxVisible
		if end > len(visible) {
			end = len(visible)
			offset = end - maxVisible
		}
		visible = visible[offset:end]
	}
	for idx, entry := range visible {
		idx += offset
		name := entry.name
		if entry.isDir {
			name += "/"
		}
		plain := "  " + name
		if idx == p.cursor {
			lines = append(lines, th.PadHighlight(plain, width))
		} else {
			lines = append(lines, th.FG256(th.Muted, plain))
		}
	}
	lines = append(lines, "", th.FG256(th.Muted, "  ↑/↓ navigate - tab/enter complete - esc cancel"), "")
	return lines
}
