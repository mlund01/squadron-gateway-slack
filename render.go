package main

import (
	"encoding/json"
	"strings"

	gatewaysdk "github.com/mlund01/squadron-gateway-sdk"
	"github.com/slack-go/slack"
)

// buildFallbackText is the plain-text fallback shown by Slack clients
// that can't render Block Kit (notifications, screen readers).
func buildFallbackText(rec gatewaysdk.HumanInputRecord) string {
	if rec.ShortSummary != "" {
		return rec.ShortSummary
	}
	return truncate(rec.Question, 200)
}

// buildBlocks renders the message body + interactive picker as Block
// Kit blocks. Returned slice is ready to pass to MsgOptionBlocks.
func buildBlocks(rec gatewaysdk.HumanInputRecord) []slack.Block {
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, buildMessageBody(rec), false, false),
			nil, nil,
		),
	}
	if actions := buildActionsBlock(rec); actions != nil {
		blocks = append(blocks, actions)
	}
	return blocks
}

// buildMessageBody renders the question + context as Slack mrkdwn.
// Slack's mrkdwn dialect:
//   - bold:          *text*    (single asterisks, NOT **text**)
//   - italics:       _text_
//   - inline code:   `text`
//   - strikethrough: ~text~    (single tildes)
func buildMessageBody(rec gatewaysdk.HumanInputRecord) string {
	var b strings.Builder
	if rec.ShortSummary != "" {
		b.WriteString("*")
		b.WriteString(rec.ShortSummary)
		b.WriteString("*\n")
	}
	b.WriteString(rec.Question)
	if rec.MultiSelect && len(rec.Choices) > 0 {
		b.WriteString("\n_Pick one or more from the dropdown below._")
	} else if len(rec.Choices) == 0 {
		b.WriteString("\n_Reply in the thread to answer._")
	}
	if rec.AdditionalContext != "" {
		b.WriteString("\n\n_Context_\n")
		b.WriteString(rec.AdditionalContext)
	}
	if rec.MissionName != "" {
		b.WriteString("\n\n`")
		b.WriteString(rec.MissionName)
		if rec.TaskName != "" {
			b.WriteString(" › ")
			b.WriteString(rec.TaskName)
		}
		b.WriteString("`")
	}
	return b.String()
}

func buildResolvedBody(rec gatewaysdk.HumanInputRecord) string {
	var b strings.Builder
	if rec.ShortSummary != "" {
		b.WriteString("~*")
		b.WriteString(rec.ShortSummary)
		b.WriteString("*~\n")
	} else {
		b.WriteString("~")
		b.WriteString(truncate(rec.Question, 200))
		b.WriteString("~\n")
	}
	b.WriteString("✅ ")
	b.WriteString(formatResolvedResponse(rec))
	if rec.ResponderUserID != "" {
		b.WriteString(" — _")
		b.WriteString(displayResponder(rec.ResponderUserID))
		b.WriteString("_")
	}
	return b.String()
}

// formatResolvedResponse expands a multi-select JSON array into a
// comma list. Falls back to the raw response on parse failure so the
// audit trail keeps whatever squadron stored.
func formatResolvedResponse(rec gatewaysdk.HumanInputRecord) string {
	if !rec.MultiSelect {
		return rec.Response
	}
	var picks []string
	if err := json.Unmarshal([]byte(rec.Response), &picks); err != nil || len(picks) == 0 {
		return rec.Response
	}
	return strings.Join(picks, ", ")
}

// buildActionsBlock picks the picker shape:
//   - no choices → no actions block (free-text via thread reply)
//   - multi_select → ActionsBlock containing one MultiStaticSelect
//   - else → ActionsBlock of buttons
func buildActionsBlock(rec gatewaysdk.HumanInputRecord) slack.Block {
	if len(rec.Choices) == 0 {
		return nil
	}
	if rec.MultiSelect {
		return buildSelectMenuBlock(rec)
	}
	return buildButtonsBlock(rec)
}

// Slack Block Kit limits.
const (
	maxActionElements   = 25 // elements per ActionsBlock
	maxButtonLabelBytes = 75
	maxSelectOptionText = 75
)

func buildButtonsBlock(rec gatewaysdk.HumanInputRecord) slack.Block {
	elements := make([]slack.BlockElement, 0, len(rec.Choices))
	for i, choice := range rec.Choices {
		if i >= maxActionElements {
			break
		}
		label := truncate(choice, maxButtonLabelBytes)
		btn := slack.NewButtonBlockElement(
			encodeCustomID(rec.ToolCallID, choice),
			choice,
			slack.NewTextBlockObject(slack.PlainTextType, label, false, false),
		)
		elements = append(elements, btn)
	}
	return slack.NewActionBlock("sq-actions-"+rec.ToolCallID, elements...)
}

func buildSelectMenuBlock(rec gatewaysdk.HumanInputRecord) slack.Block {
	options := make([]*slack.OptionBlockObject, 0, len(rec.Choices))
	for i, choice := range rec.Choices {
		if i >= maxActionElements {
			break
		}
		label := truncate(choice, maxSelectOptionText)
		options = append(options, slack.NewOptionBlockObject(
			choice,
			slack.NewTextBlockObject(slack.PlainTextType, label, false, false),
			nil,
		))
	}
	menu := slack.NewOptionsMultiSelectBlockElement(
		slack.MultiOptTypeStatic,
		slack.NewTextBlockObject(slack.PlainTextType, "Pick one or more…", false, false),
		encodeSelectMenuCustomID(rec.ToolCallID),
		options...,
	)
	return slack.NewActionBlock("sq-actions-"+rec.ToolCallID, menu)
}

// displayResponder substitutes a generic label when no responder id
// was recorded — happens when commander resolves with auth disabled.
func displayResponder(s string) string {
	if s == "" {
		return "another operator"
	}
	return s
}

// truncate caps a string at `max` BYTES (not runes), reserving space
// for the 3-byte UTF-8 ellipsis. Slack enforces text-object limits on
// bytes-on-the-wire.
func truncate(s string, max int) string {
	const ellipsis = "…"
	if len(s) <= max {
		return s
	}
	if max <= len(ellipsis) {
		return s[:max]
	}
	return s[:max-len(ellipsis)] + ellipsis
}
