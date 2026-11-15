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
		VerifiedAt: "2026-11-11",
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
			"https://dev.twitch.tv/docs/api/reference/#get-users",
			"https://dev.twitch.tv/docs/api/reference/#get-channel-followers",
			"https://dev.twitch.tv/docs/api/reference/#get-broadcaster-subscriptions",
			"https://dev.twitch.tv/docs/api/reference/#get-vips",
			"https://dev.twitch.tv/docs/api/reference/#get-moderators",
			"https://dev.twitch.tv/docs/api/reference/#get-banned-users",
			"https://dev.twitch.tv/docs/api/reference/moderation",
			"https://dev.twitch.tv/docs/api/reference/chat",
			"https://dev.twitch.tv/docs/eventsub/reference/channel-moderation",
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
				Name: "chat.chatters", Support: Supported, Scopes: []string{"moderator:read:chatters"},
				Notes: "Get Chatters (GET /helix/chat/chatters, broadcaster_id and moderator_id both your account, pages of up " +
					"to 1000), read 2026-11-10. Twitch's reference: \"There is a delay between when users join and leave a chat " +
					"and when the list is updated\". It doesn't say whether viewers who aren't logged in are listed, and treats " +
					"none as known. Used for time in chat: quiet viewers on the list earn; the list is never used to guess " +
					"who watched while StreamGremlin was closed.",
			},
			{
				Name: "subscribers.list", Support: Supported, Scopes: []string{"channel:read:subscriptions"},
				Notes: "Get Broadcaster Subscriptions (GET /helix/subscriptions, broadcaster_id your account, pages of up to 100), " +
					"read 2026-11-10. \"The list is empty if the broadcaster has no subscribers.\" The reference doesn't say what " +
					"a channel that isn't an Affiliate or Partner gets, or whether the broadcaster is listed; StreamGremlin leaves " +
					"you and your bot out, and says Twitch listed no subscribers rather than that there are none. Used to give " +
					"points to subscribers.",
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
				Name: "events.hype_train", Support: PermissionRequired, Scopes: []string{"channel:read:hype_train"},
				Notes: "channel.hype_train.begin, progress and end, v2 (the only version the subscription types list), as " +
					"monetization.hype_train.started, updated and ended, with no actor. Progress comes once per contribution that " +
					"started the train and in no guaranteed order. Built against mocktwitch only: the user's channel isn't an Affiliate " +
					"(D-039). Not stated in the pages read: whether a non-Affiliate gets a 403, and v1's status.",
			},
			{
				Name: "polls_and_predictions", Support: PermissionRequired, Scopes: []string{PollScope, PredictionScope},
				Notes: "channel.poll.begin/progress/end and channel.prediction.begin/progress/lock/end v1, as chat.poll.* and " +
					"chat.prediction.*. Create/End Poll (title 60, 2-5 choices of 25, 15-1800 s, channel points per vote 1-1000000; " +
					"end TERMINATED or ARCHIVED) and Create/End Prediction (title 45, 2-10 outcomes of 25, 30-1800 s; LOCKED, RESOLVED " +
					"with a winner, CANCELED refunds; resolve within 24 h or Twitch cancels), from the API reference. One of each at a " +
					"time (400 when one is running). Affiliate or Partner only, per the guides; the reference lists no status code for " +
					"that, so a 403 or a message naming partner or affiliate is taken as it. Get Polls and Get Predictions with first=1 " +
					"are taken as the newest (the order isn't stated). Built against mocktwitch only (D-039).",
			},
			{
				Name: "ads", Support: PermissionRequired, Scopes: []string{AdsReadScope, CommercialScope, AdsManageScope},
				Notes: "Start Commercial (channel:edit:commercial; the broadcaster only, Partner or Affiliate, live; up to 180 s asked, " +
					"Twitch may serve shorter or longer; answers length and retry_after; 400 not live, 429 or 400 inside the cooldown). " +
					"Get Ad Schedule (channel:read:ads; snooze_count, snooze_refresh_at, next_ad_at, last_ad_at, duration, " +
					"preroll_free_time; a new ad can't run until 8 minutes after the last). Snooze Next Ad (channel:manage:ads; +5 " +
					"minutes when a snooze is left). The reference's examples send numbers as strings and times as RFC3339 while " +
					"fields are described as Unix times in places, so every documented shape is read. channel.ad_break.begin v1 " +
					"(channel:read:ads) as ads.break.started, the only sign an ad is running. Not offered by Twitch: an ad-ended " +
					"event, an eligibility check, cancelling a break, or which viewers saw an ad; the end is announced as an estimate " +
					"(start plus length). Requests are never retried; an unclear answer is checked against last_ad_at. Built against " +
					"mocktwitch only (D-039).",
			},
			{
				Name: "rewards.manage", Support: PermissionRequired, Scopes: []string{ManageScope},
				Notes: "Create, Get, Update and Delete Custom Reward; Get Custom Reward Redemption; Update Redemption Status. Affiliate or " +
					"Partner only (403 otherwise, shown as such). Only the Client ID that made a reward may change, delete or handle its " +
					"redemptions; rewards made on Twitch are listed as trigger only (only_manageable_rewards). Up to 50 rewards; titles 1-45 " +
					"characters and unique; prompts up to 200; cost at least 1; cooldown up to 604800 seconds (stated on Update only). A " +
					"redemption moves only from UNFULFILLED, to FULFILLED or CANCELED; CANCELED refunds the points. Not stated in the " +
					"reference: rate limits for these endpoints, a maximum cost or per-stream limit, and how several ids answer in one update.",
			},
			{
				Name: "viewers.card", Support: PermissionRequired,
				Scopes: []string{FollowersScope, SubscribersScope, ModerationReadScope, VIPsReadScope},
				Notes: "The Live screen's viewer card (D-078), read 2026-11-11, each asked fresh with one user_id, never cached by the " +
					"engine: Get Users (no scope; deleted or suspended accounts aren't listed), Get Channel Followers (moderator:read:followers; " +
					"empty means not following, as documented), Get Broadcaster Subscriptions (channel:read:subscriptions; tier, is_gift, " +
					"gifter, anonymous gifter id 274598607), Get VIPs (channel:read:vips; ids not on the list are ignored), Get Moderators " +
					"(moderation:read), Get Banned Users (moderation:read; expires_at empty for a ban, a time for a timeout). Not stated in " +
					"the reference: what Get Broadcaster Subscriptions, Get Moderators and Get Banned Users return for one user_id who isn't " +
					"listed; an empty list is taken as no. A timeout whose expires_at has passed is shown as not banned. A fact that fails " +
					"or is refused is listed as unknown with why, never as no. Check User Subscription isn't used: it needs the viewer's own token.",
			},
			{
				Name: "moderation.actions", Support: PermissionRequired,
				Scopes: []string{BansScope, ChatDeleteScope, WarningsScope, ShoutoutsScope, VIPsManageScope, ModsManageScope, SuspiciousManage},
				Notes: "D-079, ADR-0041, read 2026-11-11. As the streamer (moderator_id = broadcaster). Ban User (POST /moderation/bans, " +
					"data{user_id, duration 1-1209600 for a timeout, reason up to 500}; 400 already banned, may not be banned; a timeout on a " +
					"ban is refused; a ban on a timeout replaces it), Unban User (DELETE; 400 not banned), Delete Chat Messages (DELETE " +
					"/moderation/chat with message_id, never without: that clears chat; within 6 hours; not the broadcaster's or a mod's), " +
					"Warn Chat User (POST /moderation/warnings, data{user_id, reason}), Send a Shoutout (POST /chat/shoutouts; one per 2 " +
					"minutes, one per target per 60 minutes, 429), Add/Remove Channel VIP (422 a moderator or already a VIP; 409 no slots; " +
					"10 per 10 s), Add/Remove Channel Moderator (400 already; 422 a VIP; 10 per 10 s), Add/Remove Suspicious Status " +
					"(POST {user_id, status ACTIVE_MONITORING|RESTRICTED}, DELETE). None has an idempotency key: each is sent once; 4xx is " +
					"refused with Twitch's message, 5xx or no answer is unknown and never retried. Warn, Shoutout and the suspicious-user " +
					"endpoints were read from a summary of the reference, not verbatim: checked in the live check.",
			},
			{
				Name: "chat.settings", Support: PermissionRequired, Scopes: []string{ChatSettingsScope},
				Notes: "Get Chat Settings (no scope for these fields) and Update Chat Settings (PATCH, moderator:manage:chat_settings): " +
					"emote_mode, follower_mode with follower_mode_duration 0-129600 minutes, slow_mode with slow_mode_wait_time 3-120 " +
					"seconds, subscriber_mode, unique_chat_mode. non_moderator_chat_delay isn't offered. Read from a summary of the " +
					"reference: checked in the live check.",
			},
			{
				Name: "ban_lists", Support: PermissionRequired, Scopes: []string{BansScope, ModerationReadScope, VIPsReadScope},
				Notes: "M8 slice 2 (D-106, ADR-0049): Get Users (up to 100 logins and ids together; deleted and suspended accounts " +
					"aren't returned), Get Banned Users with user_id (up to 100; expires_at empty is a ban, not a timeout) and paged " +
					"(first=100, after), Get Moderators and Get VIPs with user_id (up to 100), Ban User and Unban User one account a " +
					"request. No bulk ban exists. Ratelimit-Limit, -Remaining and -Reset are read on each: ban lists wait at 200 points " +
					"left, and on 429 until the reset. 400 \"already banned\" and \"not banned\" are read from Twitch's message (not a " +
					"code: checked in the live check). Not documented: a cap on how many bans a channel holds. The raid guard (D-107) " +
					"asks Get Users for a new chatter's created_at once, to tell brand-new accounts.",
			},
			{
				Name: "shield_mode", Support: PermissionRequired, Scopes: []string{ShieldScope},
				Notes: "M8 slice 1 (D-105): Get Shield Mode Status (GET /moderation/shield_mode, broadcaster_id and moderator_id) and " +
					"Update Shield Mode Status (PUT, body is_active), moderator:manage:shield_mode (it covers reading too); " +
					"channel.shield_mode.begin and .end v1, condition broadcaster_user_id and moderator_user_id, either shield scope, " +
					"stored as moderation.shield_mode.changed (enabled, moderator). StreamGremlin only turns Shield Mode on and off: " +
					"what it does is the profile the streamer set up in Twitch's Mod View, which the API doesn't expose. A lock is " +
					"StreamGremlin's own: Shield Mode, then emote-only and followers-only (at least 10 minutes) through Update Chat " +
					"Settings, with what was there kept so an unlock puts back only what it changed. Each request is sent once; no " +
					"answer is unknown. Read from a summary of the reference: checked in the live check.",
			},
			{
				Name: "events.suspicious_users", Support: PermissionRequired, Scopes: []string{SuspiciousReadScope},
				Notes: "channel.suspicious_user.message and channel.suspicious_user.update v1, condition broadcaster_user_id and " +
					"moderator_user_id (both you). Stored as moderation.suspicious_user.message (keyed by the chat message id) and " +
					"moderation.suspicious_user.updated. Fields: low_trust_status none|active_monitoring|restricted, types " +
					"manually_added|ban_evader|banned_in_shared_channel, ban_evasion_evaluation unknown|possible|likely, " +
					"shared_ban_channel_ids. Not stated in the pages read: whether a restricted user's message also arrives as " +
					"channel.chat.message; the Live screen merges the two by message id in either order, and shows one alone. There's " +
					"no request to read a viewer's flags, so only flags heard while connected are known. Read from a summary of the " +
					"reference: checked in the live check.",
			},
			{
				Name: "channel.title_and_category", Support: PermissionRequired, Scopes: []string{BroadcastScope},
				Notes: "Modify Channel Information (PATCH /channels, channel:manage:broadcast; 204): title 1-140 characters, never empty; " +
					"game_id from Get Games (exact name, no scope) or else Search Categories' first answer (no scope; matches the start of " +
					"words, case-insensitive, close misspellings too). The step says which category it chose. Tags, content labels, " +
					"language and delay aren't offered (D-086). Read from the API reference 2026-11-13. Not stated: a general rate limit.",
			},
			{
				Name: "moderation.blocked_terms", Support: PermissionRequired, Scopes: []string{BlockedTermsScope},
				Notes: "D-088, read 2026-11-13. As the streamer (moderator_id = broadcaster), so private terms are listed too, unmarked. " +
					"Get Blocked Terms (GET /moderation/blocked_terms; first up to 100, newest first; a full page carries a cursor even " +
					"when the next page is empty; read up to 50 pages). Add Blocked Term (POST, body {text}; answers a term that's already " +
					"there with the existing one, so adding repeats safely). Remove Blocked Term (DELETE with id; 204, also for an id " +
					"Twitch doesn't have). expires_at is set only on terms AutoMod blocked for a while. 409, or 400 naming a conflict, " +
					"means another change is pending: tried again after 0.3 and 1 second. Add's length rule (2 to 500 characters; 3 " +
					"with a *, which goes at a word's start or end) and all of Remove were read from a summary of the reference, not " +
					"verbatim: StreamGremlin checks only 2-500 and shows Twitch's own refusal otherwise; checked in the live check. " +
					"Not stated: how Twitch matches a term (case, whole words), a limit on how many, a rate limit.",
			},
			{
				Name: "clips", Support: PermissionRequired, Scopes: []string{ClipsScope},
				Notes: "Create Clip (POST /clips, clips:edit; 202 with id and edit_url) with title and duration 5-60 seconds (default " +
					"30, of up to 90 seconds around the request). Made after answering: Get Clips by id is checked at 2, 5, 10 ... 60 " +
					"seconds, and a clip not shown by then is taken as failed, as the reference says. 404 not live; 403 clips restricted " +
					"or off. Never sent twice; no answer is unknown. StreamGremlin limits titles to 100 characters (Twitch states none). " +
					"Not stated: rate limits.",
			},
			{
				Name: "stream_markers", Support: PermissionRequired, Scopes: []string{BroadcastScope},
				Notes: "Create Stream Marker (POST /streams/markers, channel:manage:broadcast; 200 with position_seconds): description up to " +
					"140 characters; 404 when not live, past broadcasts are off, or in a stream's first seconds; 400 for reruns and " +
					"premieres. Never sent twice. Not stated: rate limits.",
			},
			{
				Name: "stream.read", Support: Supported,
				Notes: "Get Streams by user_id (no scope): type live, started_at, title, game_name, viewer_count; an empty list is " +
					"offline. Offline, the title and category come from Get Channel Information. Uptime is worked out from started_at.",
			},
			{
				Name: "follow_age", Support: PermissionRequired, Scopes: []string{FollowersScope},
				Notes: "Get Users by login (no scope; unknown names are left out), then Get Channel Followers with user_id " +
					"(moderator:read:followers): followed_at, or an empty list for not following.",
			},
			{
				Name: "users.profile_picture", Support: Supported,
				Notes: "Get Users by id (no scope) for profile_image_url, for alert overlays. Used only when it's https on " +
					"static-cdn.jtvnw.net. Kept 30 minutes per viewer. Anonymous gifts and cheers have no viewer, so no picture.",
			},
		},
	}
}
