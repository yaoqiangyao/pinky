# Changelog

What changed for people in each release of Pinky, newest first. A version is the release's date in Asia/Shanghai and that day's count: 2026.1107.2 is the second release of 7 November 2026. Notes wait under "Unreleased" until the next release takes them; see [docs/RELEASING.md](docs/RELEASING.md).

## Unreleased

- Unlocking an iPhone from the computer now works when the phone doesn't say whether its screen is on, as some iPhones on iOS 27 do.
- Pinky now comes from flyfox's fork, yaoqiangyao/pinky, for Windows and iPhones on iOS 27 or later. It updates from there, and Help › Send Feedback… opens an issue there.
- Pinky no longer sends usage statistics or crash reports.

## 2026.1108.24 (2026-11-08)

- When a phone isn't on your tailnet, Settings › Phones now says whether Tailscale isn't connected on it or is signed in to another account, with Set up Tailscale to open the steps.
- Pinky now says that Android runs one VPN at a time: another VPN or proxy app on the phone turns Tailscale off.
- Feedback diagnostics now include Tailscale's state and the Android devices on your tailnet, without their names.
- When pairing over Wi-Fi fails, Pinky now says why: the phone ended the pairing, nothing at that address was waiting to pair, the phone didn't answer, or the computer's adb is too old. It no longer says the pairing code didn't match when you scanned a QR code.
- A QR pairing that fails now offers Pair with a code instead next to Try again.
- adb's own words for a failed pairing now go to Pinky's log, so feedback diagnostics include them.

## 2026.1108.23 (2026-11-08)

- When Apple won't sign the developer disk image Pinky has for an iPhone (the images of Xcode 26 and earlier no longer work for iOS 27), Add phone and the phone's Checkup now say which image it was and what to do: install or update Xcode from the App Store and open it once. Pinky no longer retries the same image, and tries again as soon as a newer one is there.
- On iPhones, apps are now found by the name their home screen shows: a task for 微信 opens WeChat, not 微信读书. A name that is only part of other apps' names no longer opens one of them; agents are told which apps it may mean.
- iPhones connected through the WebDriverAgent helper: a locked phone no longer reads as an app with an empty screen, and lists such as WeChat's chats no longer repeat each row's texts or show rows hidden under the navigation bar.
- Agents that reach Pinky over HTTP from another computer can now hand it a file for an Android phone: `phone_files` takes an upload, a small file right in the call or a file of up to 200 MB through a one-time link, and pushing it to the phone still asks you first.

## 2026.1108.22 (2026-11-08)

- Settings › Safety now describes Hide codes from agents as it works: one-time codes in notifications are masked before an agent reads them. It never blurred the screen itself.

## 2026.1108.21 (2026-11-08)

- On an iPhone with iOS 27, agents now see the elements of web pages in Safari (links, buttons and fields) rather than only the text read from the picture, and a web page's password field counts as one, so agents can't type into it.
- On an iPhone with iOS 27, agents can open http and https links with `phone_launch`: they open in Safari, and Pinky judges them by Safari's rules too, so blocking Safari or leaving it out of an agent's scope stops its links. The Installs and links rule still asks.
- Deleting old activity now stops if Pinky cannot finish reading which screenshots belonged to those tasks, instead of deleting the tasks and leaving the screenshots behind.

## 2026.1108.20 (2026-11-08)

- Plugging in an iPhone or iPad now turns on its Wi-Fi connection, as Finder's "Show this iPhone when on Wi-Fi" does, so it keeps working over Wi-Fi when you unplug it; you no longer need Finder or Xcode for that.
- Over Wi-Fi, screenshots of an iPhone or iPad for approvals, history and agents now come from the live picture, in a fraction of a second instead of up to half a minute.
- On iPhones, Pinky no longer takes a picture of a blocked app you haven't allowed, not even in memory.
- On Linux, `pinkyctl daemon` no longer says it keeps the computer awake when the system refuses a moment later; it reports the refusal and tries again in 5 minutes.

## 2026.1108.19 (2026-11-08)

