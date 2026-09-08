package provider

import (
	"context"
	"strings"
)

type copilotRequestKey struct{}
type copilotRequestMetadata struct {
	initiator string
	vision    bool
}

type copilotClient struct {
	router *modelRouter
}

func (c *copilotClient) Name() string { return "github-copilot" }

func (c *copilotClient) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	metadata := copilotRequestMetadata{initiator: "user"}
	if len(req.Messages) > 0 && req.Messages[len(req.Messages)-1].Role != RoleUser {
		metadata.initiator = "agent"
	}
	for _, message := range req.Messages {
		if message.Role == RoleUser || message.Role == RoleTool {
			metadata.vision = metadata.vision || copilotHasImages(message.Content)
		}
	}
	// These endpoints perform their own thinking; they reject reasoning_effort.
	api := copilotModelAPI(req.Model)
	if model, err := FindModel(c.Name(), req.Model); err == nil && model.API != "" {
		api = model.API
	}
	if api == APICompletions {
		req.Reasoning = ""
	}
	ctx = context.WithValue(ctx, copilotRequestKey{}, metadata)
	// Use family routing for custom models that have not supplied an API tag.
	if model, err := FindModel(c.Name(), req.Model); err != nil || model.API == "" {
		return c.router.byAPI[api].Stream(ctx, req)
	}
	return c.router.Stream(ctx, req)
}

func copilotHasImages(blocks []Content) bool {
	for _, block := range blocks {
		switch b := block.(type) {
		case ImageBlock:
			return true
		case ToolResultBlock:
			if copilotHasImages(b.Content) {
				return true
			}
		}
	}
	return false
}

func copilotModelAPI(id string) string {
	switch {
	case strings.HasPrefix(id, "claude-"):
		return APIAnthropicMessages
	case strings.HasPrefix(id, "gpt-"), strings.HasPrefix(id, "grok-"), strings.HasPrefix(id, "mai-"), strings.HasPrefix(id, "oswe"):
		return APIResponses
	default:
		return APICompletions
	}
}

func configureCopilotModel(m *Model) {
	m.API = copilotModelAPI(m.ID)
	if m.API == APIAnthropicMessages && !strings.HasPrefix(m.ID, "claude-haiku-") {
		m.AdaptiveThinking = true
	}
	if m.API == APICompletions {
		m.ReasoningLevelMap = map[string]string{"minimum": "", "low": "", "medium": "", "high": "", "xhigh": "", "max": ""}
	}
	// Responses defaults expose xhigh, but these models only support low/medium/high.
	if m.ID == "gpt-5-mini" || m.ID == "grok-4.5" || strings.HasPrefix(m.ID, "mai-") {
		m.ReasoningLevelMap = map[string]string{"xhigh": "", "max": ""}
	}
}
