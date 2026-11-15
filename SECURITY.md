# Security

StreamGremlin runs on your own computer and signs in to Twitch, YouTube, OBS and Discord for you. If you find a way to misuse it, please tell us privately first.

## Reporting a problem

- **Email `security@streamgremlin.com`** with what you found, how to make it happen, the version (Help shows it) and your system. Please don't put it in a public issue or chat until it's fixed.
- **During the beta,** you can also tell the person who invited you, privately.
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
- **What comes from outside** (chat, Twitch and YouTube events, add-ons' messages, files you import) is checked by fuzz tests ([ADR-0050](docs/adr/0050-security-and-load-testing.md)).

## What's not covered

- Someone who can already use your computer as you, or read your data folder.
- Twitch's, YouTube's, OBS's or Discord's own security.
- Builds you didn't get from the person who invited you or from streamgremlin.com.