- Android phones now read text that apps draw without describing it, as in mini-programs and other pages inside super-apps, games and maps: Pinky reads it from the screen on macOS and Windows, and agents can read and tap it. `phone_observe` takes `ocr: true` to read all the text on the screen.
- A blocked app you allow for a task is read this way too: agents get its text, never a picture of it.
- Taps on Android where the screen says little, such as a "Pay" button that is a picture, are now checked against the text drawn there.
- Pinky now reads text from screen pictures on Windows too.

## 2026.1108.18 (2026-11-08)

- Pinky in Chinese now shows What's new and the update notes in Chinese.

## 2026.1108.17 (2026-11-08)

- Typing on an iPhone no longer adds a space where you didn't type one, as when text is added after a word.

## 2026.1108.16 (2026-11-08)

- `pinkyctl daemon` now keeps the computer awake while an agent task runs and while a phone is connected through Tailscale, as the app does, so it no longer needs `caffeinate`.

## 2026.1108.15 (2026-11-08)

- Watching a phone takes about a third of the CPU it did: the video's frames go to the GPU as the phone sends them, without converting each one first. Recordings play the same way.
- Screenshots saved from a phone's screen are at the phone's full resolution.
- Windows and Linux computers stay awake while an agent task runs, and while a phone is connected through Tailscale with Keep this computer awake for phones away on, as Macs already did.

## 2026.1108.14 (2026-11-08)

