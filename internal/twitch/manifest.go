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
			"https://dev.twitch.tv/docs/eventsub/eventsub-subscription-types/",
			"https://dev.twitch.tv/docs/eventsub/eventsub-reference/",
			"https://dev.twitch.tv/docs/eventsub/manage-subscriptions/",
			"https://dev.twitch.tv/docs/api/reference/#get-channel-information",
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
			{
				Name: "events.follow", Support: Supported, Scopes: []string{"moderator:read:followers"},
				Notes: "channel.follow v2; condition broadcaster_user_id and moderator_user_id, both your account (the docs say " +
					"to name the broadcaster when the broadcaster authorized). Stored as audience.follow.created.",
			},
			{
				Name: "events.subscriptions", Support: Supported, Scopes: []string{"channel:read:subscriptions"},
				Notes: "channel.subscribe (new subs, gift recipients with is_gift; resubs aren't included), channel.subscription.message " +
					"(a resub shared in chat), channel.subscription.gift (anonymous gifts have null user fields). Tiers " +
					"1000/2000/3000 are stored as 1/2/3; Prime arrives as 1000. The message, gift and redemption payload tables " +
					"were read from a summary of the docs on 2026-11-08, not verbatim: checked against real events in the live check.",
			},
			{
				Name: "events.cheer", Support: Supported, Scopes: []string{"bits:read"},
				Notes: "channel.cheer; anonymous cheers have null user fields. The docs found no affiliate rule for subscribing; " +
					"a refusal is listed per event on Connections and chat goes on.",
			},
			{
				Name: "events.raid", Support: Supported,
				Notes: "channel.raid with to_broadcaster_user_id; no authorization. Costs 1 of the 10 cost points per WebSocket " +
					"(with stream.online, stream.offline and channel.update: 4 in all). The raider is the event's actor.",
			},
			{
				Name: "events.redemptions", Support: Supported, Scopes: []string{"channel:read:redemptions", "channel:manage:redemptions"},
				Notes: "channel.channel_points_custom_reward_redemption.add for every reward. The source event id is the " +
					"redemption's id, stable across redelivery. Managing rewards and fulfilling redemptions aren't built yet.",
			},
			{
				Name: "events.stream_and_channel", Support: Supported,
				Notes: "stream.online and stream.offline (broadcast.channel.online/offline), channel.update v2 " +
					"(broadcast.channel.updated). No authorization. The title and category are also read once per session " +
					"with Get Channel Information (no scope), so quotes can take the category.",
			},
			{
				Name: "events.limits", Support: Partial,
				Notes: "One WebSocket session per sign-in, with 11 subscriptions (300 allowed, total cost 4 of 10). The docs don't " +
					"give the status codes for a missing scope or a duplicate in the pages read; 403 is shown as refused, 409 " +
					"counts as already subscribed. A revoked event type stops only that type; chat goes on.",
			},
			{
				Name: "users.profile_picture", Support: Supported,
				Notes: "Get Users by id (no scope) for profile_image_url, for alert overlays. Used only when it's https on " +
					"static-cdn.jtvnw.net. Kept 30 minutes per viewer. Anonymous gifts and cheers have no viewer, so no picture.",
			},
		},
	}
}
