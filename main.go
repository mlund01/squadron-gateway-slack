// Squadron Slack gateway. Subscribes to ask_human events from a
// running squadron and bridges them to a Slack channel: new
// questions become messages with choice buttons; clicking a button
// (or replying in the message's thread) resolves the request.
//
// Required HCL settings:
//
//	gateway "slack" {
//	  source  = "github.com/mlund01/squadron-gateway-slack"
//	  version = "vX.Y.Z"
//	  settings = {
//	    bot_token  = vars.slack_bot_token   // xoxb-…
//	    app_token  = vars.slack_app_token   // xapp-… (Socket Mode)
//	    channel_id = "C0123456789"
//	  }
//	}
//
// Optional settings:
//
//	checkpoint_path = "/abs/path/to/checkpoint.json"   // default: ./.squadron-slack-gateway.json
//
// The checkpoint file holds the latest event timestamp the gateway has
// processed; on restart we ask squadron for everything since then so
// transient disconnects don't drop events.
package main

import "github.com/mlund01/squadron-gateway-sdk"

func main() {
	gateway.Serve(newSlackGateway())
}
