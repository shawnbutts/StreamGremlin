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
		Provider: "youtube", Protocol: "Google OAuth 2.0 for desktop apps; YouTube Data API v3", VerifiedAt: "2026-11-13",
		Docs: []string{
			"https://developers.google.com/identity/protocols/oauth2/native-app",
			"https://developers.google.com/identity/protocols/oauth2/limited-input-device",
			"https://developers.google.com/youtube/v3/docs/channels/list",
			"https://developers.google.com/youtube/v3/determine_quota_cost",
			"https://developers.google.com/youtube/v3/live/docs/liveChatMessages/streamList",
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
			{Name: "chat", Support: "unsupported", Notes: "M6 slice 2: liveChatMessages.streamList (gRPC, 5 units a " +
				"connection)."},
			{Name: "subscribers", Support: "partial", Notes: "M6 slice 7: no push exists; subscriptions.list " +
				"myRecentSubscribers (1 unit), public subscriptions only, possibly limited."},
		},
	}
}
