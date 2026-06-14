package main

import (
	"context"
	"fmt"
	"log"
	"strings"

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

// postNotification posts a one-way mission-lifecycle notification. No
// idempotency map — there is nothing to track or edit. A per-mission channel
// override is resolved here, falling back to the configured default channel.
func (g *slackGateway) postNotification(ctx context.Context, rec gatewaysdk.NotificationRecord) error {
	g.mu.Lock()
	client := g.client
	channel := g.channelID
	g.mu.Unlock()
	if client == nil {
		return fmt.Errorf("slack client not initialized")
	}
	if rec.Channel != "" {
		channel = g.resolveNotifyChannel(ctx, client, rec.Channel, channel)
	}

	body := buildNotificationBody(rec)
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, body, false, false),
			nil, nil,
		),
	}
	if _, _, err := client.PostMessageContext(ctx, channel,
		slack.MsgOptionText(body, false),
		slack.MsgOptionBlocks(blocks...),
	); err != nil {
		return fmt.Errorf("post notification: %w", err)
	}
	return nil
}

// postText posts a free-form message, honoring an optional channel override
// (falling back to the configured default channel). Backs builtins.gateway.post.
func (g *slackGateway) postText(ctx context.Context, channelOverride, text string) error {
	g.mu.Lock()
	client := g.client
	channel := g.channelID
	g.mu.Unlock()
	if client == nil {
		return fmt.Errorf("slack client not initialized")
	}
	if channelOverride != "" {
		channel = g.resolveNotifyChannel(ctx, client, channelOverride, channel)
	}
	if _, _, err := client.PostMessageContext(ctx, channel, slack.MsgOptionText(text, false)); err != nil {
		return fmt.Errorf("post message: %w", err)
	}
	return nil
}

// resolveNotifyChannel turns a per-mission channel override into a channel ID.
// A '#'-prefixed or name-shaped override is resolved by name; a Slack
// channel-ID-shaped override is used as-is. On failure it logs and falls back
// to def.
func (g *slackGateway) resolveNotifyChannel(ctx context.Context, client *slack.Client, override, def string) string {
	if !strings.HasPrefix(override, "#") && looksLikeSlackID(override) {
		return override
	}
	name := strings.TrimPrefix(override, "#")
	resolved, err := resolveChannelByName(ctx, client, name)
	if err != nil {
		log.Printf("notification channel override %q: %v — falling back to default", override, err)
		return def
	}
	return resolved
}

// looksLikeSlackID reports whether s has the shape of a Slack channel ID
// (C/G/D prefix followed by uppercase letters and digits).
func looksLikeSlackID(s string) bool {
	if len(s) < 2 {
		return false
	}
	switch s[0] {
	case 'C', 'G', 'D':
	default:
		return false
	}
	for _, r := range s[1:] {
		if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
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
