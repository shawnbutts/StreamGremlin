package twitch

// Support says how far Twitch's public API allows something.
type Support string

const (
	Supported          Support = "supported"
	Partial            Support = "partial"
	Unsupported        Support = "unsupported"
	PermissionRequired Support = "permission_required"
	Unknown            Support = "unknown"
)

// Capability is one thing the adapter does, and what Twitch requires for it.
type Capability struct {
	Name    string   `json:"name"`
	Support Support  `json:"support"`
	Scopes  []string `json:"scopes,omitempty"`
	Notes   string   `json:"notes,omitempty"`
}

// Manifest records what the Twitch adapter relies on, checked against
// Twitch's documentation on VerifiedAt (AGENTS.md).
type Manifest struct {
	Provider     string       `json:"provider"`
	VerifiedAt   string       `json:"verified_at"`
	Docs         []string     `json:"docs"`
	Capabilities []Capability `json:"capabilities"`
}

// TwitchManifest is the adapter's manifest. Slices add capabilities as they
// build them.
func TwitchManifest() Manifest {
	return Manifest{
		Provider:   "twitch",
		VerifiedAt: "2026-11-08",
		Docs: []string{
			"https://dev.twitch.tv/docs/authentication/getting-tokens-oauth/",
			"https://dev.twitch.tv/docs/authentication/refresh-tokens/",
			"https://dev.twitch.tv/docs/authentication/validate-tokens/",
			"https://dev.twitch.tv/docs/authentication/revoke-tokens/",
			"https://dev.twitch.tv/docs/authentication/register-app/",
			"https://dev.twitch.tv/docs/eventsub/handling-websocket-events/",
			"https://dev.twitch.tv/docs/eventsub/eventsub-subscription-types/#channelchatmessage",
			"https://dev.twitch.tv/docs/api/reference/#create-eventsub-subscription",
			"https://dev.twitch.tv/docs/api/reference/#send-chat-message",
			"https://dev.twitch.tv/docs/api/reference/#get-moderated-channels",
			"https://dev.twitch.tv/docs/chat/",
		},
		Capabilities: []Capability{
			{
				Name: "sign_in.device_code", Support: Supported,
				Notes: "Public client, no secret. POST /oauth2/device then poll /oauth2/token. Pending answers 400 " +
					"authorization_pending. Errors for an expired or denied code and slow_down aren't documented: " +
					"slow_down adds 5 seconds, anything else ends the attempt with Twitch's message.",
			},
			{
				Name: "sign_in.refresh", Support: Partial,
				Notes: "Public clients refresh without a secret; refresh tokens are single use and the new pair is " +
					"saved before use. The docs say a public client's refresh token expires 30 days after it's " +
					"generated in one place and after 30 days unused in another, so the app plans for signing in " +
					"again within 30 days. An invalid refresh token is documented as both 400 and 401; both end the sign-in.",
			},
			{
				Name: "sign_in.validate", Support: Supported,
				Notes: "GET /oauth2/validate at start and hourly, as Twitch requires. A 401 gets one refresh; " +
					"if that fails every session using the token ends.",
			},
			{Name: "sign_in.revoke", Support: Supported, Notes: "On sign-out and when replacing a sign-in. Best effort."},
			{
				Name: "chat.read", Support: PermissionRequired, Scopes: []string{"user:read:chat"},
				Notes: "EventSub WebSocket (wss://eventsub.wss.twitch.tv/ws, 30-second keepalive), channel.chat.message v1 " +
					"with broadcaster_user_id = user_id = you. Subscribed within 10 seconds of the welcome. A missed keepalive " +
					"or a close reconnects with backoff and subscribes again; session_reconnect hands over without " +
					"resubscribing. Twitch doesn't replay messages missed while disconnected, and doesn't say whether a " +
					"message can arrive twice, so the inbox drops repeats by message_id. Limits: 3 WebSocket connections per " +
					"user token; StreamGremlin uses one.",
			},
			{
				Name: "chat.send", Support: PermissionRequired, Scopes: []string{"user:write:chat"},
				Notes: "POST /helix/chat/messages as you or the bot, 500 characters. Never retried (no idempotency key). " +
					"is_sent false comes back as not sent, with Twitch's drop reason. for_source_only isn't sent: its " +
					"default changed in 2025 and it needs an app token. Twitch's limits: 20 per 30 s and 1 per second " +
					"for an ordinary account, 100 per 30 s for the broadcaster, a moderator or a VIP; past them Twitch " +
					"ignores the account for an hour. StreamGremlin stops at 18 and 90.",
			},
			{
				Name: "chat.bot_moderator", Support: PermissionRequired, Scopes: []string{"user:read:moderated_channels"},
				Notes: "GET /helix/moderation/channels with the bot's token, when chat connects, when the bot signs in and " +
					"hourly; also from the badges on the bot's own messages. VIP status is only learnt from badges.",
			},
			{
				Name: "chat.bot_badge", Support: Unsupported,
				Notes: "The Chat Bot badge needs an app access token, which needs a client secret. A public app can't have one.",
			},
		},
	}
}
