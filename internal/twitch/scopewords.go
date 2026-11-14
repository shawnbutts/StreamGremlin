package twitch

// scopeWords says what each permission StreamGremlin asks for lets it do,
// for Home's "Needs you" when a sign-in lacks some (D-087).
var scopeWords = map[string]string{
	"user:read:chat":               "read chat",
	"user:write:chat":              "send chat",
	FollowersScope:                 "see follows",
	SubscribersScope:               "see subs",
	"bits:read":                    "see bits",
	"channel:read:redemptions":     "see channel point redemptions",
	ManageScope:                    "manage channel point rewards",
	"channel:read:hype_train":      "see Hype Trains",
	PollScope:                      "run polls",
	PredictionScope:                "run predictions",
	AdsReadScope:                   "run and snooze ads",
	CommercialScope:                "run and snooze ads",
	AdsManageScope:                 "run and snooze ads",
	ChattersScope:                  "see who's in chat",
	ModerationReadScope:            "show moderators and bans on viewer cards",
	VIPsReadScope:                  "show VIPs on viewer cards",
	BansScope:                      "moderate chat",
	ChatDeleteScope:                "moderate chat",
	WarningsScope:                  "moderate chat",
	ShoutoutsScope:                 "moderate chat",
	VIPsManageScope:                "manage VIPs and moderators",
	ModsManageScope:                "manage VIPs and moderators",
	ChatSettingsScope:              "change chat modes",
	SuspiciousReadScope:            "handle suspicious users",
	SuspiciousManage:               "handle suspicious users",
	BroadcastScope:                 "change your title and category and add markers",
	ClipsScope:                     "make clips",
	BlockedTermsScope:              "manage Twitch's blocked terms",
	ShieldScope:                    "turn Shield Mode on and off",
	"user:read:moderated_channels": "check it's a moderator",
}

// ScopeWords returns what the scopes let StreamGremlin do, in words, each
// once, in the order given. A scope without words is named as it is.
func ScopeWords(scopes []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range scopes {
		w := scopeWords[s]
		if w == "" {
			w = s
		}
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}
