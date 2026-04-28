package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// resolveChannelByName looks up a channel by name across the
// workspaces the bot is in. Slack channel names are workspace-unique,
// so an exact match is unambiguous — but we still surface "no match"
// vs "API error" cleanly so the operator knows whether to fix the
// name or fix the bot's permissions.
func resolveChannelByName(ctx context.Context, client *slack.Client, channelName string) (string, error) {
	cursor := ""
	for {
		params := &slack.GetConversationsParameters{
			Cursor:          cursor,
			Limit:           200,
			ExcludeArchived: true,
			Types:           []string{"public_channel", "private_channel"},
		}
		channels, next, err := client.GetConversationsContext(ctx, params)
		if err != nil {
			return "", fmt.Errorf("list channels: %w", err)
		}
		for _, c := range channels {
			if strings.EqualFold(c.Name, channelName) {
				return c.ID, nil
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return "", fmt.Errorf("no channel %q visible to the bot — invite it with `/invite @<bot>` or pass channel_id directly", channelName)
}
