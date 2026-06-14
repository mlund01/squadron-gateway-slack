package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
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

// slackPostDescription + slackPostSchema are advertised to squadron via
// MessageToolSpec so the LLM knows how to format a Slack post.
const slackPostDescription = "Post a message to the Slack channel. " +
	"`text` is the message body and supports Slack mrkdwn (*bold*, _italics_, `code`, > quotes, <url|label> links). " +
	"`channel` optionally overrides the destination — a channel name (with or without a leading #) or id. " +
	"`blocks` is an optional Slack Block Kit array (raw JSON) for rich layout. " +
	"`attachments` is an optional array of URLs to fetch and upload as files."

const slackPostSchema = `{
  "type": "object",
  "properties": {
    "text": {"type": "string", "description": "Message body (Slack mrkdwn supported)."},
    "channel": {"type": "string", "description": "Optional channel name or id override."},
    "blocks": {"type": "array", "description": "Optional Slack Block Kit blocks (raw JSON).", "items": {"type": "object"}},
    "attachments": {"type": "array", "items": {"type": "string"}, "description": "URLs to fetch and upload as files."}
  },
  "required": ["text"]
}`

type slackPostPayload struct {
	Text        string          `json:"text"`
	Channel     string          `json:"channel,omitempty"`
	Blocks      json.RawMessage `json:"blocks,omitempty"`
	Attachments []string        `json:"attachments,omitempty"`
}

// postMessage renders a builtins.gateway.post payload (text + mrkdwn, Block
// Kit blocks, fetched URL attachments) and posts it, honoring an optional
// channel override (falling back to the configured default channel).
func (g *slackGateway) postMessage(ctx context.Context, payload string) error {
	var p slackPostPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("parse message payload: %w", err)
	}
	g.mu.Lock()
	client := g.client
	channel := g.channelID
	g.mu.Unlock()
	if client == nil {
		return fmt.Errorf("slack client not initialized")
	}
	if p.Channel != "" {
		channel = g.resolveNotifyChannel(ctx, client, p.Channel, channel)
	}

	opts := []slack.MsgOption{slack.MsgOptionText(p.Text, false)}
	if len(p.Blocks) > 0 {
		var b slack.Blocks
		if err := json.Unmarshal([]byte(`{"blocks":`+string(p.Blocks)+`}`), &b); err == nil {
			opts = append(opts, slack.MsgOptionBlocks(b.BlockSet...))
		} else {
			log.Printf("slack blocks parse: %v", err)
		}
	}
	if _, _, err := client.PostMessageContext(ctx, channel, opts...); err != nil {
		return fmt.Errorf("post message: %w", err)
	}

	for _, url := range p.Attachments {
		g.uploadAttachment(ctx, client, channel, url)
	}
	return nil
}

// uploadAttachment downloads a URL (capped at 25 MB) and uploads it to the
// channel. Best-effort: a failed attachment is logged, not fatal.
func (g *slackGateway) uploadAttachment(ctx context.Context, client *slack.Client, channel, url string) {
	resp, err := http.Get(url)
	if err != nil {
		log.Printf("attachment %q: %v", url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("attachment %q: status %d", url, resp.StatusCode)
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 25<<20))
	if err != nil {
		log.Printf("attachment %q: %v", url, err)
		return
	}
	name := path.Base(resp.Request.URL.Path)
	if name == "" || name == "." || name == "/" {
		name = "attachment"
	}
	if _, err := client.UploadFileV2Context(ctx, slack.UploadFileV2Parameters{
		Reader:   bytes.NewReader(data),
		FileSize: len(data),
		Filename: name,
		Channel:  channel,
	}); err != nil {
		log.Printf("attachment %q upload: %v", url, err)
	}
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
