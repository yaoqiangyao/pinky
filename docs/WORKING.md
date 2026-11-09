# Working on Pinky

These rules are for everyone working on the code, people and agents alike. Several agent sessions build Pinky in parallel; the rules keep them out of each other's way and keep the owner's phones safe. The design is in [DESIGN.md](DESIGN.md) (source of truth while drafting: https://claude.ai/artifact/1tETTimeUQMqvjvQf5ALsn). The UI prototype is https://claude.ai/artifact/EsCTP9Xsphcz8C6jbGsQM2.

## Layout and ownership

| Path | Owner | Notes |
| --- | --- | --- |
| `internal/screen`, `internal/driver/driver.go`, `internal/gateway/api.go` | Shared contracts | Additive changes only, kept compiling; say so in your report |
| `internal/driver/android/` | Android driver session | Not `companion/` below it |
| `internal/driver/android/companion/`, `android/companion/` | Companion session | Kotlin app and its Go client |
| `internal/driver/ios/` | iOS driver session | |
| `internal/video/`, `ui/`, `assets/` | Desktop client session | |
| `internal/gateway/` (but `api.go`), `internal/policy/`, `internal/approval/`, `internal/history/`, `internal/mcp/`, `cmd/pinky/` | Main session | Integrates everything |
| `internal/update/`, `ui/update.go`, `cmd/pinky/update.go`, `cmd/pinky/shimconn.go` | Auto-update session | The feed contract with the release pipeline is in `docs/notes/updates.md` |
| `docs/` | Anyone | Keep DESIGN.md as the reference; add notes under `docs/notes/<area>.md` |

Do not edit files another session owns. If you need a change in a shared contract, make it additive and describe it in your final report.

## Devices

| Device | ID | Primary user |
| --- | --- | --- |
| Mi 10 Pro, LineageOS, Android 17 | `3e8be48e` | Android driver session |
| Pixel (2016), Android 10 | `FA6C20303860` | Companion session |
| iPhone 13 mini, iOS 27.0.1 | `00008110-001E30C9262A801E` | iOS driver session |

Hold the device's lock for every command that touches a phone:

```sh
scripts/devlock.sh <device-id> <command...>
```

Locks are per device and wait up to 30 minutes. Keep each hold short (one test run) so other sessions can get in. Another session may briefly use your primary device; never assume it is in the state you left it.

## Safety rules for real phones

These are the owner's own phones, and nobody is watching while you test.

- Use only: the home screen and app drawer, Settings (navigate and read; never change a setting), Calculator, Clock (view only), and Pinky's own companion or test apps.
- Never: send messages or email, place calls, buy or pay, sign in or out, change any setting, account or security option, install or uninstall anything except Pinky's own apps, clear app data, reboot, power off, or press the power or lock key.
- Never open apps that hold personal data: messages, photos, mail, banking, wallets, notes, contacts.
- Never commit screenshots, recordings or screen dumps from real devices. Keep them under `/tmp/pinky-*` and delete them when you are done.
- If a phone is locked, shows a dialog you did not expect, or asks for a password: stop using that phone, leave it as it is, and say so in your report.
- Pace input like a person: at least about 300 ms between actions.

## Code and commits

- Go 1.27.3, module `github.com/DIYgod/pinky`. No cgo: `CGO_ENABLED=0 go build ./...` must pass. Call C libraries through purego.
- English in code, comments, docs and commit messages.
- Words the app shows belong in the catalogs in `cmd/pinky/locales`. A new or changed message gets its English (`en.json`, with a description), Simplified Chinese (`zh-Hans.json`) and Japanese (`ja.json`) in the same commit; `go test ./cmd/pinky/locales` fails when one is missing. See `docs/notes/client.md` ("i18n").
- Conventional Commits, such as `feat(android): stream video over scrcpy`. End every commit message with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- `go build ./... && go vet ./... && go test ./...` must pass before each commit. Tests that need a real phone use the `device` build tag and read the device id from `PINKY_DEVICE`; run them through `scripts/devlock.sh`.
- Work on your own branch. Push it to `origin` (a private repository) whenever it reaches a working state. Never push to `main`, never force-push. The main session merges.
- Write what you built, how to run it, what works on which device, and what does not into `docs/notes/<area>.md` on your branch.
