package android

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/DIYgod/pinky/internal/driver"
)

// App clones: a second copy of an installed app with its own sign-in and
// data, which the phone keeps in another Android user. Xiaomi calls them
// 双开 / Dual apps (user 999, "XSpace"), OPPO, OnePlus, realme and vivo
// 应用分身 / App Cloner (user 999), Huawei and Honor 应用分身 / App Twin
// (user 128), Samsung Dual Messenger (user 95, "DUAL_APP"), and Android 14
// and later have a clone profile type of their own. scrcpy lists and starts
// the apps of user 0 only, so the driver finds the clones itself and starts
// them with `am start --user`.
//
// Only users that are clone spaces count: never another person's user, a
// guest, a work profile (it has a profile owner, the employer's device
// policy app), a private space or Samsung's Secure Folder.

// clone is how to start an app clone.
type clone struct {
	pkg       string
	user      int
	component string // its launcher activity, "com.tencent.mm/.ui.LauncherUI"
}

// phoneUser is an Android user as `pm list users` and `cmd user list -v`
// report it.
type phoneUser struct {
	id      int
	name    string
	flags   uint64
	running bool
	typ     string // "profile.CLONE"; "" before Android 11
}

// Android's user flags that matter here (UserInfo.FLAG_*). Makers add their
// own in the high bits.
const (
	userFlagGuest     = 0x4
	userFlagDisabled  = 0x40
	userFlagQuietMode = 0x80 // a work profile that is paused
)

var userFlagNames = map[string]uint64{"GUEST": userFlagGuest, "DISABLED": userFlagDisabled, "QUIET_MODE": userFlagQuietMode}

var (
	// `pm list users`: "\tUserInfo{999:XSpace:801010} running".
	userInfoRE = regexp.MustCompile(`UserInfo\{(\d+):(.*):([0-9a-fA-F]+)\}(.*)`)
	// `cmd user list -v` (Android 11+): "1: id=999, name=XSpace,
	// type=profile.CLONE, flags=INITIALIZED|PROFILE (parentId=0) (running)";
	// makers' own flags come as hex ("INITIALIZED|PROFILE|0x800000").
	userVerboseRE = regexp.MustCompile(`\bid=(\d+), name=(.*), type=([A-Za-z0-9_.]+), flags=([A-Za-z0-9_|]*)(.*)`)
	// `dumpsys device_policy`: "Profile Owner (User 10): ".
	profileOwnerRE = regexp.MustCompile(`Profile Owner \(User (\d+)\)`)
)

// parseUsers reads the output of `pm list users` and of `cmd user list -v`
// in any order: the second adds the users' types.
func parseUsers(out string) []phoneUser {
	byID := map[int]*phoneUser{}
	var order []int
	get := func(id int) *phoneUser {
		if u := byID[id]; u != nil {
			return u
		}
		byID[id] = &phoneUser{id: id}
		order = append(order, id)
		return byID[id]
	}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if m := userInfoRE.FindStringSubmatch(line); m != nil {
			id, err := strconv.Atoi(m[1])
			flags, ferr := strconv.ParseUint(m[3], 16, 64)
			if err != nil || ferr != nil {
				continue
			}
			u := get(id)
			u.name, u.flags = m[2], flags
			u.running = u.running || strings.Contains(m[4], "running")
			continue
		}
		if m := userVerboseRE.FindStringSubmatch(line); m != nil {
			id, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			u := get(id)
			if u.name == "" {
				u.name = m[2]
			}
			u.typ = strings.TrimPrefix(m[3], "android.os.usertype.")
			for _, f := range strings.Split(m[4], "|") {
				u.flags |= userFlagNames[f]
			}
			u.running = u.running || strings.Contains(m[5], "(running)")
		}
	}
	users := make([]phoneUser, 0, len(order))
	for _, id := range order {
		users = append(users, *byID[id])
	}
	return users
}

