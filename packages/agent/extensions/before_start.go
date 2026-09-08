package extensions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/patriceckhart/zot/packages/agent/extproto"
)

// MaxSystemPromptResponseSize limits the encoded system_prompt value. The
// transport additionally limits each complete response frame to 4 MiB.
const MaxSystemPromptResponseSize = 1024 * 1024

// InterceptBeforeAgentStart chains exact prompt replacements in load order.
// Failures are advisory, never permission decisions.
func (m *Manager) InterceptBeforeAgentStart(ctx context.Context, ev extproto.EventInterceptFromHost) string {
	current := ""
	if ev.SystemPrompt != nil {
		current = *ev.SystemPrompt
	}
	ev.Event = "before_agent_start"
	_, ev.CWD = m.SessionContext()
	if ev.CWD == "" {
		ev.CWD = m.cwd
	}
	if ev.Provider == "" {
		ev.Provider = m.provider
	}
	m.mu.RLock()
	subs := append([]*Extension(nil), m.extOrder...)
	m.mu.RUnlock()
	for _, ext := range subs {
		if ctx.Err() != nil {
			break
		}
		ext.mu.Lock()
		_, subscribed := ext.interceptSubs[ev.Event]
		ext.mu.Unlock()
		if !subscribed {
			continue
		}
		ev.SystemPrompt = &current
		callCtx, cancel := context.WithTimeout(ctx, interceptTimeout)
		r := m.askIntercept(callCtx, ext, ev)
		cancel()
		failure := r.failure
		if failure == "" && r.Block && len(r.SystemPrompt) == 0 {
			failure = "extension returned a blocking response; startup hooks cannot block"
		}
		if failure == "" && len(r.SystemPrompt) > 0 {
			var replacement string
			switch {
			case len(r.SystemPrompt) > MaxSystemPromptResponseSize:
				failure = "system_prompt exceeds 1 MiB encoded limit"
			case r.SystemPrompt[0] != '"' || json.Unmarshal(r.SystemPrompt, &replacement) != nil:
				failure = "system_prompt must be a string"
			default:
				current = replacement
			}
		}
		if failure != "" {
			message := "before_agent_start: " + failure + "; keeping current system prompt"
			fmt.Fprintln(ext.logFile, "[zot] "+message)
			ext.recordDiagnostic(message)
			if m.hooks != nil {
				m.hooks.Notify(ext.Manifest.Name, "warn", message)
			}
		}
	}
	return current
}
