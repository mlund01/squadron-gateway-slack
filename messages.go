package main

import (
	"context"
	"fmt"

	gatewaysdk "github.com/mlund01/squadron-gateway-sdk"
	"github.com/slack-go/slack"
)

// postQuestion posts a fresh request to Slack. Idempotent on
// tool_call_id so catch-up replays don't double-post.
func (g *slackGateway) postQuestion(ctx context.Context, rec gatewaysdk.HumanInputRecord) error {
	g.mu.Lock()
	if _, exists := g.messages[rec.ToolCallID]; exists {
		g.mu.Unlock()
		return nil
	}
	client := g.client
	channel := g.channelID
	g.mu.Unlock()
	if client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	blocks := buildBlocks(rec)
	_, ts, err := client.PostMessageContext(ctx, channel,
		slack.MsgOptionText(buildFallbackText(rec), false),
		slack.MsgOptionBlocks(blocks...),
	)
	if err != nil {
		return fmt.Errorf("post message: %w", err)
	}

	g.mu.Lock()
	g.messages[rec.ToolCallID] = ts
	g.mu.Unlock()

	g.advanceCheckpoint(rec.RequestedAt)
	return nil
}

// markResolved edits the original message: strikethrough question +
// ✅ answer, blocks cleared so a stale click can't re-resolve.
// No-op if we never posted this question.
func (g *slackGateway) markResolved(ctx context.Context, rec gatewaysdk.HumanInputRecord) error {
	g.mu.Lock()
	ts, ok := g.messages[rec.ToolCallID]
	client := g.client
	channel := g.channelID
	g.mu.Unlock()
	if !ok || client == nil {
		return nil
	}

	body := buildResolvedBody(rec)
	resolvedBlocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, body, false, false),
			nil, nil,
		),
	}
	if _, _, _, err := client.UpdateMessageContext(ctx, channel, ts,
		slack.MsgOptionText(body, false),
		slack.MsgOptionBlocks(resolvedBlocks...),
	); err != nil {
		return fmt.Errorf("update message: %w", err)
	}
	return nil
}
