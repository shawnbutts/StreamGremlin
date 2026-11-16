# Security

StreamGremlin runs on your own computer and signs in to Twitch, YouTube, OBS and Discord for you. If you find a way to misuse it, please tell us privately first.

## Reporting a problem

- **Report it privately on GitHub:** [open a security advisory](https://github.com/shawnbutts/StreamGremlin/security/advisories/new) (the repository's Security tab, then "Report a vulnerability"), with what you found, how to make it happen, the version (Help shows it) and your system. Only the maintainers can see it. In the app, Help has a button for it.
- Please don't put it in a public issue, discussion or chat until it's fixed.
- Please don't include your own sign-ins, tokens or the contents of your data folder. If a log is needed, we'll say which, and StreamGremlin's logs never contain tokens.

We'll say when we've read it, work on a fix, and credit you in the changelog if you'd like. StreamGremlin is made by a small team without a bug bounty.

## What's supported

Only the newest beta gets fixes. Updating means installing the newest build; your data folder carries over.

## What StreamGremlin does to stay safe

- **Nothing listens beyond your computer.** The engine's API and the overlay pages answer only on `127.0.0.1`, refuse requests addressed to any other name (which stops web pages using DNS rebinding) and refuse browser origins they don't know.
- **Every API request needs a token,** kept in the data folder only you can read, except a health check and swapping a Stream Deck's pairing code for its own token. A paired Stream Deck can only see and press buttons. A test checks every route for this.
- **Sign-ins live in your system's keyring** (Keychain, Credential Manager, Secret Service), or an encrypted file where none answers. They never appear in logs, backups, exports or bug reports.
- **Add-ons and Lua scripts are untrusted:** each runs in its own process with time and memory limits, can only do what you approved, and everything it does is recorded.
- **Content packs and themes are data only.** They can't run code on your computer, and installing one can't write outside its own folder.
- **Winners are drawn on your computer** with a cryptographically secure random source, and recorded.
- **Updates install only if they're signed** with StreamGremlin's own release key, which never leaves the maintainer's computer, and every file must match the signed list ([ADR-0052](docs/adr/0052-updates.md)). The app downloads an update only when you ask, and installs it only after asking again.
- **What comes from outside** (chat, Twitch and YouTube events, add-ons' messages, files you import) is checked by fuzz tests ([ADR-0050](docs/adr/0050-security-and-load-testing.md)).

## What's not covered

- Someone who can already use your computer as you, or read your data folder.
- Twitch's, YouTube's, OBS's or Discord's own security.
- Builds you didn't get from streamgremlin.com or [GitHub](https://github.com/shawnbutts/StreamGremlin).
