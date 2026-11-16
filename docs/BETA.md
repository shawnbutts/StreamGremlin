StreamGremlin beta guide
========================

Thank you for trying StreamGremlin! It's stream automation for Twitch that
runs on your own computer: chat commands, alerts in OBS, sounds, timers,
giveaways, points, wheels, a Live screen to run your stream from, and Stream
Deck keys, with YouTube alongside. Nothing is sent anywhere but the services
you connect (Twitch, YouTube, OBS, Discord) and GitHub, to check for
updates.

This is a beta, so expect rough edges, and please tell us about them.


Before you start
----------------

- StreamGremlin is free and open source. Replies to reports may take a
  few days.
- A beta can break. Keep the bot you use now (if you have one) ready the
  first few times you stream with StreamGremlin, and don't rely on it alone
  for anything you can't redo, such as a giveaway with a real prize.
- Your data stays between betas: each new beta opens the last one's data,
  and is tested doing so before it's released. StreamGremlin backs up
  before it installs an update, and Settings, Backups makes a copy any
  time.
- Windows and macOS warn about StreamGremlin when you first open it. That's
  expected for now: see "Why Windows and macOS warn you" below.


Getting StreamGremlin
---------------------

Download it only from StreamGremlin's releases on GitHub,
https://github.com/shawnbutts/StreamGremlin/releases, or from Download on
https://streamgremlin.com, which links to the same files. A copy from
anywhere else may not be StreamGremlin.

Each release lists the SHA-256 of every file, in SHA256SUMS.txt and on
Download. To check yours, work out its SHA-256 and compare it with the
list. They must match exactly; if they don't, delete the file and tell us.

- Windows, in PowerShell:
  Get-FileHash .\StreamGremlin-VERSION-windows-x64-setup.exe
- macOS, in Terminal:
  shasum -a 256 StreamGremlin-VERSION-macos.dmg
- Linux, in the folder with the files and SHA256SUMS.txt:
  sha256sum --check --ignore-missing SHA256SUMS.txt


Why Windows and macOS warn you
------------------------------

Apple and Microsoft expect apps to be signed with a certificate that they
check, and warn about any app that isn't. StreamGremlin isn't yet: the
certificates cost a yearly fee, and they'll come later. So:

- Windows says "Windows protected your PC". Choose "More info", then "Run
  anyway".
- macOS says it can't check the app for malicious software. Allow it once
  in System Settings, as below.

The warnings mean only that nobody has vouched for the file, which is why
checking its SHA-256 is worth the minute. Updates are different: those are
signed with StreamGremlin's own key, and the app installs an update only if
the signature checks out (see "Updating").


Installing on Windows (10 or 11, 64-bit)
----------------------------------------

1. Run StreamGremlin-VERSION-windows-x64-setup.exe.
2. Windows warns: "Windows protected your PC". Choose "More info", then
   "Run anyway".
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
   for malicious software. Choose "Done" (or "OK"), not "Move to Bin".
4. Open System Settings, then Privacy & Security. Scroll down to "StreamGremlin
   was blocked to protect your Mac" and choose "Open Anyway". Confirm with
   your password.
5. You only do this once per version.

The dmg also holds the Stream Deck plugin. The command line is inside the
app, at StreamGremlin.app/Contents/Helpers/streamgremlin.


Installing on Linux (beta, lightly tested)
------------------------------------------

Linux is lightly tested: it's built, installed, upgraded and started on
Ubuntu, Debian and Fedora before each release, but nobody has streamed with
it yet. Please tell us how it goes. It needs a 64-bit Intel or AMD computer
and Ubuntu 24.04, Debian 13, Fedora 42 or newer (or another system as new).

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
Connections, Stream Deck, "Pair a Stream Deck". "Counter", "Status" and
"Switch" keys show a variable, whether you're live or connected, and turn
workflows, timers or the chat lock on and off: choose what they may use on
Connections, under "What your Stream Deck can use".


Updating
--------

From 0.1.0-beta.6, StreamGremlin checks for a new version when it starts
and once a day, and Home says when one is out. In Settings, under Updates,
choose Download, then Install and restart: StreamGremlin backs up, closes,
installs the new version and opens again. It installs only an update signed
with StreamGremlin's own key, and never while you're live. Checking sends
GitHub only StreamGremlin's name and version; you can turn it off there.

On macOS, run StreamGremlin from Applications, not from the disk image.
After an update, macOS may ask again whether StreamGremlin can use your
keychain: choose Always Allow. On Linux, StreamGremlin tells you a new
version is out; install it as below.

To update by hand, install the new version over the old one: on Windows run
the new installer, on macOS drag the new app over the old one, and on Linux
install the new package the same way as the first. Close StreamGremlin
first. Your workflows, settings and sign-ins are kept.

Your data carries on into the first public release, 0.1.0, too.


What leaves your computer
-------------------------

StreamGremlin has no servers and no account. There are no analytics and no
crash reports. It talks to Twitch, YouTube, OBS and Discord only when you
connect them, and to GitHub to check for updates, sending only its name and
version. The Privacy page on https://streamgremlin.com says more.


Telling us what went wrong
--------------------------

Report it on GitHub (you'll need a free account):
https://github.com/shawnbutts/StreamGremlin/issues/new/choose
Help in the app has a button for it. Look first for an issue that already
describes it, and add to that one. Everything on GitHub is public, so leave
out passwords, sign-in codes and Discord webhook links. The most useful
report says:
- what you did, what you expected, and what happened instead;
- the version, shown on Home under "Version";
- Windows, macOS or Linux, and which version (on Linux, which
  distribution);
- the log files, if you can: they're in the "logs" folder inside your
  StreamGremlin data folder (below). Logs never contain your passwords or
  sign-in tokens, but they do contain chat names and messages from your
  stream, so attach only what you're happy to share in public.

A screenshot helps too. Ideas are welcome the same way: the form asks who
would use it, and how you do it today.

We'd most like to hear:
- anything that went wrong during a real stream;
- where you got stuck setting up, or had to guess;
- what you did in your old bot that you can't do here;
- how installing, updating and the warnings went.

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
- Installers signed with Apple's and Microsoft's certificates.
- Updating from inside the app on Linux.


Uninstalling
------------

- Windows: Settings, Apps, StreamGremlin, Uninstall.
- macOS: drag StreamGremlin from Applications to the Bin.
- Linux: sudo apt remove streamgremlin, or sudo dnf remove streamgremlin.

Uninstalling keeps your data folder, in case you come back. Delete it
yourself to remove everything.
