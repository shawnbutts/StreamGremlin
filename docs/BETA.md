StreamGremlin beta guide
========================

Thank you for trying StreamGremlin! It's stream automation for Twitch that
runs on your own computer: chat commands, alerts in OBS, sounds, timers,
giveaways, points, wheels, a Live screen to run your stream from, and Stream
Deck keys. Nothing is sent anywhere but Twitch and OBS.

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

The installer also puts these in StreamGremlin's folder
(%LocalAppData%\Programs\StreamGremlin):
- the Stream Deck plugin, in "Stream Deck";
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


Getting started
---------------

- Home has a "Get set up" checklist that ticks itself as you go.
- Connections: sign in to Twitch (you'll get a code to enter on Twitch's
  site), and optionally a bot account. Connect OBS with OBS's WebSocket
  server: in OBS, Tools, then WebSocket Server Settings.
- Workflows: "when this happens, do that". Start from a template.
- Live: your chat, moderation, and your own buttons.
- Help (Ctrl+/ on Windows, Cmd+/ on macOS) explains every screen.

Stream Deck (Stream Deck 7.1 or later): double-click
com.streamgremlin.streamdeck.streamDeckPlugin to install it. Drag "Press a
button" onto a key, then in the key's settings type the pairing code from
Connections, Stream Deck, "Pair a Stream Deck".


Updating
--------

Install the new version over the old one: on Windows run the new installer,
on macOS drag the new app over the old one. Close StreamGremlin first. Your
workflows, settings and sign-ins are kept.

One exception: when StreamGremlin's first public release comes out, beta
data may need a fresh start. You'll be told before that happens.


Telling us what went wrong
--------------------------

Send it to the person who invited you to the beta. The most useful report
says:
- what you did, what you expected, and what happened instead;
- the version, shown on Home under "Version";
- Windows or macOS, and which version;
- the log files, if you can: they're in the "logs" folder inside your
  StreamGremlin data folder (below). Logs never contain your passwords or
  sign-in tokens, but they do contain chat names and messages from your
  stream.

A screenshot helps too.


Where your data is
------------------

- Windows: %LocalAppData%\StreamGremlin
- macOS: ~/Library/Application Support/StreamGremlin

Help, "Your data", shows the exact folder. Passwords and sign-ins are kept
in Windows Credential Manager or the macOS Keychain, not in that folder.
Settings, Backups makes copies of your data.


What isn't there yet
--------------------

- YouTube (planned next).
- Importing from Streamer.bot.
- Signed installers, and automatic updates.


Uninstalling
------------

- Windows: Settings, Apps, StreamGremlin, Uninstall.
- macOS: drag StreamGremlin from Applications to the Bin.

Uninstalling keeps your data folder, in case you come back. Delete it
yourself to remove everything.
