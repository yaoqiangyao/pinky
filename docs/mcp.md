# Pinky MCP reference

Pinky's gateway serves one MCP server, `pinky`, with 14 tools. Agents launch `pinkyctl mcp` (Pinky's executable with the argument `mcp`), a stdio shim that forwards to the running gateway over its user-only Unix socket (and starts the gateway first when none runs: the Pinky app on macOS, `pinky daemon` elsewhere). Agents that cannot start a command can use Streamable HTTP instead (below). The agent-facing guide is [SKILL.md](SKILL.md); the design is in [DESIGN.md](DESIGN.md), section "Agent interface".

## Connecting an agent

### With a click

Settings › Agents › Connect an agent lists the agents Pinky finds on this computer, each with Connect, and connects one with a click. Until some agent can reach Pinky (its settings have Pinky, or one has connected), a card in the sidebar offers the same: Connect when Pinky found one agent, Choose… for several, and where to get one (Claude Code, Claude Desktop) when there is none; the Tasks list says no agent is connected.

Connect adds an MCP server named `pinky` to the agent's own settings, which starts this Pinky by its full path with the argument `mcp` (as `/Applications/Pinky.app/Contents/MacOS/Pinky mcp`): no other program can stand in for a path, and agents started from the Dock or the Start menu find it without the terminal's `PATH`.

- Pinky changes nothing until the person clicks Connect (or Update, Fix or Remove) for that agent.
- Where the agent has a command for its MCP servers, Pinky runs it, found on the `PATH` of the person's login shell and where installers put it: `claude mcp add --scope user pinky -- <path> mcp`, `codex mcp add pinky -- <path> mcp`, `copilot mcp add pinky -- <path> mcp`, `gemini mcp add --scope user pinky <path> mcp`, and `… mcp remove` for Remove. Otherwise Pinky changes the settings file itself, in Pinky's entry only: the rest of the file stays as it was, comments, order and formatting included. Pinky reads the file again after the change and refuses one that would alter anything else, and it leaves a file with a mistake in it alone: Settings says what is wrong where, with Show the file.
- Before any change Pinky keeps a copy of the file as it was in its data folder, `agents/backups/<agent>/<time>-<file>` (the newest ten for each agent), and Settings offers to show it.
- It installs Pinky's skill, [SKILL.md](SKILL.md), as `pinky/SKILL.md` in the folder of skills the agent reads: `~/.claude/skills` for Claude Code, `~/.agents/skills` for the agents that share it ([agentskills.io](https://agentskills.io)). Remove takes it out, from `~/.agents/skills` once no agent that reads it has Pinky. A `pinky` skill that is not Pinky's (its front matter names another) stays as it is.
- An agent is Connected when its entry starts this Pinky (or `pinkyctl`, when that is on the `PATH`) and its skill is this Pinky's. Settings marks an entry that starts a Pinky that is no longer there (the app moved), `pinky` (coreutils' command on many computers, below), another copy of Pinky or something else under the name `pinky`, and an old skill; Update sets it right. The sidebar's card offers Fix for the first two, which leave the agent without Pinky.
- Settings reads the agents again every 10 s while it shows them, and the card every 30 s, so an agent installed meanwhile shows up.

