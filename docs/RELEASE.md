# Release checklist

One page for each release, in order. Why each step is there is in [CONTRIBUTING.md](../CONTRIBUTING.md#releases-on-github). Copy it into the release's notes or tick it here as you go; `V` is the version, such as `0.1.0-beta.7`.

## Before the tag

- [ ] `main` is clean, and pushed to `origin` and `github`.
- [ ] `make verify` and `make windows` pass.
- [ ] `make test-fuzz`, `make test-load` and `make vuln` pass ([ADR-0050](adr/0050-security-and-load-testing.md)).
- [ ] If a screen changed since the last release: `make screenshots`, and commit the pictures.
- [ ] [docs/BETA.md](BETA.md) is still right: system versions, the warnings and how to get past them, what isn't there yet.
- [ ] In `CHANGELOG.md`, move **Unreleased** to `## V (YYYY-MM-DD)`, leaving Unreleased with "Nothing yet." Commit and push.

## Build

- [ ] `git tag vV`, then `git push github vV` and `git push origin vV`.
- [ ] GitHub's Release workflow passes all three jobs and makes a **draft** release with every file.

## Sign

- [ ] `make release-fetch VERSION=V`: downloads the draft into `bin/release` and checks every file.
- [ ] `make release-sign VERSION=V`: signs `SHA256SUMS.txt` and uploads `SHA256SUMS.txt.sig`.
- [ ] From now on, don't re-run the workflow for this tag. It would replace `SHA256SUMS.txt`, and the app wouldn't install the release until it's signed again.

## Check on your own computers

With the installers in `bin/release`, on the Mac and the Windows PC ([M9's installer checks](milestones/M9.md#check-for-slice-3-installers-user)):

- [ ] Install over the previous release: everything is still there, and the version on Home is `V`.
- [ ] The Stream Deck plugin still presses a button.

## Publish

- [ ] `gh release edit vV --repo shawnbutts/StreamGremlin --draft=false`
- [ ] On a computer still on the previous release: Settings, Updates, Check now, then Download and Install and restart. It comes back as `V`, with a "Before installing an update" backup.

## After

- [ ] `make upgrade-fixture VERSION=V`, then commit `tests/testdata/upgrade/V` and push.
- [ ] `make site`, then upload `bin/site/www` to streamgremlin.com and `bin/site/code` to code.streamgremlin.com.
- [ ] Tell testers: the release's page on GitHub, and what to try.

## Never

- Commit or upload the signing key (`~/.config/streamgremlin-release/signing.key`), the Twitch Client ID or any other secret.
- Put real stream data or a tester's files in the repository.
