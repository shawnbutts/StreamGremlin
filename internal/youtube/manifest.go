package youtube

// Capability is one thing the YouTube adapter does, and what it relies on.
type Capability struct {
	Name    string `json:"name"`
	Support string `json:"support"` // supported, partial, unsupported or unknown
	Notes   string `json:"notes,omitempty"`
}

// Manifest records what the adapter relies on, checked against Google's
// documentation on VerifiedAt (AGENTS.md).
type Manifest struct {
	Provider     string       `json:"provider"`
	Protocol     string       `json:"protocol"`
	VerifiedAt   string       `json:"verified_at"`
	Docs         []string     `json:"docs"`
	Scopes       []string     `json:"scopes"`
	Capabilities []Capability `json:"capabilities"`
}

// YouTubeManifest is the adapter's manifest (D-095, D-096). Later M6
// slices add chat, events, moderating and the rest.
func YouTubeManifest() Manifest {
	return Manifest{
		Provider: "youtube", Protocol: "Google OAuth 2.0 for desktop apps; YouTube Data API v3; liveChatMessages.streamList over gRPC", VerifiedAt: "2026-11-14",
		Docs: []string{
			"https://developers.google.com/identity/protocols/oauth2/native-app",
			"https://developers.google.com/identity/protocols/oauth2/limited-input-device",
			"https://developers.google.com/youtube/v3/docs/channels/list",
			"https://developers.google.com/youtube/v3/determine_quota_cost",
			"https://developers.google.com/youtube/v3/live/docs/liveChatMessages/streamList",
			"https://developers.google.com/youtube/v3/live/streaming-live-chat",
			"https://developers.google.com/youtube/v3/live/docs/liveChatMessages",
			"https://developers.google.com/youtube/v3/live/docs/liveBroadcasts",
			"https://developers.google.com/youtube/v3/live/docs/liveBroadcasts/list",
			"https://developers.google.com/youtube/v3/live/docs/liveChatMessages/insert",
			"https://developers.google.com/youtube/v3/docs/subscriptions/list",
		},
		Scopes: []string{Scope},
		Capabilities: []Capability{
			{Name: "sign_in", Support: "supported", Notes: "Google's flow for desktop apps: the browser opens Google's page " +
				"(accounts.google.com/o/oauth2/v2/auth), which returns to a one-time loopback address (http://127.0.0.1:port/) " +
				"with a code. PKCE with S256 and a random state are always used; the client secret is optional for Desktop " +
				"app clients and sent when the streamer gave one. access_type=offline and prompt=select_account consent, so " +
				"each sign-in can pick an account or brand account and gets a refresh token. The device flow (Twitch's) was " +
				"not used: Google allows only the youtube and youtube.readonly scopes there."},
			{Name: "scopes", Support: "supported", Notes: "youtube.force-ssl for both accounts: liveChatMessages.insert, " +
				"liveChatBans.insert and the broadcast calls accept youtube or youtube.force-ssl; there's no narrower scope. " +
				"It's a sensitive scope: StreamGremlin's own Google project needs Google's verification before others can " +
				"use it, so each streamer uses their own project until then (D-095)."},
			{Name: "tokens", Support: "supported", Notes: "Access tokens last about an hour and are refreshed 10 minutes " +
				"early and every 50 minutes, which is also the check that the sign-in still works (no quota). invalid_grant " +
				"ends the sign-in: access removed, or 7 days passed while the project's publishing status is Testing " +
				"(Google's OAuth consent screen rules; unverified: the guide says to publish the project). Revoking ends " +
				"Google's whole grant for that account, so a refused second sign-in of the same channel isn't revoked."},
			{Name: "channel", Support: "supported", Notes: "channels.list?part=snippet&mine=true (1 unit) names the signed-in " +
				"channel; a Google account with no channel is refused. Two accounts can't be the same channel."},
			{Name: "quota", Support: "partial", Notes: "10,000 units a day per Google project, reset at midnight Pacific " +
				"time. Google doesn't tell an app what's used, so StreamGremlin counts its own calls (an estimate) and " +
				"stops calling after Google answers quotaExceeded, until the reset."},
			{Name: "broadcast", Support: "supported", Notes: "liveBroadcasts.list?part=snippet,status&broadcastStatus=active&" +
				"broadcastType=all (1 unit) finds your live broadcast and its snippet.liveChatId; lifeCycleStatus live or " +
				"liveStarting, never testing. Google allows only one of mine, id and broadcastStatus; it doesn't say in so " +
				"many words that broadcastStatus lists only the signed-in channel's broadcasts (checked on a real stream). " +
				"No push exists for going live: asked every 2 minutes while not live (at most 720 units a day), at once when " +
				"Twitch goes live or on Find my stream, and not at all while chat is being read."},
			{Name: "chat", Support: "partial", Notes: "liveChatMessages.streamList over gRPC (youtube.googleapis.com:443, " +
				"youtube.api.v3.V3DataLiveChatMessageService/StreamList, bearer token in metadata), 5 units a connection: " +
				"Google doesn't say whether a long connection costs more. Read with Google's stream_list.proto field numbers, " +
				"no generated code. A connection without a page token first gets recent history (marked backlog); a dropped " +
				"one resumes from nextPageToken, also after a restart (saved). FAILED_PRECONDITION means the chat ended or is " +
				"off (Google can't say which); chatEndedEvent and offlineAt end it too; RESOURCE_EXHAUSTED is too fast, unless " +
				"it says quota. Text messages are stored as chat.message.received (source youtube) and, for !commands said while " +
				"reading, chat.command.received; Live shows them with Twitch's (D-098). Super Chats and the other types in slice 4; " +
				"tombstones (deleted messages) as chat.message.deleted in slice 5."},
			{Name: "send", Support: "supported", Notes: "liveChatMessages.insert (POST /liveChat/messages?part=snippet, " +
				"textMessageEvent), 20 units a message, as the bot channel when signed in, otherwise yours, to the chat being " +
				"read. Google's reference states no length limit; YouTube's chat box takes 200 characters, so longer messages " +
				"(up to 500) are split at a space into up to 3 (unverified against the API's own limit). No threaded replies " +
				"exist: an answer to a message starts with @name. rateLimitExceeded, forbidden (banned or timed out) and " +
				"liveChatEnded are said in words; nothing is retried. A workflow's message as you is recognised by its id (or " +
				"text, before the answer) and stored as sent, so it starts nothing."},
			{Name: "events", Support: "supported", Notes: "From the chat stream (no extra quota), by snippet.type and its " +
				"details (stream_list.proto): superChatEvent (amount in micros, currency, amountDisplayString, tier, comment) and " +
				"superStickerEvent as their own events; giftEvent (Jewels) as monetization.jewels.gifted; newSponsorEvent and " +
				"giftMembershipReceivedEvent as monetization.subscription.started, memberMilestoneChatEvent as .renewed, " +
				"membershipGiftingEvent as .gifted, all from source youtube with the level's name and tier 0 (D-099); " +
				"sponsorOnlyModeStarted/Ended as chat.members_only.changed; userBannedEvent as moderation.user.banned or " +
				".timed_out (banType, banDurationSeconds), about the banned channel. Amounts aren't converted. Your broadcast " +
				"going live is recorded when its chat first answers, and offline when the chat ends (chatEndedEvent, offlineAt " +
				"or FAILED_PRECONDITION): a broadcast ending while StreamGremlin is closed has no offline event. A tombstone is " +
				"stored as chat.message.deleted (source youtube, deleted:<message id>), shown struck through on Live; Google's wording " +
				"leaves open whether a deletion sends one, so StreamGremlin's own deletes record it too. Polls in slice 7. Events " +
				"from before StreamGremlin was reading are marked backlog."},
			{Name: "moderate", Support: "partial", Notes: "M6 slice 5 (D-100), always as your channel, the chat's owner, to the " +
				"chat being read: liveChatMessages.delete (20 units); liveChatBans.insert, type permanent or temporary with " +
				"banDurationSeconds (200; Google states no longest timeout, so StreamGremlin keeps Twitch's 2 weeks); " +
				"liveChatBans.delete (200) by the ban's id, which only insert's answer gives: Google has no way to list bans, so " +
				"only a ban or timeout made from StreamGremlin can be undone, and the id is kept in the moderation record. Whether a " +
				"ban's id still works after its broadcast ends isn't documented. liveChatModerators.insert and .delete (200, owner " +
				"only); a moderator added elsewhere is found with liveChatModerators.list (50 a page, owner only, kept 10 minutes; " +
				"its scope isn't stated, youtube.force-ssl is assumed). No reason field, no warnings, no VIPs. Not in the API: " +
				"members-only mode and slow mode (YouTube Studio only; members-only changes are seen as events). Errors in words: " +
				"liveChatBanInsertionNotAllowed (the owner or a moderator), modificationNotAllowed, liveChatMessageNotFound, " +
				"liveChatBanNotFound, quotaExceeded. Nothing is retried: no answer is recorded as unknown. Chat filters delete on " +
				"YouTube unless a rule asks to time out too, and only delete with under 1,000 units left."},
			{Name: "roles", Support: "supported", Notes: "authorDetails: isChatOwner is the broadcaster, isChatModerator a " +
				"moderator, isChatSponsor a channel member, counted as a subscriber for roles and command permissions (D-098). " +
				"YouTube has no VIPs."},
			{Name: "subscribers", Support: "partial", Notes: "M6 slice 7: no push exists; subscriptions.list " +
				"myRecentSubscribers (1 unit), public subscriptions only, possibly limited."},
		},
	}
}