| Agent | Found by | Settings | Changed with | Skills |
| --- | --- | --- | --- | --- |
| Claude Code | `claude`, or `~/.claude.json` | `~/.claude.json` (or `$CLAUDE_CONFIG_DIR/.claude.json`): `mcpServers`, user scope | `claude mcp add` and `remove`, else the file | `~/.claude/skills` |
| Claude Desktop | Claude.app, or its folder | Pinky's extension (below); an entry in `claude_desktop_config.json` (`mcpServers`, in `~/Library/Application Support/Claude`, `%APPDATA%\Claude` or MSIX's folder, `~/.config/Claude`) is set right there | opens the extension in Claude Desktop, which asks to install it | — |
| Codex (CLI, IDE extension, ChatGPT app) | `codex` (also inside ChatGPT.app), or `~/.codex` | `~/.codex/config.toml` (or `$CODEX_HOME`): `[mcp_servers.pinky]` | `codex mcp add` and `remove`, else the file | `~/.agents/skills` |
| Cursor | Cursor.app, or `~/.cursor/mcp.json` | `~/.cursor/mcp.json`: `mcpServers` | the file | `~/.agents/skills` |
| VS Code | the app, or its User folder | Copilot's global `~/.copilot/mcp-config.json` (or `$COPILOT_HOME`): `mcpServers`, `"type": "local"`; an entry in VS Code's `User/mcp.json` (`servers`) is set right there | the file | `~/.agents/skills` |
| Copilot CLI | `copilot` | the same as VS Code | `copilot mcp add` and `remove`, else the file | `~/.agents/skills` |
| Windsurf, now Devin Desktop | the app, `~/.config/devin`, `devin` or `~/.codeium/windsurf` | Devin's `~/.config/devin/mcp_config.json` (`%APPDATA%\devin`), or Windsurf's `~/.codeium/windsurf/mcp_config.json`: `mcpServers` | the file | `~/.agents/skills`; Windsurf's own `~/.codeium/windsurf/skills` |
| Gemini CLI | `gemini` (`~/.gemini` is Antigravity's too) | `~/.gemini/settings.json` (or `$GEMINI_CLI_HOME/.gemini`): `mcpServers` | `gemini mcp add` and `remove`, else the file | `~/.agents/skills` |

Claude Code, Codex, Copilot CLI and Gemini CLI read their MCP servers when a session starts, so Settings says to start a new session; Cursor, Windsurf and Claude Desktop's settings file when they open (quit and open them again). VS Code starts a new server at the next message.

Claude Desktop keeps its settings in memory while it runs and writes them back whole, so a change to `claude_desktop_config.json` made while it runs may be lost. Connect therefore installs Pinky as an extension: it writes `Pinky.mcpb` into `<data>/agents/` (the extension below, made to start this Pinky) and opens it in Claude Desktop (`open -b com.anthropic.claudefordesktop` on macOS), which asks the person to install it. Settings then finds it among Claude Desktop's extensions (`Claude Extensions/*/manifest.json` named `pinky`) and shows it connected, or turned off; only Claude Desktop removes it, in Settings › Extensions. An entry that the person added to `claude_desktop_config.json` comes first in Claude Desktop, so Pinky changes that one instead, and says to quit and open Claude Desktop again.

`PINKY_AGENTS_PATH`, for tests of the app, is the whole search path for agents' commands, and leaves out the login shell, the system's application folders and every command outside that path.

Not yet: Antigravity (`~/.gemini/config/mcp_config.json`), opencode, Zed, Kiro, Cline, Factory Droid and Qwen Code; add them by hand. How it is built and what was tried: [notes/agents.md](notes/agents.md).

### Claude Desktop extension

Each release has `pinky-<version>.mcpb`, an [MCP bundle](https://github.com/modelcontextprotocol/mcpb): opened with a double-click, or dragged into Claude Desktop's Settings › Extensions, it installs as the extension Pinky, with no settings file to change. The bundle does not carry Pinky: it starts the Pinky installed on the computer, `/Applications/Pinky.app` or `~/Applications/Pinky.app` on a Mac (then the cask's `pinkyctl`), `%LOCALAPPDATA%\Programs\Pinky\Pinky.exe` on Windows, so install Pinky first. Its setting Pinky takes another path to Pinky's program on a Mac. `go run ./scripts/release mcpb -version <version> -out <file>` makes it; the release workflow adds it to every release. The one Settings › Agents opens names this Pinky's path instead.

### By hand

Settings › Agents › Add an agent by hand shows both commands below for the copy of Pinky that is open, by its own path, with buttons that copy them.

Claude Code:

```sh
claude mcp add --scope user pinky -- pinkyctl mcp
mkdir -p ~/.claude/skills/pinky && cp docs/SKILL.md ~/.claude/skills/pinky/SKILL.md
```

`--scope user` makes Pinky available in every folder; without it, Claude Code adds it to the folder it runs in only.

`pinkyctl` is the command that Homebrew's cask (`brew install --cask diygod/tap/pinky`) and the Linux packages put on the `PATH`. Without them, or for agents started from the Dock or the Start menu, which may not see the terminal's `PATH`, give the app's own executable instead: `/Applications/Pinky.app/Contents/MacOS/Pinky` on a Mac, `Pinky.exe` in `%LOCALAPPDATA%\Programs\Pinky` on Windows, `/opt/pinky/pinky` (the .deb) or `~/.local/pinky.app/pinky` (the .tar.gz's `install.sh`) on Linux.

The command used to be named `pinky`, which is also a coreutils command (on many Macs through Homebrew's coreutils, and on every Linux): an agent set up with `pinky mcp` that reports "Connection closed" runs coreutils' `pinky` (`pinky --version` says GNU coreutils). Set it up again with `pinkyctl mcp` or the app's path; Settings › Agents marks such an agent and sets it right with Update.

Any client that takes a JSON config:

```json
{ "mcpServers": { "pinky": { "command": "pinkyctl", "args": ["mcp"] } } }
```

To try Pinky without a phone, use `pinkyctl mcp --fake` (it starts `pinkyctl daemon --fake` when no gateway runs) or run `pinkyctl daemon --fake` yourself first.

### Over HTTP

For agents on another machine, in a container, or clients that speak only HTTP, Pinky serves the same tools over Streamable HTTP. It is off by default: turn on Settings › Remote agents in the app, or run `pinky daemon --http 127.0.0.1:7465`. The endpoint is `http://<address>/mcp`, on this computer only (`127.0.0.1:7465`) unless the person picks another address. Every request needs the agent token as a bearer token; `pinky token` prints it.

Claude Code:

```sh
claude mcp add --transport http pinky http://127.0.0.1:7465/mcp --header "Authorization: Bearer $(pinkyctl token)"
```

A JSON config:

```json
{ "mcpServers": { "pinky": { "type": "http", "url": "http://127.0.0.1:7465/mcp", "headers": { "Authorization": "Bearer <token>" } } } }
```

- Without the token, or with another one, requests get `401`; only upload links (below) need none. Browser pages from other origins get `403`, and so do requests to `127.0.0.1` that name another host (DNS rebinding).
- A remote agent's files are on its own machine, out of reach of the folder `phone_files` copies to the phone from. It uploads them into that folder instead (`phone_files { action: "upload" }`, [below](#uploading-a-file)): in the call, or to a one-time link at `http://<address>/upload/<token>`, which takes the file without the agent token.
- Each MCP session over HTTP is one Pinky session, with the same first-connection approval and scopes as over stdio. The agent's name gets " (remote)" after it ("Claude Code (remote)"), so a remote agent is asked about on its own first connection and never gets the scope the person gave the agent of that name on this computer. A newer session of the same remote agent continues the phone its older sessions left ([below](#what-http-clients-may-do)). Turning Remote agents off ends every session at once, with its leases and open approvals.

#### What HTTP clients may do

Pinky is a stateful Streamable HTTP server: `initialize` returns an `Mcp-Session-Id`, and the requests after it carry that id. Clients that handle sessions loosely work too:

- **A new session for every call.** Some clients open a session for each tool call (initialize, then one `tools/call`) and never send an id twice, or start a new session for every turn of a conversation. Over HTTP, a newer session of the same agent (the same `clientInfo` name, so the same "(remote)" name in Pinky) continues the phone that its older sessions hold: the task goes on, with its open approvals (repeat the call with `approval_id` from the new session) and what the person allowed for it. A call that names the phone takes it over, and so does a call without `device` when the agent's older sessions hold exactly one phone; `phone_connect` with the same task name continues the task, another name starts a new one, and `phone_release` releases what the agent's older sessions held.
- **Isolation.** Only a newer session of the same remote agent takes a phone over, and only while the older session has no call running on it (else `lease_held`, "still busy with a call"). It never goes back: of two sessions of one agent that both want a phone, the newer one gets it, and the older one gets `lease_held`. Other agents, and agents on this computer, never take a phone this way; all of them need the agent token.
- **Closing sessions.** `DELETE`, or an hour without requests, ends a session. Its phone then waits 2 minutes for the agent's next session before its task ends as disconnected; `phone_release` ends it at once.
- **Stop.** When the person stops such an agent's task, the agent's later sessions may not use that phone for 10 minutes (any phone, after Stop all), so a client that reconnects for every call cannot simply go on.
- **No session id at all.** A client that cannot keep the `Mcp-Session-Id` (Grok Bot's connector sends fixed headers only, GitHub DIYgod/pinky#10) may send every request after `initialize` without it. Pinky then keeps a session of its own for that client, told apart by its `User-Agent`, starts it with what the client's own `initialize` asked (so the person sees and approves it by its name, "Grok (remote)"), and sends those requests into it: the phone, the task and its approvals carry from call to call, also when the client initializes again. Like any session it ends after an hour without requests; the next one continues the phone as above. A client that never initializes gets one too, as "Agent (remote)". Requests that carry an id always go to that session, so a client that keeps its id never shares a session with one that does not; two clients without ids and with the same `User-Agent` are one client to Pinky.
- **A `GET` without a session id** gets `405`: Pinky offers no stream outside a session, so carry on with `POST`.
- **A `GET` stream.** A client may keep its session's `GET` stream open as long as it likes; calls (`POST`) are served alongside it. Pinky sends little on it, and a proxy that cuts idle streams only makes the client open it again.
- **Protocol versions** 2024-11-05 to 2025-11-25. The sessionless 2026-07-28 protocol (per-request `_meta`) is not served yet.
- **Tunnels and reverse proxies** on this computer (Cloudflare Tunnel, `tailscale serve`, nginx) must send a `Host` of `127.0.0.1:<port>` or `localhost:<port>` (cloudflared: `originRequest.httpHostHeader`), as requests to `127.0.0.1` that name another host get `403`. Upload links use the `Host` the request had, or the proxy's `X-Forwarded-Host` and `X-Forwarded-Proto`; the tool's answer says to swap in the public address if the link shows Pinky's own.

- Protocol: the official Go SDK (`github.com/modelcontextprotocol/go-sdk` v1.8.0), newline-delimited JSON-RPC over stdio; the SDK negotiates the protocol version.
- Server info: name `pinky`, title `Pinky`; the server sends short usage instructions at initialization.
- Identity: the agent's name in Pinky comes from the client's `clientInfo` (`claude-code` shows as "Claude Code"). The shim presents the agent token from `agent-token` in the config directory (mode 0600).
- Sessions: one MCP session is one Pinky session. Closing it (the agent exits, or closes stdin) ends its leases and withdraws its open approval panels without acting.

## Results

Every tool returns one text block (plus a JPEG image block for `phone_observe { screenshot: true }`). Results are text for models, not JSON:

```text
<status> · <device name> · approval_id: <id> · rule: <rule> · settle: timeout
note: "<the person's note>"
<one or two sentences>
<the screen, or the lines that changed>
```

- The first line is the status, or `error: <code>` for errors (then the MCP result also has `isError: true`). Details follow as `key: value`, separated by ` · `: `approval_id`, `rule`, `decision`, `settle`, `matched`, `holder`, `setup`, and `no change` when the screen did not change.
- The screen is the format in DESIGN.md: a header line, then events (`toast:`, `notification:`, `dialog:`), then one element per line `[ref] role "label" @x,y state`, then `ocr` lines.
- `ocr` lines are text read from the screen's pixels that no element shows; they have no ref and are tapped by `x,y`. iPhones add them on every read that shows the agent the screen. Android phones add them where the element tree is empty or sparse over a web view or a canvas (mini-programs and other pages in super-apps' own web views, games, maps), and only there; `phone_observe { ocr: true }` asks for all the text no element shows. OCR uses Vision on macOS and Windows.Media.Ocr on Windows; a computer without it (Linux) marks those Android areas with the state `no_ocr` instead. The picture OCR reads stays in memory and is dropped once read: none is taken of a locked phone, nor of a blocked app the person has not allowed for the task.
- After an action, only the changes are sent: `+` added, `-` removed, `~` changed (including `~ screen …` for a changed header). A new app, size, orientation or lock state, or a diff longer than the screen, sends the whole screen.
- `phone_connect`, `phone_observe` and `phone_handoff` always send the whole screen.
- A blocked app shows as `app: blocked app` with no elements; its toasts and dialogs are dropped, and no screenshot is returned. An app the person allowed for the task (see `phone_launch`) shows as usual, `ocr` lines included, but still without screenshots: OCR reads it from a picture the driver keeps in memory and drops.

### Statuses

| Status | Meaning |
| --- | --- |
| `settled` | The call ran and the screen stopped changing, or settling hit its 3 s limit (`settle: timeout`) |
| `uncertain` | The action may have happened (the driver failed mid-action, the phone dropped). Observe before anything else |
| `blocked_by_policy` | A rule denied the action; `rule` names it (`credentials` for password and code fields, `Read-only`, `Finance apps`, …) |
| `approval_denied` | The person said no, or nobody answered in time; `note` may say why |
| `approval_pending` | The call blocked for 45 s; repeat it with `approval_id` |
| `stale` | The screen changed while the approval waited, so nothing was done |
| `user_took_over` | The person took over; returned at once with the current screen. Later calls return `paused` until they hand back |
| `paused` | The person is in control. Wait with `phone_handoff` |
| `stopped` | The person pressed Stop; the lease is gone, and this session may not use that phone again |
| `device_locked` | The phone is locked. Call `phone_handoff` |
| `device_needs_setup` | The phone is connected, but Pinky cannot use it until the person does a step on it or on the computer, and nothing was done. `setup` names the step and the message says what to do: on an iPhone `trust`, `developer_mode`, `unlock`, `passcode` (iOS asks for it before the WebDriverAgent helper may control the phone), `no_disk_image`, `disk_image`, `install_helper`, `connection` (the connection chosen in Settings › Phones does not work with the phone: the person chooses another), `unlock` (also an iPhone or iPad locked on Wi-Fi, which Pinky opens only once it is unlocked), `device_service` (the Mac's device service lost the phone on Wi-Fi: the person clicks Restart device service on the phone's page in Pinky); on Android `authorize` (allow USB debugging), `usb_permission`, `usb_driver`; `preparing` (Pinky prepares the phone: wait a few seconds, then observe) and `update` (the phone's iOS is too old: end the task) are no steps to hand off. Otherwise call `phone_handoff` asking for the step: it ends by itself once the phone is ready, with the whole screen. After `usb_driver` the phone comes back under another id: call `phone_connect` again. The lease waits with the phone instead of ending after 60 s |

### Errors

| Error | Meaning |
| --- | --- |
| `device_offline` | The phone is not connected. A lease survives 60 s of disconnection |
| `lease_held` | Another session holds the phone; `holder` names its agent and task |
| `scope_denied` | The app is outside the agent's scope or the task's app list |
| `not_found` | The ref is gone, or no installed app matches |
| `unsupported` | The phone or backend cannot do this, such as `recents` on iPhone, or `phone_files` on iPhone |
| `invalid_argument` | Bad or missing arguments, a misused `approval_id`, or no phone connected in this session |
| `internal` | The call was cancelled, or the gateway failed |

## Common arguments

Every action tool (`phone_tap`, `phone_type`, `phone_swipe`, `phone_key`, `phone_launch`) takes:

| Argument | Type | Meaning |
| --- | --- | --- |
| `device` | string | The phone, by id, name or a unique id prefix. Required when the session holds more than one phone |
| `intent` | string | One sentence on why, shown on the approval panel and in history |
| `approval_id` | string | From `approval_pending`: repeat the same call with it to keep waiting |

`phone_observe`, `phone_apps`, `phone_wait_for` and `phone_release` take `device`; `phone_files`, `phone_request_approval` and `phone_handoff` take `device` and `approval_id` (and `phone_files` takes `intent`). Unknown arguments are rejected.

## Tools

### phone_connect

Leases a phone for a task and returns its first screen.

| Argument | Type | Meaning |
| --- | --- | --- |
| `task` | string, required | One line naming the task, shown to the person |
| `device` | string | Optional when exactly one phone is online, or the session already holds one, or none is online and exactly one is connected but needs setup |
| `apps` | string[] | Optional app allow-list for the task (names or ids). Launching another app returns `scope_denied`; typing into one asks the person. Launchers and system UI are always allowed |
| `read_only` | boolean | Run the task read-only, whatever the agent's scope: anything the person's rules would ask about is refused (`blocked_by_policy`, rule `Read-only`), and typing works only in search fields. Connecting again for the same task never widens it. A new agent's approval card then offers read-only access first |

The first connection of an agent the person has not approved yet returns `approval_pending` (rule `New agent`); repeat with `approval_id`. Another session's phone returns `lease_held`. Calling again with the same task keeps the lease; another task name ends the previous task (outcome `replaced`) and starts a new one. After the person pressed Stop, the session cannot connect to that phone again. While the person is in control of the phone (they took over and have not handed back, whichever task was running), the lease starts paused and the result is `paused`: call `phone_handoff`. A phone that is connected but waits for a step (Trust, Allow USB debugging, Developer Mode, …) is leased all the same and returns `device_needs_setup`, without a screen, so `phone_handoff` can ask the person for the step; other calls return `device_needs_setup` until the phone is ready.

### phone_release

Ends the task and frees the phone.

| Argument | Type | Meaning |
| --- | --- | --- |
| `outcome` | `done` \| `failed` \| `partial` \| `cancelled` | Default `done` |
| `summary` | string | For the person's history |
| `device` | string | Default: every phone the session holds |

### phone_observe

Returns the whole current screen.

| Argument | Type | Meaning |
| --- | --- | --- |
| `wait_ms` | 0–30000 | Wait until the screen differs from what the agent last saw (then let it settle), or until this passes |
| `screenshot` | boolean | Add a JPEG screenshot, in the screen's pixel space. None while the phone is locked or a blocked app is in front |
| `ocr` | boolean | Add all the text the pixels show that no element does, as `ocr` lines, however complete the list looks (Android adds such lines by itself only where the list is empty or sparse). On a computer without OCR, the result says so |

Works while the person is in control and while an approval is open.

### phone_tap

| Argument | Type | Meaning |
| --- | --- | --- |
| `ref` | integer | The element to tap |
| `x`, `y` | integers | A point instead of a ref (both or neither) |
| `long_press` | boolean | Press and hold |

The policy checks every element under the finger, not only the smallest: tapping a price label inside a Pay button counts as tapping Pay.

### phone_type

| Argument | Type | Meaning |
| --- | --- | --- |
| `text` | string, ≤ 16384 chars | The text; may be empty with `submit` |
| `ref` | integer | The field (tapped first to focus it); default the focused field. Must be a text field |
| `append` | boolean | Keep the field's text |
| `submit` | boolean | Press Enter afterwards |

Typing into password and one-time-code fields is always refused (`blocked_by_policy`, rule `credentials`). So is typing without `ref` when no field reports focus and the screen has such a field.

### phone_swipe

| Argument | Type | Meaning |
| --- | --- | --- |
| `direction` | `up` \| `down` \| `left` \| `right` | Where the finger moves; `up` shows the content below |
| `ref` | integer | Swipe inside this element (with `direction`) |
| `from`, `to` | `{x, y}` | Exact points instead of a direction |
| `duration_ms` | 80–1200 | Default 300 |

### phone_key

| Argument | Type | Meaning |
| --- | --- | --- |
| `key` | `back` `home` `recents` `enter` `tab` `delete` `forward_delete` `escape` `dpad_up` `dpad_down` `dpad_left` `dpad_right` `page_up` `page_down` | Keys the phone lacks return `unsupported` |

`home` always works, even in a blocked app, except on an iPhone below iOS 27 without the WebDriverAgent helper (see Limits).

### phone_launch

| Argument | Type | Meaning |
| --- | --- | --- |
| `app` | string, required | Package name, bundle id, or name (the app's name, its home screen name or a well-known one; part of a name is not enough); on an iPhone without the WebDriverAgent helper also an `http` or `https` link, which opens in Safari |

Opening a blocked banking or wallet app, or one the person blocked, asks the person like an Ask action (rule `Finance apps` or `Blocked by you`; up to 45 s, then `approval_pending`). If they allow it, it stays allowed for the rest of the task: the agent may open it and read its screen, and everything else the rules ask about there still asks; Pinky keeps no screenshots, recording or screen text of it (its `ocr` lines come from a picture kept in memory only, and no screenshot is returned), and Settings do not change. If they deny it, later launches of it in the task return `blocked_by_policy` at once. Password managers return `blocked_by_policy` without asking. Apps outside the agent's scope or the task's list return `scope_denied`; unknown apps return `not_found`. A name finds an app only by its id, its name, its home screen name or a well-known name (微信 for WeChat): a name that is only part of other apps' names (微信 in 微信读书) finds nothing, and `not_found` lists those apps as similar. The task's `apps` list in `phone_connect` takes names the same way, and its message lists similar apps for a name it could not place. A URL (`https://…`) counts as opening a link (rule `Installs and links`), which asks the person when they chose to approve more than payments; the app that opens the link (Safari on an iPhone) is judged too, so blocking it or leaving it out of the agent's scope refuses its links. Other phones return `not_found` for a link, and an iPhone returns `unsupported` for a link that is not `http` or `https`.

### phone_apps

| Argument | Type | Meaning |
| --- | --- | --- |
| `query` | string | Only apps whose name or id contains it |

Lists installed apps inside the agent's scope, user apps first, one per line: `Name (id)`, with `· system` for system apps. An iPhone app whose home screen shows another name than its own (`WeChat · 微信 (com.tencent.xin)`) lists both. Blocked apps are never listed.

### phone_batch

| Argument | Type | Meaning |
| --- | --- | --- |
| `steps` | array, ≤ 50 | Each step has `tool` (`tap`, `type`, `swipe`, `key`, `launch`, with or without the `phone_` prefix) and that tool's arguments, plus its own `intent` and `approval_id` |
| `intent` | string | For steps without their own |

Steps run in order, each settling first, and the batch stops at the first step that does not settle. The result's first line is the last step's status; then `step N:` blocks follow with each step's status and changes. To finish a batch that stopped at `approval_pending`, repeat that step alone as its own tool call with the `approval_id`.

### phone_wait_for

| Argument | Type | Meaning |
| --- | --- | --- |
| `text` | string, required | Matched without case in element labels and new events. In languages that put spaces between words it matches whole words only (`Stop` does not match `Stopwatch`); Chinese, Japanese, Korean and Thai text matches anywhere in a label |
| `gone` | boolean | Wait for it to disappear |
| `timeout_ms` | 0–45000 | Default 10000 |

Returns `settled` with `matched: true` or `matched: false` and the screen changes.

### phone_files

Copies files between the phone's Pinky folder (`/sdcard/Pinky` on Android, the only place on the phone agents may reach) and a folder on the computer, `<data>/files/<phone id>/`, which the result names. Agents read and write that folder with their own file tools; agents that cannot (on another computer, over HTTP) upload files into it. Needs a `phone_connect` lease like the other tools. iPhones return `unsupported`.

| Argument | Type | Meaning |
| --- | --- | --- |
| `action` | `list` \| `pull` \| `push` \| `upload` | `list` (default) shows both sides; `pull` copies from the phone; `push` copies to the phone; `upload` saves a file the agent sends in the computer's folder. With `content` and no `action`, `upload` |
| `paths` | string[] | Files or folders, relative to the Pinky folder. Default: everything; for `push`, the files that are new or changed on the computer. For `upload`: the one file's name, such as `photo.jpg` or `Pictures/photo.jpg` |
| `content` | string | For `upload`: the file's bytes in base64 (standard or URL alphabet, padding and line breaks optional, or a `data:…;base64,` URL), up to 10 MB |
| `replace` | boolean | For `upload`: replace a different file of the same name |
| `intent` | string | For `push`: one sentence on why, shown on the approval panel |
| `approval_id` | string | From `approval_pending`: repeat the same call with it |

The result lists the folder, then one line per file: `path · size · modified · where`, where `where` is `both` (same size and time), `phone`, `computer` or `differs`. `pull` and `push` list the files they copied and skip files that are the same on both sides; copied files keep their modification time.

- `push` asks the person first (rule `Files`), like an Ask action: up to 45 s, then `approval_pending`. "Allow for this task" covers later pushes in the task. Read-only mode refuses it (`blocked_by_policy`, rule `Read-only`), and read-only and watch-only agents get `scope_denied`: they may only list and pull.
- Paths that leave the folder (`..`, absolute paths) are `invalid_argument`; files that are not there are `not_found`. Symbolic links in the computer's folder are not copied, and a pull replaces a link in its way instead of writing through it.
- Each call is a step in the task's history ("Copied 2 files from the phone: a.txt, b.png").
- Files pushed to Android show in the phone's gallery and photo pickers: Pinky asks the media scanner for them on Android 10 and earlier, and later versions find them by themselves.

#### Uploading a file

`upload` writes one file into the computer's folder and nothing to the phone, so it asks nothing; the `push` after it asks the person as usual. It is the first half of a push and follows its rules: read-only and watch-only agents get `scope_denied`, and read-only mode `blocked_by_policy` (rule `Read-only`).

- **Names** are paths inside the folder; missing folders are made. `invalid_argument` for `..`, absolute paths, names that start with a dot, control characters, `< > : " | ? *`, names that end in a space or a dot, Windows device names (`CON`, `NUL`, `COM1`, `LPT1`…, with any extension), names over 255 bytes and paths over 1000 bytes: the file is made on this computer, which may run Windows, and then on the phone.
- **A name already taken:** the same bytes settle without change; a different file is `invalid_argument` unless `replace: true`. A symbolic link in the way is replaced, never written through, and a folder on the way that is a link is refused.
- **`content`** carries up to 10 MB, checked before anything is decoded or written. Over HTTP Pinky takes requests of up to 16 MiB for it, as over stdio (the SDK's own HTTP default is 4 MiB). A model has to write every byte of it out, so it suits small files and agents that call tools from code.
- **Without `content`, over HTTP,** Pinky checks the upload (scope, name, a taken name) and returns a link instead: `http://<address>/upload/<token>`, at the address the agent reached Pinky at (or the `X-Forwarded-Proto` and `X-Forwarded-Host` of a proxy in front of it). The agent sends the file as the request body with `PUT` or `POST`, from its own shell: `curl --upload-file photo.jpg '<link>'`. A link works once, for 10 minutes, for files up to 200 MB, and only for its session, phone, name and `replace`; it ends with the session. It needs no agent token: its own random 128-bit token is the key. Forms (`multipart/form-data`) get `415`, larger files `413` (or `400` once past the limit when no length was sent), and unknown, used or expired links `404`. The answer is the text the tool would return, with `200`, or `400`, `403`, `503` or `500`.
- **Without `content`, over stdio,** the call is `invalid_argument` and names the folder: an agent on this computer writes the file there itself.
- Each upload is a step in the task's history: "Received “photo.jpg” (1.2 MB) from the agent".

### phone_request_approval

| Argument | Type | Meaning |
| --- | --- | --- |
| `summary` | string, required | The question as the person reads it |
| `details` | string | More context |

Returns `settled` with `decision: allow`, or `approval_denied`, with the person's `note`. It grants nothing: later steps the person's rules ask about, such as payments, still ask, and so does your next question. Like action approvals it blocks up to 45 s and holds the phone while open.

### phone_handoff

| Argument | Type | Meaning |
| --- | --- | --- |
| `reason` | string, required | What the person should do |

Shows a Needs you card and waits until the person hands control back; returns `settled` with their `note` and the whole screen. If they already handed back before the call, it returns at once. A handoff opened while the phone waits for a step (`device_needs_setup`) ends by itself once Pinky has opened the phone, with `note: "The phone is ready."`; if the person hands back before that, the result is `device_needs_setup` again, with their note. A handoff opened while the phone is locked ends by itself when the person unlocks from the computer, if unlocking is all its `reason` asks for ("Please unlock the phone"); one that asks for more ("Unlock the phone, then sign in to Wise") stays open until the person hands back, and so does any handoff after an unlock on the phone itself. The person may decline (`approval_denied`); with no answer it ends after 15 minutes. Blocks up to 45 s at a time, then `approval_pending`.

## Approvals

- Which actions ask is the person's choice in Settings › Safety: payments only (the default); payments, messages, deletions and other commits, calls, installs, links that leave the app and settings changes; or every action. Their own rules may ask about more, and blocked apps and credential fields are refused whatever they chose.
- An Ask action opens an approval panel in the client and the call blocks for up to 45 s, sending progress notifications if the request carried a `progressToken` (at the start, then every 5 s, with the time left to answer).
- The person has 60 s from when the panel opens (configurable with `pinky daemon --approval-timeout`). No answer means Deny (`note: "No answer in 60 s."`).
- After 45 s the call returns `approval_pending` with the id. The panel stays open. A decision made while no call waits is kept for the next call with that id, for as long as the timeout again; later the id is gone and a call with it asks anew.
- The id is single-use and bound to the session, device, lease, tool and arguments (`intent` excluded). Another call with it returns `invalid_argument`.
- While an approval is open on a phone, other actions on it return `approval_pending` with that approval's id; reads still work.
- Before acting on Allow, Pinky reads the screen again and compares the target's role, label, states and position (a few pixels of tolerance) and the screen's text and states (status bar excluded; OCR text only when the screen has no element text). Any change returns `stale`.
- "Allow for this task" covers the rules that asked, in that app, for the rest of the task. An action that another rule also asks about still asks. `phone_request_approval` has no such answer: each question asks the person.
- "Always allow" keeps the answer in the rules file: in that app, the rule that asked, and the rules that asked with it, no longer ask about the steps that rule asks about, in later tasks too, until the person takes it back in Settings › Safety. The approved call's message says `from now on "Payments" no longer asks in Dinely`; a later such action returns `settled` at once, its message starting `No approval needed: the person always allows "Payments" in Dinely.` It never lifts a deny, read-only mode, the agent's scope or the task's `apps`, and it does not apply while every action asks. The panel offers it for actions only: not for `phone_request_approval`, `phone_files` or `phone_launch`.
- An MCP cancellation of the blocked call, a closed session, Stop and Take over all withdraw the panel without acting.
- Only the client's `Decide` answers approvals. No tool can.

## Progress notifications

Sent only when the request carries `_meta.progressToken`: while waiting for an approval or handoff (at the start and every 5 s), for `phone_observe` with `wait_ms`, and for `phone_wait_for` (every 5 s). `progress` and `total` are seconds.

## Limits

- iPhones below iOS 27 without the WebDriverAgent helper (iOS 17.4 to 26, nothing installed) are read through the accessibility inspector and OCR and can launch apps, nothing more: `phone_tap`, `phone_swipe`, `phone_type` and `phone_key` return `unsupported`, with a sentence naming the helper. With the helper, everything works; reading the screen takes 0.5 to 3 s, and about 7 s on a home screen.

| Limit | Value |
| --- | --- |
| Longest block of one call | 45 s |
| Approval timeout | 60 s |
| Handoff timeout | 15 min |
| Settle limit | 3 s |
| Idle lease | 10 min |
| Reconnect grace | 60 s |
| `text` | 16384 characters |
| `phone_batch` steps | 50 |
