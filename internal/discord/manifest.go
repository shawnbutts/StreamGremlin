package discord

// Capability is one thing the Discord adapter does, and what it relies on.
type Capability struct {
	Name    string `json:"name"`
	Support string `json:"support"` // supported, partial, unsupported or unknown
	Notes   string `json:"notes,omitempty"`
}

// Manifest records what the adapter relies on, checked against Discord's
// developer documentation on VerifiedAt (AGENTS.md).
type Manifest struct {
	Provider     string       `json:"provider"`
	Protocol     string       `json:"protocol"`
	VerifiedAt   string       `json:"verified_at"`
	Docs         []string     `json:"docs"`
	Capabilities []Capability `json:"capabilities"`
}

// DiscordManifest is the adapter's manifest (D-089).
func DiscordManifest() Manifest {
	return Manifest{
		Provider: "discord", Protocol: "Discord HTTP API v10, webhooks only (no bot account, no gateway)", VerifiedAt: "2026-11-13",
		Docs: []string{"https://docs.discord.com/developers/resources/webhook", "https://docs.discord.com/developers/resources/message",
			"https://docs.discord.com/developers/topics/rate-limits"},
		Capabilities: []Capability{
			{Name: "webhooks.add", Support: "supported", Notes: "Get Webhook with Token (GET /webhooks/{id}/{token}, no auth) checks a pasted link " +
				"and gives the webhook's name and channel id. Links on discord.com, discordapp.com, canary.discord.com and ptb.discord.com " +
				"are accepted: Discord's pages don't list the hosts, these are the ones its app copies. The link (its token) is kept in " +
				"the secret store only."},
			{Name: "webhooks.post", Support: "supported", Notes: "Execute Webhook with wait=true: content up to 2000 characters, one embed (title " +
				"256, description 4096, footer 2048, 6000 in all; colour as an integer; image by http(s) link), username. allowed_mentions " +
				"is always sent: parse [] unless @everyone or @here is chosen, then parse [everyone]; Discord's default for webhooks would " +
				"notify mentioned users. Text is cut to fit, never refused. There's no idempotency key: each post is sent once; no answer " +
				"or a 5xx is unknown and never retried."},
			{Name: "rate_limits", Support: "supported", Notes: "429 carries retry_after in seconds (or Retry-After): waited for and tried again " +
				"twice, up to 10 seconds a wait, since a 429 means nothing was posted. 401, 403 and 429 count toward Discord's 10,000 " +
				"invalid requests per 10 minutes; a webhook that answers 404 (10015) or 401 (50027) is reported as gone and not tried again " +
				"by StreamGremlin until the streamer acts, as Discord asks."},
			{Name: "webhooks.edit", Support: "unsupported", Notes: "Edit Webhook Message exists; editing the live announcement when the stream " +
				"ends is Tier 2 (D-089)."},
			{Name: "bot", Support: "unsupported", Notes: "No bot account, slash commands, roles or Discord events: Tier 2, not decided (D-089)."},
		},
	}
}
