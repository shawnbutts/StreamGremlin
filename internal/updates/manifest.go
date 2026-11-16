package updates

// Capability is one thing the update check does, and what it relies on.
type Capability struct {
	Name    string `json:"name"`
	Support string `json:"support"` // supported, partial, unsupported or unknown
	Notes   string `json:"notes,omitempty"`
}

// Manifest records what the update check relies on from GitHub, checked
// against GitHub's REST documentation on VerifiedAt (AGENTS.md).
type Manifest struct {
	Provider     string       `json:"provider"`
	Protocol     string       `json:"protocol"`
	VerifiedAt   string       `json:"verified_at"`
	Docs         []string     `json:"docs"`
	Capabilities []Capability `json:"capabilities"`
}

// GitHubManifest is the update check's manifest (D-120).
func GitHubManifest() Manifest {
	return Manifest{
		Provider: "github", Protocol: "GitHub REST API " + apiVersion + ", unauthenticated, public repository releases only", VerifiedAt: "2026-11-16",
		Docs: []string{"https://docs.github.com/en/rest/releases/releases", "https://docs.github.com/en/rest/releases/assets",
			"https://docs.github.com/en/rest/about-the-rest-api/api-versions", "https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api",
			"https://docs.github.com/en/rest/using-the-rest-api/getting-started-with-the-rest-api"},
		Capabilities: []Capability{
			{Name: "releases.list", Support: "supported", Notes: "GET /repos/{owner}/{repo}/releases?per_page=30: published releases with pre-releases; " +
				"drafts only for people with push access, so never seen. The order isn't documented, so every release is compared by its tag's " +
				"version (v and SemVer). /releases/latest isn't used: it leaves out pre-releases, and the beta is pre-releases."},
			{Name: "releases.assets", Support: "supported", Notes: "Each asset's browser_download_url, followed through redirects over HTTPS only; " +
				"size is used as an upper bound. Assets whose state isn't \"uploaded\" are ignored. The asset digest field exists but its " +
				"format isn't documented, so it isn't used: integrity comes from the signed SHA256SUMS.txt."},
			{Name: "rate_limits", Support: "supported", Notes: "60 requests an hour per IP address without signing in. The app checks once a day " +
				"and at start, and Check now at most once a minute. 403 or 429 with x-ratelimit-reset: no request until then, as GitHub asks."},
			{Name: "user_agent", Support: "supported", Notes: "Required. StreamGremlin/VERSION: the app's name and version, nothing about the streamer."},
			{Name: "conditional_requests", Support: "unknown", Notes: "Whether a 304 counts against the limit isn't on the pages read, so ETags aren't used."},
			{Name: "authentication", Support: "unsupported", Notes: "Never signs in to GitHub: no token is kept or sent."},
		},
	}
}
