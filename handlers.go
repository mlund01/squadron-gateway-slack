package main

import (
	"context"
	"encoding/json"
	"log"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// runSocketEvents pumps the socket-mode Events channel until the run
// context is canceled. Each event is acked immediately (Slack expects
// an ack within ~3s) and routed to a typed handler.
func (g *slackGateway) runSocketEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-g.socket.Events:
			if !ok {
				return
			}
			switch evt.Type {
			case socketmode.EventTypeInteractive:
				cb, ok := evt.Data.(slack.InteractionCallback)
				if !ok {
					continue
				}
				if evt.Request != nil {
					g.socket.Ack(*evt.Request)
				}
				g.onInteraction(ctx, cb)
			case socketmode.EventTypeEventsAPI:
				eapi, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					log.Printf("events-api evt arrived but Data was %T", evt.Data)
					continue
				}
				if evt.Request != nil {
					g.socket.Ack(*evt.Request)
				}
				g.onEventsAPI(ctx, eapi)
			case socketmode.EventTypeConnecting, socketmode.EventTypeConnected, socketmode.EventTypeHello:
				// status events — nothing to do
			case socketmode.EventTypeErrorBadMessage, socketmode.EventTypeErrorWriteFailed,
				socketmode.EventTypeIncomingError, socketmode.EventTypeDisconnect:
				log.Printf("socket-mode event: %s", evt.Type)
			}
		}
	}
}

// onInteraction routes a Block Kit action (button click or
// multi-select submit) into ResolveHumanInput.
func (g *slackGateway) onInteraction(ctx context.Context, cb slack.InteractionCallback) {
	if cb.Type != slack.InteractionTypeBlockActions {
		return
	}
	if len(cb.ActionCallback.BlockActions) == 0 {
		return
	}
	action := cb.ActionCallback.BlockActions[0]

	toolCallID, response, ok := decodeInteractionResponse(action)
	if !ok {
		return
	}

	res, err := g.api.ResolveHumanInput(ctx, toolCallID, response, slackResponderID(cb))
	if err != nil {
		log.Printf("resolve from interaction: %v", err)
		return
	}

	// On NotFound surface a transient ephemeral note so the operator
	// knows their click didn't land. Other outcomes are confirmed by
	// the message edit flowing through OnHumanInputResolved.
	if res.NotFound {
		_, err := g.client.PostEphemeralContext(ctx, cb.Channel.ID, cb.User.ID,
			slack.MsgOptionText("That request is no longer in squadron's store — the row may have been trimmed.", false))
		if err != nil {
			log.Printf("ephemeral not-found notice: %v", err)
		}
	}
}

// decodeInteractionResponse maps a Block Kit BlockAction into the
// (toolCallID, response) pair we send to squadron. Returns ok=false
// for any action_id that didn't originate here. Multi-select values
// are JSON-encoded so the agent gets a canonical array string instead
// of a Go-printed slice.
func decodeInteractionResponse(action *slack.BlockAction) (toolCallID, response string, ok bool) {
	if action == nil {
		return "", "", false
	}
	if tc, ok := decodeSelectMenuCustomID(action.ActionID); ok {
		picks := make([]string, 0, len(action.SelectedOptions))
		for _, opt := range action.SelectedOptions {
			picks = append(picks, opt.Value)
		}
		encoded, err := json.Marshal(picks)
		if err != nil {
			log.Printf("encode select-menu values: %v", err)
			return "", "", false
		}
		return tc, string(encoded), true
	}
	if tc, choice, ok := decodeCustomID(action.ActionID); ok {
		return tc, choice, true
	}
	return "", "", false
}

// onEventsAPI handles the message-event subset we care about: a thread
// reply to one of our posted question messages.
func (g *slackGateway) onEventsAPI(ctx context.Context, evt slackevents.EventsAPIEvent) {
	if evt.Type != slackevents.CallbackEvent {
		return
	}
	msg, ok := evt.InnerEvent.Data.(*slackevents.MessageEvent)
	if !ok {
		return
	}
	g.handleMessage(ctx, msg)
}

func (g *slackGateway) handleMessage(ctx context.Context, msg *slackevents.MessageEvent) {
	if msg == nil {
		return
	}
	// Drop bot messages, edits, and channel mismatches silently — these
	// fire on every channel the bot is a member of and would flood the
	// log. Only thread replies in our channel that don't match a
	// tracked question get a diagnostic line, since that's the rare
	// case where an operator's reply silently does nothing and they'll
	// want to know why.
	if msg.SubType != "" {
		return
	}
	if msg.BotID != "" || msg.User == "" || msg.User == g.botUserID {
		return
	}
	if msg.Channel != g.channelID {
		return
	}
	if msg.ThreadTimeStamp == "" {
		return
	}

	g.mu.Lock()
	matched := lookupToolCallByMessageTS(g.messages, msg.ThreadTimeStamp)
	known := len(g.messages)
	g.mu.Unlock()
	if matched == "" {
		log.Printf("thread reply on ts=%s did not match any tracked question (%d open) — likely a reply on a question posted before this gateway started", msg.ThreadTimeStamp, known)
		return
	}

	if _, err := g.api.ResolveHumanInput(ctx, matched, msg.Text, "slack:"+msg.User); err != nil {
		log.Printf("resolve from thread reply: %v", err)
	}
}

// lookupToolCallByMessageTS — caller holds g.mu.
func lookupToolCallByMessageTS(messages map[string]string, ts string) string {
	for toolCallID, msgTS := range messages {
		if msgTS == ts {
			return toolCallID
		}
	}
	return ""
}

// slackResponderID prefers the user's display name when present;
// falls back to the user id (UXXXXXXXX) so the audit trail always has
// something stable.
func slackResponderID(cb slack.InteractionCallback) string {
	if cb.User.Name != "" {
		return "slack:" + cb.User.Name
	}
	if cb.User.ID != "" {
		return "slack:" + cb.User.ID
	}
	return "slack:unknown"
}
