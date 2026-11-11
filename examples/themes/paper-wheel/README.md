# Paper wheel

An example of an installed StreamGremlin wheel theme
(docs/adr/0036-packages-plugins-and-themes.md). To try it, compress this
folder into a ZIP (Finder: Compress; Explorer: Send to, Compressed folder)
and install it on the **Add-ons** screen.

It runs in a sandbox with no network, and draws the result StreamGremlin
drew and sends it (an add-on could draw anything: chat always has the
real result). Its sounds, start.wav, end.wav and cheer.wav beside
its page, play as a spin starts, as it stops and as the result shows; a
sound the streamer picks plays instead. They're made by
internal/overlays/themesounds (go test ./internal/overlays/themesounds
-update rewrites them).
