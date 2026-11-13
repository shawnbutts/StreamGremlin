package pretzel

// Capability is one thing the Pretzel adapter does, and what it relies on.
type Capability struct {
	Name    string `json:"name"`
	Support string `json:"support"` // supported, partial, unsupported or unknown
	Notes   string `json:"notes,omitempty"`
}

// Manifest records what the adapter relies on, checked against Pretzel's
// help pages on VerifiedAt (AGENTS.md).
type Manifest struct {
	Provider     string       `json:"provider"`
	Protocol     string       `json:"protocol"`
	VerifiedAt   string       `json:"verified_at"`
	Docs         []string     `json:"docs"`
	Capabilities []Capability `json:"capabilities"`
}

// PretzelManifest is the adapter's manifest (D-090).
func PretzelManifest() Manifest {
	return Manifest{
		Provider: "pretzel", Protocol: "HTTPS GET, plain text, no sign-in; read only", VerifiedAt: "2026-11-13",
		Docs: []string{"https://help.pretzel.rocks/en-us/articles/15281318-how-do-i-create-a-song-command-for-my-bot",
			"https://help.pretzel.rocks/en-us/articles/15281314-why-isn-t-my-chat-attribution-working",
			"https://help.pretzel.rocks/en-us/articles/15281316-how-do-i-show-what-s-playing-on-my-streaming-overlay-write-to-file"},
		Capabilities: []Capability{
			{Name: "now_playing", Support: "supported", Notes: "GET https://api.pretzel.rocks/playing/twitch/{numeric Twitch user id}, " +
				"documented for chat bots' !song commands. Its format isn't documented; as seen on 2026-11-13 it answers 200 text/plain " +
				"\"Now Playing: Title by Artist -> https://prtzl.io/…\" (the artist may be empty), and 400 \"ID not found\" for an " +
				"account Pretzel doesn't know. Anything else is kept as said and never shown on an overlay. No rate limit is published: " +
				"StreamGremlin asks every 15 seconds only while a Now playing overlay is open, and backs off on 429 or 503 (Retry-After)."},
			{Name: "album_art", Support: "unsupported", Notes: "The API gives no picture. Pretzel's desktop app can write the cover to a JPG " +
				"(Write to File); OBS can show that file itself with an Image source."},
			{Name: "playback_state", Support: "unknown", Notes: "Whether paused or stopped is said, and how, isn't documented."},
			{Name: "playback_control", Support: "unsupported", Notes: "Pretzel documents no way for other apps to play, pause, skip or choose " +
				"music. StreamGremlin doesn't press its hotkeys or use private endpoints."},
			{Name: "attribution", Support: "supported", Notes: "Pretzel requires its own chat attribution unless the streamer has Premium or " +
				"Pro, and says !song commands don't replace it. StreamGremlin never hides or replaces it."},
		},
	}
}
