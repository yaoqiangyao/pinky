# Field test on a real iPhone (2026-11-07)

Pinky 2026.1107.1 as people get it (`brew install --cask diygod/tap/pinky`, notarized `Pinky.app` in `/Applications`), on the owner's iPhone 13 mini (iOS 27.0.1, English UK system language, USB), with the owner at the Mac approving and passing Face ID. Agents: `scripts/mcpagent` (added on this branch), which keeps one stdio MCP session to `Pinky mcp` the way Claude Code does and takes calls one at a time, and a real Claude Code 2.1 (`claude -p`, Pinky's SKILL.md as a project skill) given a three-app task in Chinese.

What was done: a read-only bookkeeping pass through seven banking, card and crypto apps (balances and transactions read, nothing changed); approvals (allow, deny, deny with a note, stale); blocked apps and their screens around Face ID; typing in Chinese, emoji and symbols; custom controls (timer and alarm wheels, swipe actions, sheets); a timer alert during a task; two agents on one phone; an agent killed mid-task; quitting Pinky mid-task; unlocking from the computer; a real Claude Code run (Weather → Notes → Clock).

No screenshots or personal data from the phone are kept here. Amounts, names, account and card numbers are left out on purpose.

## Bugs, most severe first

Status: **fixed** on branch `claude/iphone-field-test-9b9223` (commit), **open** with the owning area.

### Critical

1. **Money-moving buttons do not ask (fixed, 3d1c058 on `claude/default-preset-payments`; checked with policy tests on made-up screens, not on the phone).** `policy.Default().Evaluate` for a tap on a button in an ordinary app allows, with no kinds: `Sell`, `Sell all`, `Swap`, `Slide to swap`, `Convert`, `Trade`, `Repay`, `Borrow`, `Stake`, `Unstake`, `Claim`, `Bridge`, and `还款`, `立即还款`, `分期还款`, `去分期`, `立即分期`, `借钱`, `买入`, `卖出`, `赎回`, `申购`, `兑换`, `闪兑`. `Buy`, `Withdraw`, `Confirm swap`, `Place order`, `转账`, `确认支付`, `预约还款` ask. A crypto wallet's final `Swap` or an exchange's `Sell` runs without the person, and so does `还款` (repay a credit card) in a bank app the person has allowed. Reproduce: evaluate a `screen.Element{Role: button, Label: "Swap"}` tap in an app outside the finance category.
   Expected: commits (payments, with a currency on screen). Actual: allow.
   Now: they are commits; those whose words move money (sell, swap, trade, repay, borrow, stake, withdraw, transfer, 还款, 分期, 买入, 卖出, 赎回, 申购, 闪兑, …) are payments without an amount on screen, which the default (payments only) asks about; Convert, Claim, Redeem and 兑换 are payments next to an amount, coins included (`0.5 ETH`). Tabs never commit. `TestMoneyMovingButtonsAskByDefault`, `TestCoinAmounts`.