// cloneSpace reports whether a user holds app clones, by its type where
// Android says it (Android 14's clone profile) and otherwise by the ids and
// names the makers use. A user that is not running, is a guest, is
// disabled or paused, or is a private space never counts; a work profile
// is ruled out by its profile owner (profileOwners).
func cloneSpace(u phoneUser) bool {
	if u.id <= 0 || !u.running || u.flags&(userFlagGuest|userFlagDisabled|userFlagQuietMode) != 0 {
		return false
	}
	switch u.typ {
	case "profile.CLONE":
		return true
	case "profile.PRIVATE", "full.GUEST", "full.DEMO", "full.RESTRICTED":
		return false
	}
	switch {
	case u.id == 999: // Xiaomi's XSpace; OPPO, OnePlus, realme, vivo
	case u.id == 128: // Huawei's and Honor's App Twin
	case u.name == "DUAL_APP": // Samsung's Dual Messenger, user 95
	default:
		return false
	}
	return true
}

// profileOwners returns the users that have a profile owner in the output
// of `dumpsys device_policy`: work profiles, and Samsung's Knox spaces.
func profileOwners(out string) map[int]bool {
	owned := map[int]bool{}
	for _, m := range profileOwnerRE.FindAllStringSubmatch(out, -1) {
		if id, err := strconv.Atoi(m[1]); err == nil {
			owned[id] = true
		}
	}
	return owned
}