- iPhones and iPads now work over Wi-Fi too, on by default: one you use with Pinky keeps working when you unplug it, while it's on the same Wi-Fi as your computer, and a running task moves between the cable and Wi-Fi by itself. Turn it off in Settings › Phones.
- When Pinky finds an iPhone or iPad on Wi-Fi that it hasn't used, such as a family member's iPad paired with your computer, it shows it as Found on Wi-Fi and leaves it alone until you choose Use or Ignore.
- A locked iPhone or iPad on Wi-Fi shows as Locked on Wi-Fi, and Pinky connects once you unlock it; it leaves a sleeping phone alone, also one without a passcode, so its screen doesn't light up. An agent that needs it asks you to unlock it.
- Over Wi-Fi, Pinky reads the text on an iPhone's screen from the live picture, which is much quicker than a screenshot there (about 5 seconds).
- When a Mac's device service loses an iPhone on Wi-Fi, which it can do for up to an hour after the phone's connection broke once, Pinky notices, shows it as Lost on Wi-Fi and offers to restart the service: macOS asks for an administrator's password, and every iPhone and iPad disconnects for a second or two. To notice it, Pinky looks for your iPhones on the local network, so macOS may ask whether Pinky may do that.
- Add phone › iPhone says which steps need the cable: trusting the computer, and making the Developer Mode switch appear; the rest works over Wi-Fi.
- A phone Pinky is connecting to, or moving between the cable and Wi-Fi, shows as Connecting or Moving to Wi-Fi rather than offline.
- iPads are called iPads, with their model names.
- Use your Android phone when it's away from your computer's network, as when you're out and the computer is at home, through Tailscale: turn on Use away from home for the phone in Settings › Phones once while it's near the computer, and Pinky keeps reaching it wherever it is. Add phone › Android has the steps, with buttons to get or open Tailscale and to connect the phones on your tailnet.
- Pinky says what Use away from home opens before turning it on (the phone's adb listens on the network until it restarts) and turns it off with one click, or when you remove the phone.
- New in Settings › Phones: Keep this computer awake for phones away, on by default, so agents can still reach a phone connected through Tailscale.
- Through Tailscale, Pinky streams the phone's screen lighter (1 Mbit/s, 15 frames a second), since the phone's upload carries it.
- Android 10 phones, which have no Wireless debugging, can now leave the cable too: plug the phone in and choose Use Wi-Fi instead… in the ⋯ menu under its picture. Pinky reconnects it by itself when it comes back to the network, and after the phone restarts its page says to plug it in once and choose Use Wi-Fi again; Stop the Wi-Fi connection turns it off.

## 2026.1108.13 (2026-11-08)

- On Windows, a task's screen recording no longer takes in a moment of your own use of the phone from just before the task started or just after it ended.
- An iPhone below iOS 27 set to connect through CoreDevice now says in your language that it needs another connection, with a button to Settings › Phones.

## 2026.1108.12 (2026-11-08)

- An agent that connects to a phone waiting for a step (trusting the computer, allowing USB debugging, Developer Mode) now hears which step, instead of being told the phone is offline, and can ask you for it. Pinky's request card then says the agent goes on by itself once the phone is ready.
- An iPhone waiting for its passcode before the WebDriverAgent helper may control it now says so in your language.
- A phone that never connected fully and was unplugged leaves the phone list after two minutes, such as the extra entry Windows showed for a phone before its USB driver was installed.
- `pinkyctl devices` no longer prints an offline phone's reason twice, and lists phones that need setup as such.
- On phones that ask before installing apps over USB, the Update card for Pinky Companion now says why to update when it is signed with the old public key: anyone could make a fake update of it, and the new version is signed with Pinky's private key.

## 2026.1108.11 (2026-11-08)

- Pinky Companion's accessibility service is now on only while an agent works on the phone: it turns on as a task starts and off a few seconds after the task ends or you take over. Banking apps that refuse to run next to an accessibility service from outside an app store work again the rest of the time.
- Pinky Companion is now signed with Pinky's private key. Builds signed with the old key, which was public, are removed and installed again by themselves when the phone is free, and Pinky tells you; phones that ask before installing over USB wait for you to tap Update.
- The FAQ says which apps can notice Pinky Companion and what to do about it.

## 2026.1108.10 (2026-11-08)

- Pinky now knows when the WebDriverAgent helper on an iPhone stops working: with a free Apple ID its signature lasts 7 days. Two days before, the phone's page and its checkup remind you to install it again from Xcode, with how to do it.
- Once the helper's signature has run out, the iPhone shows as needing setup and says why, instead of failing to start the helper.
- An iPhone set to connect through the helper without it installed now says so in your language, with how to install it.
- Phones waiting for a step before Pinky can use them (allowing USB debugging, trusting the computer, Developer Mode, the developer disk image, a USB driver) now show as needing setup instead of offline, and their page says what to do in your language.
- A phone unplugged while it waited for a step now shows as offline instead of still asking for that step.

## 2026.1108.9 (2026-11-08)

- Pinky now mounts Apple's developer disk image on iPhones with iOS 27 by itself, in a second or two. This corrects 2026.1108.8: Xcode is no longer needed to mount it once.
- Fixed iPhone connections that could treat Pinky as an old host, which broke large transfers on that connection.
- The "You're in control" bar now sits above the phone in Pinky's window instead of over the top of its screen, so buttons there, such as an app's back button or menu, can be clicked from the computer.
- Clicking the phone in Pinky's window to take over now always finishes that tap on the phone (it could stay held down), and what you type right after reaches the phone.
- On a locked phone, the dimming and the "is locked" label step aside while the pointer is on the phone in Pinky's window, so you can unlock it there by hand.

## 2026.1108.8 (2026-11-08)

- A correction to the last release: on iOS 27, Pinky can't yet mount Apple's developer disk image by itself (it waits until it times out). Open Xcode once with the iPhone plugged in; the iPhone keeps the image when it restarts.
- Pinky now notices within a minute when an iPhone's developer disk image goes away while it stays plugged in, and checks again whenever an iPhone is plugged in or restarts, instead of still showing it as connected.
- Add phone › iPhone shows an iPhone that is already connected and ready, instead of looking for one.
- Pinky's log now records each step of preparing an iPhone: mounting the developer disk image (how, how long, and why it failed), the Developer Mode switch, the trust request and the image download.
- When something in Pinky gets in an agent's way, the agent now tells you afterwards and suggests reporting it with Help › Send Feedback…, leaving out what was on your screen. It never posts a report without your yes. Update in Settings › Agents gives your agents the new guide.
- On iPhones below iOS 27, Pinky now notices when the developer disk image is not mounted, instead of failing later when it starts the WebDriverAgent helper.

## 2026.1108.7 (2026-11-08)

- iPhones no longer need Xcode's Devices window: Pinky turns on what the iPhone needs for development by itself, and again after every restart. Add phone › iPhone walks you through the steps (trusting the computer, Developer Mode) and says what to do when one is stuck.
- Pinky uses Xcode's developer disk image when Xcode is installed. Without Xcode, Add phone can download the image, when you click Download, from github.com/doronz88/DeveloperDiskImage, a third-party copy of Apple's files, and checks every file against pinned checksums.
- Each iPhone's checkup now covers trusting the computer, Developer Mode and the developer disk image.
- On Linux, iPhones plugged in after Pinky started are found; before, Pinky had to be restarted. Add phone says when Windows lacks Apple's iPhone driver or Linux lacks usbmuxd.

## 2026.1108.6 (2026-11-08)

- You can now remove a phone from Pinky: Remove this phone… in the phone's ⋯ menu, on its page while it is offline, on its tile in All phones, by right-clicking it in the sidebar, or in Settings › Phones. Its saved PIN and settings go; its history stays unless you choose to delete it too.
- A phone you removed over Wi-Fi stays away until you pair it again or plug it in, even when adb reconnects it by itself. Pinky doesn't unpair the phone from your computer, so other apps that use it keep working.

## 2026.1108.5 (2026-11-08)

- Pinky now carries its own adb on macOS, Windows and Linux, so Android phones work without installing Android's platform tools. It still uses the adb you already run, such as Android Studio's or Homebrew's, also when Pinky is opened from the Finder.
- On Windows, Add phone shows Android phones that lack a USB driver and how to get one; on Linux, it shows the steps that let Pinky reach the phone over USB.
- Add phone › Android walks you through connecting your phone step by step for its brand, such as Xiaomi, OPPO, vivo, HONOR, HUAWEI, Samsung and Pixel, including each brand's extra switches.
- Each phone has a checkup that lists what works and how to fix what doesn't, such as Xiaomi's USB debugging (Security settings) or a phone that needs a restart. When adb itself is the problem, Pinky says so first.
- Pinky finds the agents on your computer, such as Claude Code, Claude Desktop, Codex and Cursor, and connects them with one click, the Pinky skill included. Until one is connected, Pinky offers to do it.
- Each release includes Pinky's MCP bundle for Claude Desktop, which adds Pinky with a double-click.

## 2026.1108.4 (2026-11-08)

- You can now choose how Pinky connects to each iPhone in Settings › Phones: Automatic, CoreDevice (nothing to install, iOS 27 and later) or the WebDriverAgent helper. Through the helper the iPhone shares no screen, so banking apps that refuse screen sharing (DBS digibank and China Merchants Bank in testing) keep working while Pinky is connected.
- Unlocking an iPhone from the computer also works through the WebDriverAgent helper.
- When an iPhone asks for its passcode before the WebDriverAgent helper can start, Pinky says so in Settings and connects once you enter it, instead of starting the helper over and over.

## 2026.1108.3 (2026-11-08)

- On iPhone, alerts and notification banners now reach the agent, and Pinky's code masking and hiding of blocked apps' notifications apply to them.
- On iPhone, Pinky stops the live picture after 30 seconds when nobody is watching, so banking apps that refuse to run while the screen is shared, and the camera and microphone, work for you again.
- On iPhone, the app in front is always named by its bundle id, also right after a session starts and while Face ID is on screen, so blocked apps stay blocked.
- On iPhone, elements keep their numbers between reads, picker wheels read as their values, and rows hidden behind the tab bar are left out.
- On iPhone, the agent is told when part of the screen, such as some apps' passcode keys, is hidden from screenshots and the mirror.
- Unlocking an iPhone from the computer swipes again when notifications cover the lock screen.

## 2026.1108.2 (2026-11-08)

- By default, Pinky now asks only before payments (Settings › Safety › When should agents ask you?). Installs that never changed this setting move to it too; if you changed any safety setting, your choice stays.
- Buttons that move money, such as Sell, Swap, Trade, Repay, Borrow, Stake, Withdraw, Transfer, 买入, 卖出 and 还款, now count as payments and ask, with or without an amount on the screen.
- "Bin" and "Remove" buttons now count as deletions, as "Delete" and "Trash" do.
- More banking, payment and crypto apps are blocked by default, among them DBS, OCBC, UOB, Trust Bank, OKX, Coinbase, Crypto.com and ether.fi; Singpass is blocked with the password managers.
- An app blocked as part of a family of apps, such as all of one bank's apps, can now be unblocked on its own in Settings › Safety.
- Edits to the rules files take effect while Pinky runs, and Settings › Safety shows them.
- Opening a blocked banking or wallet app now asks you instead of failing. If you allow it, the agent may open and read it for the rest of that task; what your rules ask about there still asks, and Pinky keeps no screenshots or recordings of it.
- An agent can start a task read-only, and a new agent that asks for read-only gets that choice first. The scope menu says plainly what each choice allows.

## 2026.1108.1 (2026-11-08)

- Approvals now show the whole phone screen with what the agent wants to tap marked, next to a close-up you can read, so you can tell the app, the page and the button at a glance. Swipes show as an arrow.
- Click an approval's screenshot to see it larger.
- Approval cards in the main window show the screenshot when that phone isn't the one on screen.
- When an approval has no screenshot, it says why, such as the phone being locked or the app being blocked.
- Taps on buttons whose words only show in the picture, such as in web pages inside apps, are now checked against your rules too.
- Waiting for text matches whole words, so waiting for "Stop" no longer stops at "Stopwatch".
- After Home or opening an app, the agent's next step waits until the app in front has changed.
- Unlocking a phone from the computer no longer ends a handoff that asked you for more than the unlock.
- A tap on a dimmed backdrop, such as the area around a menu, lands where nothing lies on it instead of on what is underneath.
- Agents keep their Pinky connection when Pinky quits and pick up again when it reopens.
- The Windows and Linux packages now carry FFmpeg, so the live mirror works there without installing it.
- The command that Homebrew and the Linux packages install is now `pinkyctl`, because `pinky` is also a coreutils command: it took Pinky's place on many Macs and kept the `.deb` from installing on Debian and Ubuntu. Agents set up with `pinky mcp` need `pinkyctl mcp`, or the command in Settings › Agents.
- Settings › Agents shows ready-to-copy commands that add Pinky to Claude Code and other agents.

## 2026.1107.9 (2026-11-07)

- Unlocking an iPhone from the computer works again after the phone locked during a long session.
- Typing Chinese or other non-English text on an iPhone no longer fails the first time after a quiet spell.

## 2026.1107.8 (2026-11-07)

- On iPhones below iOS 27 without the WebDriverAgent helper, Pinky now says up front that taps, swipes and typing need the helper; it can still show and read the screen and open apps.

## 2026.1107.7 (2026-11-07)

- Pairing a phone over Wi-Fi no longer hangs on "Pairing…" when Android's adb tools have stopped seeing the network, as they do after the computer's network changes (for example when an iPhone is plugged in): Pinky now finds the phone itself.
- Phones paired over Wi-Fi come back by themselves when they return to the network, even when adb no longer sees it.
- When pairing over Wi-Fi takes long, Add phone says what is likely wrong, such as the phone not showing up on the network or, on a Mac, Pinky not allowed to use the local network, and offers to pair with a code instead.
- Pairing with a code now also works on networks where Pinky can't find the phone: it asks for the address Wireless debugging shows and connects there.
- On a Mac, Pinky asks for Local Network access to find Android phones over Wi-Fi, and says why.

## 2026.1107.6 (2026-11-07)

- The mirror no longer stays on "Connecting…" when you come back to a phone after a while.
- Unblocking an app in Settings › Safety now works for apps that are blocked by their id, such as Wise.
- An agent's tap on an element keeps working when the iPhone reports that element under another parent or role.
- Waiting for text now also finds text that only shows in the picture, such as in web pages inside apps.
- When a task lists app names that match no installed app, the agent is told which ones.

## 2026.1107.5 (2026-11-07)

- On Windows, Pinky now installs updates by itself, as on macOS: when you're not using it, or when you click Update now, it runs the new installer quietly, starts the new version and shows what's new, and goes back to the version you had if the new one doesn't start. Agents connected through `pinky mcp` stay connected.
- On macOS, Pinky installs an update only after checking that Apple notarized it.
- When an update fails, Settings › Advanced says why, for example that the new version didn't start.
- Homebrew no longer prints a deprecation warning about Pinky's cask with every `brew` command.
- When an agent asks you a question, the answer no longer offers "Allow for this task", which granted nothing there.
- Activity marks the steps that ran without asking because of an Always allow.

## 2026.1107.4 (2026-11-07)

- iPhones on iOS 17.4 to 26 can now be connected. With the WebDriverAgent helper, installed once from Xcode, agents can do everything they do on iOS 27; this is tried on the iOS 18 and 26 Simulators, not yet on such a phone.
- Without the helper, Pinky shows and reads an older iPhone's screen and opens apps; it cannot swipe or type there.
- Pinky says why it cannot drive an iPhone on iOS 17.3 or earlier.

## 2026.1107.3 (2026-11-07)

- Turning Record the screen, Keep activity, Read-only mode or usage statistics on and off quickly no longer leaves Pinky with an earlier choice than the switch shows.

## 2026.1107.2 (2026-11-07)

- "Always allow" on an approval now lasts: in that app, Pinky stops asking about steps like that one, in later tasks and after a restart. Settings › Safety › Always allowed lists them, and any of them can be made to ask again. It never lifts a block, read-only mode or what an agent may do, and "Ask before every action" still asks about everything.
- "Always allow" is offered only where Pinky can keep it: not for an agent's own questions, copying files, or opening apps and links.
- Settings › Safety has a rules editor: add, change, reorder and remove your own rules (apps, categories, actions, button text; Ask, Block or Allow), with Pinky's built-in rules shown alongside. Removing or loosening a rule that blocks asks you first.
- Buttons that show only an icon, such as a paper plane that sends or a trash can that deletes, now ask before they commit, like buttons with words.

## 2026.1107.1 (2026-11-07)

- The first release of Pinky: AI agents such as Claude Code, Codex and Cursor operate your iPhone or Android phone over MCP while you watch on your computer, approve risky steps, take over and look back at what happened.
- Android phones over USB or Wi-Fi. The companion app, which Pinky offers to install, reads the screen in about 70 ms and types any language; on Android 13 and later agents work on a background display while the phone stays locked.
- iPhones on iOS 27 or later over USB, with nothing to install on the phone.
- Unlock a locked phone from the computer with Touch ID (Windows Hello on Windows); agents never see the PIN.
- History of every task, with recordings and export.
- Pinky for macOS 12 or later (Apple silicon and Intel), Windows and Linux; on macOS, `brew install --cask diygod/tap/pinky` also installs the `pinky` command.
- Pinky keeps itself up to date: it tells you when a new version is out and what changed, installs it by itself when you're not using it (Settings › Advanced › Update automatically), and shows what's new afterwards.
- Pinky Companion on your Android phones is updated by itself while the phone is free, and its service stays on.
- Pinky shares anonymous usage statistics and crash reports to help find what to fix: counts such as tasks run and tools used, never your screens, what you type, app names, phone names or PINs. Turn it off in Settings › Privacy or with `DO_NOT_TRACK=1`; Settings › Privacy › What's shared shows exactly what goes out.
- Help › Send Feedback… opens an issue on GitHub with your message (and diagnostics, if you choose to attach them) filled in; you review it there and submit it yourself.
- Pinky speaks Simplified Chinese. It follows your Mac's language, and Settings › Advanced › Language switches between System, English and Simplified Chinese right away, without a restart.
- In Chinese, agents' steps, approval requests, phone status lines and update messages read in Chinese too. What agents and apps write stays as written.
