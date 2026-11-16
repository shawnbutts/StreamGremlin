StreamGremlin beta guide
========================

Thank you for trying StreamGremlin! It's stream automation for Twitch that
runs on your own computer: chat commands, alerts in OBS, sounds, timers,
giveaways, points, wheels, a Live screen to run your stream from, and Stream
Deck keys, with YouTube alongside. Nothing is sent anywhere but the services
you connect (Twitch, YouTube, OBS, Discord).

This is a beta, so expect rough edges, and please tell us about them.


Installing on Windows (10 or 11, 64-bit)
----------------------------------------

1. Run StreamGremlin-VERSION-windows-x64-setup.exe.
2. Windows warns: "Windows protected your PC". The beta isn't signed with a
   certificate yet, so Windows doesn't recognise it. Choose "More info",
   then "Run anyway".
3. The installer puts StreamGremlin in your user folder. It doesn't need an
   administrator. Untick "Desktop shortcut" if you don't want one.
4. Open StreamGremlin from the Start menu.

StreamGremlin's window needs Microsoft Edge WebView2, which Windows 10 and
11 include. If it's missing, the installer offers Microsoft's download.

On the installer's last page, tick "Install the Stream Deck plugin" if you
have a Stream Deck: Stream Deck asks to install it.

The installer also puts these in StreamGremlin's folder
(%LocalAppData%\Programs\StreamGremlin):
- the Stream Deck plugin, in "Stream Deck", to install it later;
- the command line, cli\streamgremlin.exe.


Installing on macOS (13 Ventura or later, Apple silicon or Intel)
-----------------------------------------------------------------

1. Open StreamGremlin-VERSION-macos.dmg.
2. Drag StreamGremlin to Applications.
3. Open StreamGremlin from Applications. macOS says it can't check the app
   for malicious software, because the beta isn't notarised by Apple yet.
   Choose "Done" (or "OK"), not "Move to Bin".
4. Open System Settings, then Privacy & Security. Scroll down to "StreamGremlin
   was blocked to protect your Mac" and choose "Open Anyway". Confirm with
   your password.
5. You only do this once per version.

The dmg also holds the Stream Deck plugin. The command line is inside the
app, at StreamGremlin.app/Contents/Helpers/streamgremlin.


Installing on Linux (beta, lightly tested)
------------------------------------------

Linux is lightly tested: it's built and started on Ubuntu, Debian and
Fedora before each release, but nobody has streamed with it yet. Please
tell us how it goes. It needs a 64-bit Intel or AMD computer and Ubuntu
24.04, Debian 13, Fedora 42 or newer (or another system as new).

- Ubuntu or Debian:
  sudo apt install ./StreamGremlin-VERSION-linux-x64.deb
- Fedora:
  sudo dnf install ./StreamGremlin-VERSION-linux-x64.rpm
- Anything else: unpack StreamGremlin-VERSION-linux-x64.tar.gz and run
  streamgremlin-desktop. It needs GTK 4 and WebKitGTK 6.0 from your system.

Then open StreamGremlin from your applications. The command line is
streamgremlin. Sign-ins are kept by your desktop's keyring (GNOME Keyring
or KWallet); without one, they go in an encrypted file in your data folder.
Stream Deck's software doesn't run on Linux, so the plugin isn't included.


Getting started
---------------

- Home has a "Get set up" checklist that ticks itself as you go.
- Connections: sign in to Twitch (you'll get a code to enter on Twitch's
  site), and optionally a bot account. Connect OBS with OBS's WebSocket
  server: in OBS, Tools, then WebSocket Server Settings.
- Workflows: "when this happens, do that". Start from a template.
- Live: your chat, moderation, and your own buttons.
- Chat filters: catch links, shouting, spam and words you don't want.
- Help (Ctrl+/ on Windows and Linux, Cmd+/ on macOS) explains every screen.

Stream Deck (Stream Deck 7.1 or later, macOS and Windows): double-click
com.streamgremlin.streamdeck.streamDeckPlugin to install it (on Windows, or
tick the box at the end of installing). The release on GitHub has the same
plugin on its own too, StreamGremlin-VERSION-streamdeck.streamDeckPlugin,
to install or update it without reinstalling. Drag "Press a
button" onto a key, then in the key's settings type the pairing code from
Connections, Stream Deck, "Pair a Stream Deck".


Updating
--------

Install the new version over the old one: on Windows run the new installer,
on macOS drag the new app over the old one, and on Linux install the new
package the same way as the first. Close StreamGremlin first. Your
workflows, settings and sign-ins are kept.

One exception: when StreamGremlin's first public release comes out, beta
data may need a fresh start. You'll be told before that happens.


Telling us what went wrong
--------------------------

Report it on GitHub (you'll need a free account):
https://github.com/shawnbutts/StreamGremlin/issues/new/choose
Help in the app has a button for it. Look first for an issue that already
describes it, and add to that one. The most useful report says:
- what you did, what you expected, and what happened instead;
- the version, shown on Home under "Version";
- Windows, macOS or Linux, and which version (on Linux, which
  distribution);
- the log files, if you can: they're in the "logs" folder inside your
  StreamGremlin data folder (below). Logs never contain your passwords or
  sign-in tokens, but they do contain chat names and messages from your
  stream, so attach only what you're happy to share in public.

A screenshot helps too.

Found a security problem? Don't open a public issue: report it privately,
as SECURITY.md says (https://github.com/shawnbutts/StreamGremlin/security).


Where your data is
------------------

- Windows: %LocalAppData%\StreamGremlin
- macOS: ~/Library/Application Support/StreamGremlin
- Linux: ~/.local/share/streamgremlin

Help, "Your data", shows the exact folder. Passwords and sign-ins are kept
in Windows Credential Manager, the macOS Keychain or your Linux desktop's
keyring, not in that folder.
Settings, Backups makes copies of your data.


What isn't there yet
--------------------

- Importing from Streamer.bot (after the first public release).
- Signed installers, and automatic updates.


Uninstalling
------------

- Windows: Settings, Apps, StreamGremlin, Uninstall.
- macOS: drag StreamGremlin from Applications to the Bin.
- Linux: sudo apt remove streamgremlin, or sudo dnf remove streamgremlin.

Uninstalling keeps your data folder, in case you come back. Delete it
yourself to remove everything.
