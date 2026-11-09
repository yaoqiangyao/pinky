# Recorded trees

The companion app's `tree` results from our Android 16 emulator (`pinky-api36`, the companion app of release 2026.1108.11), as `companionctl -s emulator-5560 tree -raw` printed them, for `hidden_test.go`. Nothing in them comes from anyone's phone: the notifications are the shell's (`cmd notification post`) and the emulator's own.

- `android16-emulator-shade-expanded.json`: the notification shade pulled down, with the shell's notification ("Card statement ready", "Your balance is 1,234.56, due 12 Nov") expanded, so its row starts with the app's name ("Shell"), and the system's "Serial console enabled" collapsed.
- `android16-emulator-shade-collapsed.json`: the same after a tap on the shell's Collapse button: its row starts with its title and names no app.
- `android16-emulator-recents.json`: recent apps with Settings' card in the middle and Chrome's at the left edge.

To take them again: post the notification with `adb shell "cmd notification post -S bigtext -t 'Card statement ready' statement 'Your balance is 1,234.56, due 12 Nov'"`, pull the shade down with `adb shell cmd statusbar expand-notifications`, and read the tree; for recent apps, open Settings and press `KEYCODE_APP_SWITCH`.