// componentRE is a launcher activity as `cmd package query-activities
// --components` prints it.
var componentRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+)/([A-Za-z0-9_.$]+)$`)

// parseLauncherActivities reads the launcher activities of each user from
// a run of userSection markers, each followed by the user's
// `query-activities` output: package -> first component, by user.
func parseLauncherActivities(out string) map[int]map[string]string {
	by := map[int]map[string]string{}
	user := -1
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, userSection); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
				user = n
				if by[n] == nil {
					by[n] = map[string]string{}
				}
				continue
			}
		}
		m := componentRE.FindStringSubmatch(line)
		if m == nil || user < 0 {
			continue
		}
		pkg, cls := m[1], m[2]
		if strings.HasPrefix(cls, ".") {
			cls = pkg + cls
		}
		if _, ok := by[user][pkg]; !ok {
			by[user][pkg] = pkg + "/" + cls
		}
	}
	return by
}

const (
	userSection   = "::pinky-user "
	localeSection = "::pinky-locale"
)

// cloneScan is what scanClones found: each clone space's launcher
// activities, and the phone's language for the clones' names.
type cloneScan struct {
	activities map[int]map[string]string
	locale     string
	err        error
}

// scanClones finds the clone spaces and their launcher activities. A phone
// without any costs one shell command.
func (d *Device) scanClones(ctx context.Context) cloneScan {
	out, err := d.client.Shell(ctx, d.info.ID, "pm list users 2>/dev/null; cmd user list -v 2>/dev/null")
	if err != nil {
		return cloneScan{err: wrapErr(err)}
	}
	var ids []int
	for _, u := range parseUsers(string(out)) {
		if cloneSpace(u) {
			ids = append(ids, u.id)
		}
	}
	if len(ids) == 0 {
		return cloneScan{}
	}
	var b strings.Builder
	b.WriteString("dumpsys device_policy 2>/dev/null | grep 'Profile Owner'; echo '" + localeSection + "'; getprop persist.sys.locale; getprop ro.product.locale")
	for _, id := range ids {
		fmt.Fprintf(&b, "; echo '%s%d'; cmd package query-activities --components --user %d -a android.intent.action.MAIN -c android.intent.category.LAUNCHER 2>&1", userSection, id, id)
	}
	out, err = d.client.Shell(ctx, d.info.ID, b.String())
	if err != nil {
		return cloneScan{err: wrapErr(err)}
	}
	text := strings.ReplaceAll(string(out), "\r", "")
	head, rest, _ := strings.Cut(text, localeSection)
	owned := profileOwners(head)
	var locale string
	for _, l := range strings.Split(rest, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, userSection) {
			break
		}
		if l != "" && locale == "" {
			locale = l
		}
	}
	acts := parseLauncherActivities(rest)
	for id := range acts {
		if owned[id] || !slices.Contains(ids, id) {
			delete(acts, id)
		}
	}
	return cloneScan{activities: acts, locale: locale}
}

// cloneApps returns the clones of the person's own apps that the scan
// found, named as clones in the phone's language, and how to start each.
// System apps that a clone space holds (makers install some there for the
// clones' use) are not clones the person made, and are left out.
func cloneApps(main []driver.App, scan cloneScan) ([]driver.App, map[string]clone) {
	if len(scan.activities) == 0 {
		return nil, nil
	}
	byPkg := map[string]driver.App{}
	for _, a := range main {
		if a.CloneOf == nil {
			byPkg[a.ID] = a
		}
	}
	users := make([]int, 0, len(scan.activities))
	for u := range scan.activities {
		users = append(users, u)
	}
	sort.Ints(users)
	var apps []driver.App
	starts := map[string]clone{}
	nth := map[string]int{} // package -> clones named so far
	for _, u := range users {
		pkgs := make([]string, 0, len(scan.activities[u]))
		for p := range scan.activities[u] {
			pkgs = append(pkgs, p)
		}
		sort.Strings(pkgs)
		for _, p := range pkgs {
			orig, ok := byPkg[p]
			if !ok || orig.System {
				continue
			}
			nth[p]++
			id := driver.CloneID(p, u)
			o := driver.App{ID: orig.ID, Name: orig.Name}
			apps = append(apps, driver.App{ID: id, Name: cloneName(orig.Name, scan.locale, nth[p]), CloneOf: &o})
			starts[id] = clone{pkg: p, user: u, component: scan.activities[u][p]}
		}
	}
	return apps, starts
}

// cloneName names a clone the way the phone's language would: "微信（分身）",
// "LINE（クローン）", "WhatsApp (clone)"; a second clone of the same app
// gets a number.
func cloneName(name, locale string, n int) string {
	lang, _, _ := strings.Cut(strings.ToLower(locale), "-")
	lang, _, _ = strings.Cut(lang, "_")
	num := ""
	if n > 1 {
		num = " " + strconv.Itoa(n)
	}
	switch lang {
	case "zh":
		return name + "（分身" + num + "）"
	case "ja":
		return name + "（クローン" + num + "）"
	}
	return name + " (clone" + num + ")"
}

// startClone starts a clone's launcher activity in its user on a display,
// as a tap on its icon would.
func (d *Device) startClone(ctx context.Context, c clone, display int) error {
	if !componentRE.MatchString(c.component) {
		return fmt.Errorf("android: launch %s in user %d: no launcher activity: %w", c.pkg, c.user, driver.ErrNotFound)
	}
	start := func(withDisplay bool) (string, error) {
		cmd := fmt.Sprintf("am start --user %d", c.user)
		if withDisplay {
			cmd += fmt.Sprintf(" --display %d", display)
		}
		cmd += fmt.Sprintf(" -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -f 0x10200000 -n '%s'", c.component)
		out, err := d.client.Shell(ctx, d.info.ID, cmd+" 2>&1")
		return string(out), wrapErr(err)
	}
	out, err := start(true)
	if err == nil && display == 0 && strings.Contains(out, "Unknown option: --display") {
		out, err = start(false) // Android 7 and earlier
	}
	if err != nil {
		return err
	}
	if msg, ok := amStartError(out); ok {
		if strings.Contains(msg, "does not exist") || strings.Contains(msg, "unable to resolve") {
			return fmt.Errorf("android: launch %s in user %d: %s: %w", c.pkg, c.user, msg, driver.ErrNotFound)
		}
		return fmt.Errorf("android: launch %s in user %d: %s", c.pkg, c.user, msg)
	}
	d.cfg.logf("android: %s: started %s in user %d on display %d", d.info.ID, c.component, c.user, display)
	return nil
}

// amStartError returns the line of `am start`'s output that says it
// failed. Warnings that the app was already in front are not failures.
func amStartError(out string) (string, bool) {
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Error") || strings.Contains(line, "Exception") {
			if strings.HasPrefix(line, "Error type") {
				continue // the detail follows on the next line
			}
			return line, true
		}
	}
	return "", false
}
