# Changelog

All notable changes to StreamGremlin. Versions follow [Semantic Versioning](https://semver.org) from the first release.

## Unreleased

### Added

- M1 slice 1, the skeleton:
  - The engine, with its data folder, a lock so only one engine uses it, and a SQLite database with a baseline schema and automatic backups before schema changes.
  - The authenticated local API, loopback only: token, Host and Origin checks, permissions per route, and a version header.
  - The `streamgremlin` command line, with `status` and `version`.
  - The desktop app (Wails v3, Svelte 5):
    - a **Home** screen showing whether the engine is running, its version, how long it has been running, and the data folder;
    - a **Settings** screen with the colour theme: System, Dark workshop, Light workshop or Midnight blue.
- M1 slice 2, secrets:
  - Sign-ins and passwords are kept in the macOS Keychain, Windows Credential Manager or Secret Service. Where none answers, they go in an encrypted file in the data folder. Values never appear in the database, logs, backups or API answers.
  - **Settings → Passwords and sign-ins** shows where they're kept and which are saved. It can check that each one can still be read, and forget one after asking.
  - `streamgremlin secrets [-check] [-json]` and `streamgremlin secrets forget KEY`.
- M1 slice 3, events:
  - The event inbox. Every event is stored once in arrival order, platform redeliveries are recognised, and any reader can resume where it left off.
  - An **Events** screen: live events, newest first, with details, and a form to send test events (chat message, chat command, OBS scene change). Test events are marked "Test".
  - `streamgremlin events`, `events watch`, `events simulate TYPE field=value…` and `events types`.
- M1 slice 4, Twitch sign-in:
  - A **Connections** screen. You sign in to Twitch by entering a code at twitch.tv/activate, which the app opens in your browser. An optional bot account sends chat under its own name.
  - Sign-ins are kept in your system's keyring, refreshed and checked hourly. A sign-in Twitch ends is shown with the reason; an unreachable Twitch is shown as unknown.
  - Your own Twitch app's Client ID can be pasted in. Release builds will include StreamGremlin's.
  - `streamgremlin twitch [status|sign-in|sign-out|client-id]`, with `-bot` for the bot account.
  - `mocktwitch`, a stand-in for Twitch's sign-in server, for tests and for trying the app.
- M1 slice 5, Twitch chat:
  - StreamGremlin reads your channel's chat while you're signed in. Messages and `!commands` show live on Events. It reconnects by itself, and says so when chat is lost (Twitch doesn't resend missed messages).
  - **Connections → Twitch → Chat:** whether chat is being read, and a box to send a message as the bot or as you. It explains `/mod` when the bot isn't a moderator.
  - Sending stays under Twitch's rate limits, is never retried, and says exactly why a message wasn't sent.
  - The bot's own messages are never treated as viewer chat.
  - `streamgremlin chat send`. mocktwitch gains chat, with a `/chat` page to type as a viewer.

### Changed

- Screen shortcuts: Home Ctrl/⌘+1, Connections 2, Events 3, Settings 4.

- Development schema changes are now new numbered files, so data folders keep working between builds. They're squashed into one baseline at the first public release.

- The baseline schema gained the secrets tables. A development data folder from slice 1 has to be moved aside.
