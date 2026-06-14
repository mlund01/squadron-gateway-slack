# squadron-gateway-slack

A Squadron gateway that surfaces `builtins.human.ask` questions in a
Slack channel. When an agent asks a question, the gateway posts a
message with quick-reply buttons (or a multi-select dropdown); the
human's answer flows back to the agent via a button click or a thread
reply.

## Setup walkthrough

You'll create a Slack app, give it two tokens (one bot, one app-level
for Socket Mode), invite it to a channel, then point your squadron
config at it. About 5 minutes end-to-end.

### 1. Create the Slack app

1. Go to <https://api.slack.com/apps> → **Create New App** → **From
   scratch**.
2. Name it `squadron` (or whatever you like) and pick your workspace.

### 2. Enable Socket Mode and grab the app-level token

Socket Mode is how the gateway receives events without exposing a
public URL.

1. In the app's left sidebar: **Socket Mode** → toggle **Enable Socket
   Mode**.
2. Slack will prompt for an app-level token. Name it `squadron-socket`
   and add the scope `connections:write`. Click **Generate**.
3. Copy the token (starts with `xapp-`) and stash it in squadron:

   ```bash
   squadron vars set slack_app_token <xapp-…>
   ```

### 3. Configure bot scopes and install

1. Sidebar: **OAuth & Permissions** → scroll to **Scopes** → **Bot
   Token Scopes** → **Add an OAuth Scope** for each of:

   - `chat:write` — post the question messages
   - `channels:history` — read public-channel thread replies (free-text answers)
   - `groups:history` — read private-channel thread replies (skip if you only post to public channels)
   - `channels:read` — only needed if you'll use `channel_name` instead of `channel_id`
   - `groups:read` — same, for private channels
   - `files:write` — **only needed if agents will attach files** via the `builtins.gateway.post` tool's `attachments` (the gateway fetches each URL and uploads it). Skip it if you don't use attachments.

2. Scroll up → **Install to Workspace** → **Allow**.
3. After install, the page shows a **Bot User OAuth Token** (starts
   with `xoxb-`). Copy it and stash:

   ```bash
   squadron vars set slack_bot_token <xoxb-…>
   ```

### 4. Subscribe to message events

So the gateway can hear thread replies (free-text answers):

1. Sidebar: **Event Subscriptions** → toggle **Enable Events** on.
2. Under **Subscribe to bot events** → **Add Bot User Event** for:

   - `message.channels`
   - `message.groups` (skip if no private channels)

3. Click **Save Changes**. If Slack asks you to reinstall the app
   (because scopes changed), do it.

### 5. Invite the bot to your channel

In Slack, in the channel where questions should land:

```
/invite @squadron
```

Then grab the channel ID: click the channel name → **About** → bottom
of the panel shows `Channel ID: C0123456789`. Stash it:

```bash
squadron vars set slack_channel_id C0123456789
```

### 6. Wire it into your squadron config

```hcl
variable "slack_bot_token" {
  secret = true
}

variable "slack_app_token" {
  secret = true
}

variable "slack_channel_id" {}

gateway "slack" {
  source  = "github.com/mlund01/squadron-gateway-slack"
  version = "v0.0.1"

  settings = {
    bot_token       = vars.slack_bot_token
    app_token       = vars.slack_app_token
    channel_id      = vars.slack_channel_id
    checkpoint_path = "${path.cwd}/.squadron/slack-gateway.json"
  }
}
```

Restart squadron. The log should show `gateway "slack" started` and
something like `slack gateway ready (channel=C0123…, bot=U0123…)`.
The next `builtins.human.ask` call lands in your channel.

## Settings reference

| Setting           | Required | Notes                                                                 |
| ----------------- | -------- | --------------------------------------------------------------------- |
| `bot_token`       | yes      | Bot User OAuth Token (`xoxb-…`). Use a `secret` variable.             |
| `app_token`       | yes      | App-Level Token (`xapp-…`) with `connections:write`. Use a `secret` variable. |
| `channel_id`      | one of   | Slack channel ID (e.g. `C0123456789`).                                |
| `channel_name`    | one of   | Channel name (e.g. `general` or `#general`). Resolved at startup; needs `channels:read` / `groups:read`. |
| `checkpoint_path` | optional | Where the gateway persists the last event timestamp it processed. Defaults to `./.squadron-slack-gateway.json`. |

Set exactly one of `channel_id` or `channel_name`. `channel_id` is the
most stable handle — renaming a channel won't break the gateway.
`channel_name` is friendlier but pays a startup paginated REST round-
trip and breaks if the channel is renamed.

## What the operator sees

For a question with choices:

```
*Should I proceed with prod or staging?*
The schemas are identical except for the past 24 hours of writes.

[ prod ] [ staging ] [ both ]

`databricks_explore › discover`
```

Click a button to answer. For multi-select questions (`multi_select:
true` on the agent's tool call) the channel shows a multi-select
dropdown picker instead of buttons. For free-text questions, **reply
in the message's thread** and the body of your reply becomes the
answer.

When a question is resolved (here, in Slack, in Command Center, or
anywhere else), the message is edited to strike through the question
and append the answer.

## Local development

If you want to hack on the gateway itself, point your squadron config
at a locally-built binary:

```hcl
gateway "slack" {
  version = "local"   # skips the GitHub download
  # source omitted intentionally
  settings = { ... }
}
```

Then build and install:

```bash
go build -o gateway .
mkdir -p ~/.squadron/gateways/darwin-arm64/slack/local
mv gateway ~/.squadron/gateways/darwin-arm64/slack/local/gateway
```

Adjust the platform path to match `runtime.GOOS-runtime.GOARCH`.

## See also

- [`squadron-gateway-discord`](https://github.com/mlund01/squadron-gateway-discord)
  — same protocol, different surface; useful as a reference.
- [`squadron-gateway-sdk`](https://github.com/mlund01/squadron-gateway-sdk)
  — the Go SDK if you want to build a gateway for some other system.
