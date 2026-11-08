# Pinky

Pinky lets AI agents operate a person's real iPhone or Android phone over MCP, with the person watching, approving and taking over. It is a Go desktop app (MyGo native UI) with an Android companion app; see [README.md](README.md).

Read [docs/WORKING.md](docs/WORKING.md) before you change anything: who owns which part of the code, how to use the real phones safely, and the rules for code and commits. Build and test with `make check` (build, vet, test); [README.md](README.md) has the rest.

## README

The README comes in English, [README.md](README.md), and in Simplified Chinese, [README.zh-CN.md](README.zh-CN.md), which follows it section by section (building from source is only summed up there). Change both in the same commit. Write the Chinese with the app's own names for menus and buttons (`cmd/pinky/locales/zh-Hans.json`), and give a new picture with words in it a Chinese copy next to the English one (`assets/readme/*-zh.*`).

## Releases

This checkout is flyfox's fork: `origin` is `yaoqiangyao/pinky`, and `upstream` is `DIYgod/pinky`, whose changes are merged in by hand. Its releases are Windows only, for iPhones on iOS 27 or later: no companion APK, no macOS or Linux packages, no Homebrew cask, and no telemetry. Keep the fork's changes small so that upstream merges stay easy.

Pinky releases often, several times a day when there is something to ship. Versions are the date in Asia/Shanghai and that day's count, `YYYY.MDD.N` (`2026.1107.2` is the second release of 7 November 2026), and are never written by hand. [docs/RELEASING.md](docs/RELEASING.md) explains the whole pipeline.

Whoever pushes to `main` decides whether the push is a release:

- **Release** when the push changes what people get: a fix, a feature, a change in behaviour or in what they see or read in the app, the companion app, or what the packages hold.
- **Don't release** when the push changes only docs, tests, CI, development tools, or code that changes nothing people get (a refactor without a change in behaviour). Such changes ride along with the next release.
- When unsure, release. Releases are cheap.

To release:

1. Write the notes in [CHANGELOG.md](CHANGELOG.md) under the `## Unreleased` heading at its top (add the heading when it is not there; notes of earlier pushes may already wait under it). Write short bullets in English for the people who use Pinky: what changed for them, not how. "Unlocking a phone from the computer now works when it asks for a fingerprint first", not "fix(android): handle the fingerprint prompt".
2. Write the same bullets in Simplified Chinese under `## Unreleased` at the top of [CHANGELOG.zh-CN.md](CHANGELOG.zh-CN.md), one for each English bullet and in the same order: Pinky in Chinese shows these in What's new and on the update card. Write natural Chinese for users, with the app's own names for menus and buttons from `cmd/pinky/locales/zh-Hans.json` in 「」 ("「设置 › 手机」"), and a space between Chinese and Latin letters or digits, as [README.zh-CN.md](README.zh-CN.md) does. Commit both files with the change.
3. On an up-to-date `main` with nothing else to commit, run

   ```sh
   scripts/release.sh tag -push -trailer "Co-Authored-By: <the line your commits end with>"
   ```

   It gives the notes in both files the next version, commits `chore(release): <version>`, tags it `v<version>` and pushes `main` and the tag together. It refuses when CHANGELOG.zh-CN.md has no notes under `## Unreleased` or another number of bullets than CHANGELOG.md. The tag starts the release workflow, which builds, signs, notarizes and publishes the release, and the Homebrew tap picks up its cask; build nothing yourself. `-n` shows what it would do.
4. If the push is refused because `origin/main` moved, the script undoes its commit and tag: `git pull --rebase origin main`, then run it again.

A push that is not a release leaves `## Unreleased` as it is, in both files. Never change a released section's heading, never move or reuse a tag, and never push a `v*` tag other than through `scripts/release.sh tag`.

Working on a branch that someone else merges? Leave CHANGELOG.md and CHANGELOG.zh-CN.md alone (parallel branches would conflict there) and end your report with the bullets you suggest for the notes, in English and in Chinese; whoever merges and pushes writes them.