2. **Most money apps on the phone are not blocked by default (partly fixed, 5b69acc: the table and finance names; the App Store genre is open, iOS driver).** The bundled finance table covers US, EU, Chinese and a few Japanese and Korean banks. Settings › Safety on this phone listed seven blocked apps (1Password, Alipay, Passwords, Wallet, Wise, 招商银行, 掌上生活), while agents could list and open, among others: DBS digibank `com.dbs.sg.dbsmbanking`, DBS PayLah! `com.dbs.dbspaylah`, Trust Bank `sg.trust`, OKX for iOS `com.okex.OKExAppstoreFull` (only Android's `com.okinc.okex.gp` is in the table), ether.fi `fi.ether.cashapp`, Ethena `app.ethenapay.co`, Plasma One `to.plasma.one.application`, Jupiter `ag.jup.jupiter.ios`, Panda Remit `com.panda.exchangerate` and Singpass `sg.ndi.sp` (national digital identity). With bug 1, an agent can trade in an exchange or swap in a wallet on a phone with default settings, and reads every balance. The README promises "Banking, wallet and password-manager apps are invisible to agents".
   Suggestion: on iOS, installation_proxy can return each app's iTunesMetadata genre (Finance, 6015); treat unknown Finance-genre apps as finance. Add Singpass-like identity apps to the password category.
   Now: every app listed above is in the table (DBS as `com.dbs.*`), with others of their kind, and Singpass is in the password category; an unknown app whose name says bank, wallet, securities, remittance or the word pay (银行, 证券, 钱包, …) counts as finance. `TestFieldTestFinanceApps`, by id and name; not re-checked on the phone. The genre needs the iOS driver to report it.

### High

3. **Delete buttons named after their icon do not ask (fixed, 3d1c058; policy tests only).** On an English (UK) iPhone, iOS's swipe-to-delete and trash icons are labelled `bin` (`trash` in US English). `bin`, `Bin`, `trash`, `Trash`, `Remove`, `移除` and `Move to Bin` are allowed; `Delete`, `Erase`, `删除` ask. On the phone: Clock › Alarms, swipe a test alarm left, tap `bin`: the alarm was gone in 2 s with no approval card.

4. **Taps on text that only OCR sees are not checked (policy half fixed, 3d1c058: OCR text is judged by the line and its pieces, `TestOCRText`; gateway half open: `Gateway.evaluate` and `hitTest` still skip OCR elements).** `Gateway.evaluate` and `hitTest` skip `SourceOCR` elements. 掌上生活's bill pages are web views with no accessibility elements: `还款`, `分期还款` and `去分期` exist only as OCR lines, so a coordinate tap on them is evaluated with no target and allowed. Suggestion: evaluate OCR elements under the finger too (with a margin around the text box), at least for commit and payment words.

5. **Banking apps refuse to run while Pinky streams the iPhone (open, iOS driver; docs).** DBS digibank: "You may be at risk of a scam … anti-malware tool detected active screen-sharing … digibank access is temporarily blocked". 招商银行: "检测到您正在共享/录制屏幕 … 请您关闭上述功能后继续使用招商银行APP". 掌上生活 only warns ("录屏/投屏中"). The iOS stream lives for the whole device session, so these apps stop working for the person too whenever Pinky runs with the phone plugged in, not only during tasks. Needs a sentence in the README and, if HID allows, a capture-free mode (accessibility tree only) or stopping the stream while no one watches.

6. **The first non-ASCII `phone_type` after the pasteboard connection idles fails as "unsupported" (open, iOS driver).** Every time, the first Chinese text failed with `error: unsupported … without the pasteboard service only printable ASCII can be typed on iPhone`; the next call with the same text worked. Real Claude Code concluded "Pinky can only type English on this iPhone" and handed the typing to the person (a handoff card asking them to type the note by hand), then, told otherwise, wrongly decided that `append: true` is what makes Chinese work. Expected: reconnect the pasteboard service and retry once; never report a transient failure as `unsupported`.
   Typing itself is good once it works: Chinese, English, emoji with ZWJ sequences, tabs, quotes and `『』` came out exactly, and the Mac's pasteboard change count stayed the same.

7. **On iPhone, notifications and alerts never become events (open, iOS driver + gateway).** The iOS driver emits no `notification:`, `dialog:` or `toast:` events. A timer that went off during a task showed only as two OCR fragments (`X`, the label) while it rang; the agent had no sign that the phone wanted attention. Since the hiding of blocked apps' notifications works on notification events, it does not apply on iPhone: a blocked app's banner reaches the agent as OCR text in whatever app is in front (from the code). A banner with a one-time code was the same until 2026-11-08; its code is now masked like any code on the screen (`docs/notes/gateway.md`, "One-time codes").

8. **`pinky` on the PATH is coreutils' on many Macs (fixed, 21d50ec and 8c340b8).** Homebrew's coreutils links `/opt/homebrew/bin/pinky`. `brew install --cask diygod/tap/pinky` prints "It seems there is already a Command Wrapper at '/opt/homebrew/bin/pinky' from formula coreutils; skipping link", and the README's `claude mcp add pinky -- pinky mcp` then starts coreutils' `pinky` (it prints `Login Name TTY Idle When Where` and exits 0): Claude Code reports "Connection closed" and gets no tools. Suggestion: tell people to use `/Applications/Pinky.app/Contents/MacOS/Pinky mcp` when `pinky --version` is not Pinky, or name the wrapper something unique. Fixed: the cask and the Linux packages install `pinkyctl` (the .deb, which linked `/usr/bin/pinky`, did not install at all next to coreutils), and Settings › Agents › Add an agent shows the commands by the app's own path, with Copy buttons.

9. **Unlocking from the computer stops working once the phone locks in a long session (open, iOS driver).** About 90 minutes into the session the owner locked the phone with the side button during a task. The agent's next action returned `device_locked` (after 5 s), and every "Unlock with Touch ID" then failed: `unlock: com.apple.coredevice.deviceinfo: connection closed` (three tries). Agent reads failed too (`accessibility walk: focus: context deadline exceeded`) until the owner unlocked on the phone; then reads worked again. The driver dials `com.apple.coredevice.deviceinfo` once in `start` (`d.info2`) and never again, and both `ScreenOn`/`Wake` and `Unlock` go through it, so once that connection closes, waking and unlocking from the computer fail until Pinky reopens the phone. Expected: dial again when the connection is closed. (Another session was working on Wi-Fi pairing at the time; it held no lock on this phone, but its effect cannot be ruled out.)

10. **Unblocking an app in Settings did nothing for apps blocked by id (fixed, 41998c6).** Settings › Safety kept only display names and unblocked by name, so Wise (`com.transferwise.Transferwise` is in the table) left the list but stayed blocked for agents. Rows now keep the id and unblocking passes id and name.

11. **Excluding an id cannot beat a prefix entry (fixed, 5b69acc: an excluded id or name beats a prefix, so Settings unblocks 掌上生活 alone, `TestUnblockOneAppOfAPrefix`; the list computed at launch and the unshown failure are open, client).** `Apps.Apply` deletes exact ids and names from the table; `com.cmbchina.*` stays, so 招商银行 and 掌上生活 cannot be unblocked from Settings at all, and `SetAppBlocked` then says "blocked by a rule in rules.yaml that names the app", which is not what happened. Workaround used: `exclude: [com.cmbchina.*]` in apps.yaml and a restart (that unblocks every app of the prefix). Also: the blocked list in Settings is computed once at launch, so a phone connected later shows no bundled-blocked apps, and a failed unblock is not shown.

12. **The mirror stayed on "Connecting…" (fixed, 5f515b5).** After about 20 minutes of a session, going to Settings and back to the iPhone left the mirror black with "Connecting…". The shared stream keeps the pictures since the last key frame for late viewers and drops them past 64 MiB; an iPhone sends key frames only when asked, and nothing asked. A viewer that joins with no key frame kept now asks the phone for one. Not yet checked on the device with a build of the branch.

13. **Valid refs refused as "not on the screen anymore" (fixed in the gateway, bf6a3b3 and 3ec406a; root cause open in the iOS driver).** The iOS driver reports the same element under another ancestry, and sometimes another role, from one read to the next, so it gets a new ref: Ethena's nav bar (`Left` 55 ↔ 77, `Filter`, `Export transactions`), Clock's `Done` (420 → 436), `Delete Alarm` (button 445 → text 446), Notes' body field (290 → 514, also hit by real Claude Code). The agent taps a ref from the screen it just received and gets `not_found`. Diffs also carry a `-`/`+` pair for each such unchanged element. The gateway now falls back to the element of the agent's last screen with the same label, id and place (same role preferred, unique required).

### Medium

14. **App identity is lost around Face ID (open, iOS driver).** While a Face ID prompt or its fade-out is on screen the header says `app: Home Screen (com.apple.springboard)` or `app:` with nothing, and OCR shows the app beneath: after Trust's Face ID login the agent got its balances and promotion text under "Home Screen". `Blocked()` is false for an empty or SpringBoard app, so a blocked app in that state would leak. A blocked Ethena opened by the person did not leak in a 1 s polling test (it hides its content during Face ID), so this depends on the app.

15. **`phone_wait_for` never matched OCR text (fixed, d87a101).** It polled without OCR, so on web views it waited the full timeout and returned a screen without the OCR lines. It now also reads with OCR once a second while the tree has no match, and does not call text gone while OCR still sees it.

16. **Task app names in another language are kept silently (partly fixed, 27b3483).** Claude Code, asked in Chinese, connected with `apps: ["天气", "备忘录", "时钟"]`; the phone's apps are named Weather, Notes and Clock, so its first `phone_launch` was `scope_denied` and it released and reconnected. `phone_connect` now names the entries that match no installed app and how to fix the list, and a second `phone_connect` with the same task resolves the new list. A table of common system app names across languages would remove the round trip.

17. **Unlocking from the computer failed once (open, iOS driver).** "unlock: the passcode pad did not appear: the PIN was not entered" while the lock screen showed a notification stack; the second try worked. The swipe up probably opened the notification list. Pinky entered nothing, as it should.

18. **A blocked launch has no "allow this once" (open, product).** The person approved the agent's `phone_request_approval` "please unblock Wise, 招商银行, 掌上生活" expecting that to be enough; it changed nothing. The owner: since they allowed it in Pinky, the agent should be able to go on; a one-time allow that does not change settings. Suggestion: on `blocked_by_policy` for a launch, a Needs you card with "Allow <app> for this task".

19. **Read-only depends on the person noticing the scope picker (open, product).** The bookkeeping agent was approved with the preselected scope, Everything, although the task was read-only; it only read, but nothing held it to that. Agents cannot ask for less (for example `phone_connect { read_only: true }`).

20. **Quitting Pinky ends every agent's MCP session (open, main).** `Pinky mcp` exits when the app quits (it waits only across update restarts), so Claude Code loses the server mid-task and needs a restart of the server or session.

21. **What the person sees can differ from what the agent sees (open, iOS).** Trust draws its passcode keypad in a capture-proof layer: the agent's tree lists the keys and a FaceID button, the mirror and screenshots show blank space. The person watching in Pinky cannot see the keypad the agent is looking at.

22. **Picker wheels read as row indexes (open, iOS driver).** The Add Alarm time wheels come back as `other "5001"`, `other "5000"`, `other "1"` (looping picker rows); the selected values (10, 20, PM) appear only as OCR.

23. **Covered elements look tappable (open, iOS driver/gateway).** Rows behind iOS 27's floating tab bar are listed at their positions; a swipe on such a row switched tabs instead. Marking elements covered by an overlay would help.

24. **`phone_wait_for` matches substrings of labels already there (open, gateway).** Waiting for `Stop` returned in 0.3 s because the tab bar has `Stopwatch`. Word matching for Latin text, or waiting for the text to appear rather than be present, would avoid it.

25. **After `home` from a blocked app, the result says it is still in front (open, gateway).** `phone_key home` returned "blocked app · no change" and the next swipe was refused as if the blocked app were in front; a read a second later showed the home screen. The read after the key came too early.

26. **Unlocking ends a handoff that asked for more (open, gateway).** A handoff opened on a locked phone ends when the person unlocks from the computer, even when the reason asked for something else as well ("unlock, then unblock three apps in Settings"); the agent is told "The phone was unlocked" and goes on.

27. **No bundle id on the first read of a new session (open, iOS driver).** `app: Plasma One` instead of `Plasma One (to.plasma.one.application)`; later reads had it. Blocking by id cannot work on such a read.

28. **The read-only scope is hard to find on the new-agent card (open, client).** The choices read "All apps except blocked ones", "Only the apps I choose", "Browse and read, never commit" and "Watch only"; Settings › Safety calls the same idea "Read-only mode". Asked to choose read-only, the owner picked "Watch only". Both scopes were enforced as documented once chosen.

29. **A tap on a full-screen backdrop by ref hits what lies at its centre (open, gateway).** Notes' context menu has `button "Dismiss context menu"` covering the screen; `phone_tap` on its ref tapped the centre of the screen, which was the menu item "Attachment View", and opened its submenu instead of closing the menu. For an element that other elements cover, a point of it that nothing covers would do what the agent meant.

### Low

30. `depends_on macos: ">= :monterey"` in the cask prints a deprecation warning on every install (Homebrew wants `depends_on macos: :monterey`).
31. History step for a refused launch: "Blocked by "Finance apps": Opened Wise" reads as if it opened.
32. History steps for coordinate taps say "Tapped at 561,2283" rather than the text under the finger.
33. OCR noise on the home screen (single glyphs such as `は`, `甲P` from icons) costs agents tokens.
34. Settings says "Passwords and one-time codes are blurred before an agent sees the screen"; only notification events are masked, on any platform. Codes, ID and card numbers shown in app text reach agents as is. Fixed on 2026-11-08 for one-time codes: they are masked on the screen too, OCR lines and banners included, and covered in screenshots (`docs/notes/gateway.md`, "One-time codes"); the setting's line says so. ID and card numbers still reach agents as is: banking and wallet apps are blocked, but a card number in another app is not hidden.

35. zh-Hans: the history step "Unlock from the computer failed" is not translated, and its detail says only "内部错误" while the toast had the cause.

## What worked

- Approvals: Allow, Deny (`approval_denied`), Deny with a note (Claude Code read the note and tried again), and stale (the person toggled a switch on the phone before Allow: `stale`, nothing deleted). An open approval withdrawn when the agent released the task.
- First connection of a new agent: the card came up and was approved in 13 s; reconnecting with the same name later needed no card.
- Blocked apps: `phone_launch` refused at once; a blocked app the person opened showed as `app: blocked app` after Face ID.
- Unlock from the computer with Touch ID (second try), and the handoff it ended.
- Two agents on one phone: the second got `lease_held` naming the holder; after the first agent's process was killed (`kill -9`), its lease was free within 2 s.
- Typing after the first call: exact text in Chinese, emoji and symbols; the Mac's pasteboard untouched.
- Custom controls: a timer wheel turned by a short swipe, labels typed into sheets, swipe actions revealed.
- Scopes: "Watch only" refused every action (`scope_denied`); after the owner switched the agent to "Browse and read, never commit" in Settings › Agents, the running session got it at once: a Delete was refused (`Read-only`), typing into a note was refused, typing into Notes' search field worked.
- Take over: the agent's next call got `user_took_over`, then `paused`; `phone_handoff` returned the owner's note when they handed back.
- Recordings and export: the owner opened finished tasks in Activity, played their recordings and exported all history to HTML.
- Real Claude Code finished the three-app task in 9.5 minutes (43 turns), despite bugs 6, 13 and 16.

## What the person had to do by hand

- Unblock apps: in Settings (failed for three apps, bugs 10 and 11), then an apps.yaml edit and a restart.
- Pass Face ID in every banking app, each time through a handoff the agent had to ask for; Pinky could notice a Face ID prompt on the phone and tell the person to look at it.
- Retry unlocking from the computer once (bug 17), and unlock on the phone itself after it stopped working (bug 9).
- Nothing for DBS and 招商银行: they cannot be used while Pinky is connected (bug 5).
- Type a note by hand, as Claude Code asked, because of bug 6 (declined here, so the agent tried again).
