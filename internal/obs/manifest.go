package obs

// Capability is one thing the OBS adapter does, and what it relies on.
type Capability struct {
	Name    string `json:"name"`
	Support string `json:"support"` // supported, partial, unsupported or unknown
	Notes   string `json:"notes,omitempty"`
}

// Manifest records what the adapter relies on, checked against
// obs-websocket's protocol document on VerifiedAt (AGENTS.md).
type Manifest struct {
	Provider     string       `json:"provider"`
	Protocol     string       `json:"protocol"`
	VerifiedAt   string       `json:"verified_at"`
	Docs         []string     `json:"docs"`
	Capabilities []Capability `json:"capabilities"`
}

// OBSManifest is the adapter's manifest.
func OBSManifest() Manifest {
	return Manifest{
		Provider: "obs", Protocol: "obs-websocket 5, RPC version 1 (OBS 28 and newer)", VerifiedAt: "2026-11-08",
		Docs: []string{"https://github.com/obsproject/obs-websocket/blob/master/docs/generated/protocol.md"},
		Capabilities: []Capability{
			{Name: "connect", Support: "supported", Notes: "obswebsocket.json subprotocol; SHA-256 challenge authentication. 4009 (password), 4010 (version) and 4011 (Kick) stop retrying until the settings are saved again; the protocol says not to reconnect after a Kick. The default port, 4455, isn't stated in the protocol document; it's OBS's own default."},
			{Name: "events", Support: "supported", Notes: "General, Scenes, Inputs, Filters, Outputs, SceneItems and MediaInputs; no high-volume events. The protocol doesn't say whether a request's event can arrive before its answer, so a switch is recorded as pending before it's sent."},
			{Name: "scenes.switch", Support: "supported", Notes: "SetCurrentProgramScene; the program scene only, not studio mode's preview."},
			{Name: "scenes.snapshot", Support: "supported", Notes: "Switches back only while StreamGremlin still owns the scene. OBS has no conditional switch, so a manual change in the milliseconds between the check and the switch back can still be overridden."},
			{Name: "sources.visibility", Support: "supported", Notes: "GetSceneItemId then SetSceneItemEnabled: the source's first placement in the scene."},
			{Name: "filters", Support: "supported", Notes: "On, off or toggle; toggle reads first and isn't safe to repeat."},
			{Name: "audio.mute", Support: "supported", Notes: "Toggle uses ToggleInputMute: one request, but two at once each flip it."},
			{Name: "text", Support: "supported", Notes: "Only text_gdiplus* and text_ft2_source* inputs; sets text alone (overlay)."},
			{Name: "media", Support: "supported", Notes: "Play, pause, stop, restart, next, previous."},
			{Name: "recording", Support: "supported", Notes: "Start and stop read the state first and do nothing when it already is."},
			{Name: "replay_buffer.save", Support: "partial", Notes: "Only while the replay buffer is running; StreamGremlin doesn't start it. The file name arrives as an event."},
			{Name: "screenshot", Support: "supported", Notes: "PNG into a literal full path on the computer OBS runs on; StreamGremlin names the file, so nothing is replaced."},
			{Name: "stream.start_stop", Support: "unsupported", Notes: "Starting or stopping the stream waits for a confirmation design."},
		},
	}
}
