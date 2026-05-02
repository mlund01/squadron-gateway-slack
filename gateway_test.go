package main

import (
	"context"
	"strings"
	"testing"
)

// Settings validation must fail before any network call so squadron
// surfaces a specific config error instead of an opaque dial failure.
func TestConfigureRejectsBadSettings(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]string
		wantErr  string
	}{
		{
			name:     "missing bot_token",
			settings: map[string]string{"app_token": "xapp-xyz", "channel_id": "C123"},
			wantErr:  "missing setting: bot_token",
		},
		{
			name:     "bot_token wrong prefix",
			settings: map[string]string{"bot_token": "xoxp-user", "app_token": "xapp-xyz", "channel_id": "C123"},
			wantErr:  "bot_token must start with xoxb-",
		},
		{
			name:     "missing app_token",
			settings: map[string]string{"bot_token": "xoxb-abc", "channel_id": "C123"},
			wantErr:  "missing setting: app_token",
		},
		{
			name:     "app_token wrong prefix",
			settings: map[string]string{"bot_token": "xoxb-abc", "app_token": "xoxb-also-bot", "channel_id": "C123"},
			wantErr:  "app_token must start with xapp-",
		},
		{
			name:     "missing both channel_id and channel_name",
			settings: map[string]string{"bot_token": "xoxb-abc", "app_token": "xapp-xyz"},
			wantErr:  "channel_id or channel_name",
		},
		{
			name: "channel_id and channel_name both set",
			settings: map[string]string{
				"bot_token":    "xoxb-abc",
				"app_token":    "xapp-xyz",
				"channel_id":   "C123",
				"channel_name": "general",
			},
			wantErr: "not both",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newSlackGateway()
			err := g.Configure(context.Background(), tc.settings, nil)
			if err == nil {
				t.Fatalf("expected error %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}
