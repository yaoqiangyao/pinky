package android

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/scrcpy"
)

func TestParseUsers(t *testing.T) {
	// pm list users on a Xiaomi with dual apps and a work profile, then
	// cmd user list -v from Android 14, which adds the types.
	out := "Users:\n" +
		"\tUserInfo{0:机主:c13} running\n" +
		"\tUserInfo{10:Work profile:1030} running\n" +
		"\tUserInfo{999:XSpace:801010} running\r\n" +
		"\tUserInfo{11:Second space:410}\n" +
		"3 users:\n\n" +
		"0: id=0, name=机主, type=full.SYSTEM, flags=ADMIN|FULL|INITIALIZED|MAIN|PRIMARY|SYSTEM (running) (current) (visible)\n" +
		"1: id=10, name=Work profile, type=profile.MANAGED, flags=INITIALIZED|MANAGED_PROFILE|PROFILE (parentId=0) (running)\n" +
		"2: id=999, name=XSpace, type=android.os.usertype.profile.CLONE, flags=INITIALIZED|PROFILE|0x800000 (parentId=0) (running)\n" +
		"3: id=12, name=Paused, type=profile.MANAGED, flags=INITIALIZED|MANAGED_PROFILE|PROFILE|QUIET_MODE (parentId=0)\n"
	users := parseUsers(out)
	want := []phoneUser{
		{id: 0, name: "机主", flags: 0xc13, running: true, typ: "full.SYSTEM"},
		{id: 10, name: "Work profile", flags: 0x1030, running: true, typ: "profile.MANAGED"},
		{id: 999, name: "XSpace", flags: 0x801010, running: true, typ: "profile.CLONE"},
		{id: 11, name: "Second space", flags: 0x410},
		{id: 12, name: "Paused", flags: userFlagQuietMode, typ: "profile.MANAGED"},
	}
	if len(users) != len(want) {
		t.Fatalf("users = %+v", users)
	}
	for i := range want {
		if users[i] != want[i] {
			t.Errorf("user %d = %+v; want %+v", i, users[i], want[i])
		}
	}
}

func TestCloneSpace(t *testing.T) {
	for _, c := range []struct {
		desc string
		u    phoneUser
		want bool
	}{
		{"the person's own user", phoneUser{id: 0, running: true}, false},
		{"Xiaomi's XSpace", phoneUser{id: 999, name: "XSpace", flags: 0x801010, running: true}, true},
		{"OPPO's clone user", phoneUser{id: 999, name: "应用分身", flags: 0x20001010, running: true}, true},
		{"a clone user with the full flag", phoneUser{id: 999, name: "MultiApp", flags: 0x4000418, running: true}, true},
		{"Huawei's App Twin", phoneUser{id: 128, name: "ツイン", flags: 0x4000010, running: true}, true},
		{"Samsung's Dual Messenger", phoneUser{id: 95, name: "DUAL_APP", flags: 0x20001030, running: true}, true},
		{"Android 14's clone profile", phoneUser{id: 11, name: "Clone", flags: 0x1010, running: true, typ: "profile.CLONE"}, true},
		{"a clone space that is not running", phoneUser{id: 999, name: "XSpace", flags: 0x801010}, false},
		{"a work profile", phoneUser{id: 10, name: "Work profile", flags: 0x1030, running: true, typ: "profile.MANAGED"}, false},
		{"Samsung's Secure Folder", phoneUser{id: 150, name: "Secure Folder", flags: 0x1030, running: true}, false},
		{"another person's user", phoneUser{id: 10, name: "Alex", flags: 0x410, running: true, typ: "full.SECONDARY"}, false},
		{"a guest", phoneUser{id: 11, name: "Guest", flags: 0x414, running: true}, false},
		{"a private space", phoneUser{id: 999, name: "Private", flags: 0x1010, running: true, typ: "profile.PRIVATE"}, false},
		{"a paused space", phoneUser{id: 999, name: "XSpace", flags: 0x801010 | userFlagQuietMode, running: true}, false},
		{"a disabled space", phoneUser{id: 128, flags: 0x10 | userFlagDisabled, running: true}, false},
	} {
		if got := cloneSpace(c.u); got != c.want {
			t.Errorf("%s: cloneSpace = %v; want %v", c.desc, got, c.want)
		}
	}
}

func TestParseLauncherActivities(t *testing.T) {
	out := userSection + "999\n" +
		"com.tencent.mm/.ui.LauncherUI\n" +
		"com.whatsapp/com.whatsapp.Main\r\n" +
		"com.whatsapp/com.whatsapp.Second\n" +
		"com.example/.Outer$Inner\n" +
		"No activities found\n" +
		userSection + "95\n" +
		"No activities found\n" +
		userSection + "128\n" +
		"  Activity #0:\n" + // --brief output, if --components is missing
		"    priority=0 preferredOrder=0 match=0x108000 specificIndex=-1 isDefault=false\n" +
		"    com.tencent.mobileqq/.activity.SplashActivity\n"
	got := parseLauncherActivities(out)
	want := map[int]map[string]string{
		999: {
			"com.tencent.mm": "com.tencent.mm/com.tencent.mm.ui.LauncherUI",
			"com.whatsapp":   "com.whatsapp/com.whatsapp.Main",
			"com.example":    "com.example/com.example.Outer$Inner",
		},
		95:  {},
		128: {"com.tencent.mobileqq": "com.tencent.mobileqq/com.tencent.mobileqq.activity.SplashActivity"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for u, apps := range want {
		if len(got[u]) != len(apps) {
			t.Errorf("user %d: %v; want %v", u, got[u], apps)
		}
		for p, c := range apps {
			if got[u][p] != c {
				t.Errorf("user %d %s = %q; want %q", u, p, got[u][p], c)
			}
		}
	}
}

func TestCloneApps(t *testing.T) {
	main := []driver.App{
		{ID: "com.tencent.mm", Name: "微信"},
		{ID: "com.whatsapp", Name: "WhatsApp"},
		{ID: "com.android.settings", Name: "设置", System: true},
	}
	scan := cloneScan{locale: "zh-Hans-CN", activities: map[int]map[string]string{
		999: {
			"com.tencent.mm":       "com.tencent.mm/com.tencent.mm.ui.LauncherUI",
			"com.android.settings": "com.android.settings/com.android.settings.Settings",
			"com.unknown":          "com.unknown/com.unknown.Main",
		},
		128: {"com.tencent.mm": "com.tencent.mm/com.tencent.mm.ui.LauncherUI"},
	}}
	apps, starts := cloneApps(main, scan)
	if len(apps) != 2 {
		t.Fatalf("apps = %+v", apps)
	}
	// Users in order, so the lower one keeps the plain name.
	if a := apps[0]; a.ID != "com.tencent.mm@128" || a.Name != "微信（分身）" || a.CloneOf == nil || a.CloneOf.ID != "com.tencent.mm" || a.CloneOf.Name != "微信" || a.System {
		t.Errorf("first clone = %+v", a)
	}
	if a := apps[1]; a.ID != "com.tencent.mm@999" || a.Name != "微信（分身 2）" {
		t.Errorf("second clone = %+v", a)
	}
	if c := starts["com.tencent.mm@999"]; c.user != 999 || c.pkg != "com.tencent.mm" || c.component != "com.tencent.mm/com.tencent.mm.ui.LauncherUI" {
		t.Errorf("start = %+v", c)
	}
	if o := apps[0].Original(); o.ID != "com.tencent.mm" || o.CloneOf != nil {
		t.Errorf("Original = %+v", o)
	}
	if apps, _ := cloneApps(main, cloneScan{}); apps != nil {
		t.Errorf("no clone spaces: %+v", apps)
	}
}

func TestCloneName(t *testing.T) {
	for _, c := range []struct {
		name, locale string
		n            int
		want         string
	}{
		{"微信", "zh-Hans-CN", 1, "微信（分身）"},
		{"微信", "zh_TW", 2, "微信（分身 2）"},
		{"LINE", "ja-JP", 1, "LINE（クローン）"},
		{"WhatsApp", "en-US", 1, "WhatsApp (clone)"},
		{"WhatsApp", "", 3, "WhatsApp (clone 3)"},
	} {
		if got := cloneName(c.name, c.locale, c.n); got != c.want {
			t.Errorf("cloneName(%q, %q, %d) = %q; want %q", c.name, c.locale, c.n, got, c.want)
		}
	}
}

func TestAMStartError(t *testing.T) {
	for out, want := range map[string]string{
		"Starting: Intent { act=android.intent.action.MAIN cmp=com.tencent.mm/.ui.LauncherUI }\n":                                 "",
		"Starting: Intent { ... }\nWarning: Activity not started, its current task has been brought to the front\n":               "",
		"Starting: Intent { ... }\nError type 3\nError: Activity class {com.x/com.x.Main} does not exist.\n":                      "Error: Activity class {com.x/com.x.Main} does not exist.",
		"Starting: Intent { ... }\nError: Activity not started, unable to resolve Intent { ... }\n":                               "Error: Activity not started, unable to resolve Intent { ... }",
		"Starting: Intent { ... }\njava.lang.SecurityException: Permission Denial: startActivityAsUser asks to run as user 999\n": "java.lang.SecurityException: Permission Denial: startActivityAsUser asks to run as user 999",
	} {
		got, failed := amStartError(out)
		if got != want || failed != (want != "") {
			t.Errorf("amStartError(%q) = %q, %v; want %q", out, got, failed, want)
		}
	}
}

// clonePhone answers the shell commands of a phone with WeChat, WhatsApp
// and Settings, a work profile (user 10, with a profile owner) and a
// clone space (user 999, Xiaomi's XSpace).
type clonePhone struct {
	mu       sync.Mutex
	cmds     []string
	users    string // pm list users and cmd user list -v
	scanFail bool   // the clone space's activities cannot be read
	amOut    string
}

func newClonePhone() *clonePhone {
	return &clonePhone{
		users: "Users:\n\tUserInfo{0:Owner:c13} running\n\tUserInfo{10:Work profile:1030} running\n\tUserInfo{999:XSpace:801010} running\n",
		amOut: "Starting: Intent { act=android.intent.action.MAIN cat=[android.intent.category.LAUNCHER] flg=0x10200000 cmp=com.tencent.mm/.ui.LauncherUI }\n",
	}
}

func (p *clonePhone) run(_, service string) (string, bool) {
	cmd, ok := strings.CutPrefix(service, "shell:")
	if !ok {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cmds = append(p.cmds, cmd)
	switch {
	case strings.HasPrefix(cmd, "pm list users"):
		return p.users, true
	case strings.HasPrefix(cmd, "dumpsys device_policy"):
		if p.scanFail {
			return "", false
		}
		if strings.Contains(cmd, "--user 10 ") {
			return "", false // the work profile is never looked into
		}
		return "Profile Owner (User 10): \n" + localeSection + "\nzh-Hans-CN\n\n" +
			userSection + "999\n" +
			"com.tencent.mm/.ui.LauncherUI\n" +
			"com.android.settings/.Settings\n", true
	case strings.HasPrefix(cmd, "am start "):
		return p.amOut, true
	}
	return "", false
}

func (p *clonePhone) commands() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.cmds...)
}

func newCloneDevice(t *testing.T, p *clonePhone) *Device {
	t.Helper()
	f := startFakeADB(t)
	f.set(func(f *fakeADB) {
		f.props = map[string]string{"phone": ""}
		f.run = p.run
	})
	d := newDevice(f.config(), driver.Info{ID: "phone"}, deviceProps{SDK: 34})
	d.listApps = func(context.Context) ([]scrcpy.App, error) {
		return []scrcpy.App{
			{Name: "微信", Package: "com.tencent.mm"},
			{Name: "WhatsApp", Package: "com.whatsapp"},
			{Name: "设置", Package: "com.android.settings", System: true},
		}, nil
	}
	return d
}

// GitHub issue #18: the clones of a phone's apps are listed as apps of
// their own and start in their user, on the main screen or on a
// background display.
func TestAppClones(t *testing.T) {
	ctx := context.Background()
	p := newClonePhone()
	d := newCloneDevice(t, p)
	apps, err := d.Apps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var clones []driver.App
	for _, a := range apps {
		if a.CloneOf != nil {
			clones = append(clones, a)
		}
	}
	if len(apps) != 4 || len(clones) != 1 || clones[0].ID != "com.tencent.mm@999" || clones[0].Name != "微信（分身）" || clones[0].CloneOf.ID != "com.tencent.mm" {
		t.Fatalf("apps = %+v", apps)
	}
	for _, c := range p.commands() {
		if strings.Contains(c, "--user 10 ") {
			t.Errorf("looked into the work profile: %s", c)
		}
	}
	// The person's own app by its name, the clone by its name or id.
	if tgt, err := d.resolveApp(ctx, "微信"); err != nil || tgt.clone != nil || tgt.pkg != "com.tencent.mm" {
		t.Errorf("微信 = %+v, %v", tgt, err)
	}
	for _, q := range []string{"微信（分身）", "com.tencent.mm@999"} {
		tgt, err := d.resolveApp(ctx, q)
		if err != nil || tgt.clone == nil || tgt.clone.user != 999 || tgt.pkg != "com.tencent.mm" {
			t.Errorf("%s = %+v, %v", q, tgt, err)
		}
	}
	if _, err := d.resolveApp(ctx, "com.whatsapp@999"); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("a clone the phone lacks: %v", err)
	}
	// "tencent" still means WeChat, not WeChat or its clone.
	if tgt, err := d.resolveApp(ctx, "tencent"); err != nil || tgt.pkg != "com.tencent.mm" || tgt.clone != nil {
		t.Errorf("tencent = %+v, %v", tgt, err)
	}

	// The main screen.
	tgt, _ := d.resolveApp(ctx, "微信（分身）")
	if err := d.start(ctx, nil, tgt, 0); err != nil {
		t.Fatal(err)
	}
	// A background display.
	b := &Background{d: d, display: 7, closed: make(chan struct{})}
	if err := b.startApp(ctx, tgt); err != nil {
		t.Fatal(err)
	}
	var starts []string
	for _, c := range p.commands() {
		if strings.HasPrefix(c, "am start ") {
			starts = append(starts, c)
		}
	}
	want := []string{
		"am start --user 999 --display 0 -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -f 0x10200000 -n 'com.tencent.mm/com.tencent.mm.ui.LauncherUI' 2>&1",
		"am start --user 999 --display 7 -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -f 0x10200000 -n 'com.tencent.mm/com.tencent.mm.ui.LauncherUI' 2>&1",
	}
	if strings.Join(starts, "\n") != strings.Join(want, "\n") {
		t.Errorf("starts:\n%s\nwant:\n%s", strings.Join(starts, "\n"), strings.Join(want, "\n"))
	}

	// A clone uninstalled since: not found, so the agent lists apps again.
	p.mu.Lock()
	p.amOut = "Starting: Intent { ... }\nError type 3\nError: Activity class {com.tencent.mm/com.tencent.mm.ui.LauncherUI} does not exist.\n"
	p.mu.Unlock()
	if err := d.start(ctx, nil, tgt, 0); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("gone: %v", err)
	}
	p.mu.Lock()
	p.amOut = "Starting: Intent { ... }\njava.lang.SecurityException: Permission Denial\n"
	p.mu.Unlock()
	if err := d.start(ctx, nil, tgt, 0); err == nil || errors.Is(err, driver.ErrNotFound) || !strings.Contains(err.Error(), "Permission Denial") {
		t.Errorf("refused: %v", err)
	}
}

// A phone without clone spaces costs one shell command; when reading the
// clones fails, the clones listed before stay.
func TestAppClonesRefresh(t *testing.T) {
	ctx := context.Background()
	p := newClonePhone()
	p.users = "Users:\n\tUserInfo{0:Owner:c13} running\n\tUserInfo{10:Work profile:1030} running\n"
	d := newCloneDevice(t, p)
	apps, err := d.Apps(ctx)
	if err != nil || len(apps) != 3 {
		t.Fatalf("apps = %+v, %v", apps, err)
	}
	if cmds := p.commands(); len(cmds) != 1 {
		t.Errorf("commands without clone spaces: %q", cmds)
	}

	p = newClonePhone()
	d = newCloneDevice(t, p)
	if apps, _ := d.cachedApps(ctx, 0); len(apps) != 4 {
		t.Fatalf("apps = %+v", apps)
	}
	p.mu.Lock()
	p.scanFail = true
	p.mu.Unlock()
	apps, err = d.cachedApps(ctx, 1)
	if err != nil || len(apps) != 4 || apps[3].ID != "com.tencent.mm@999" {
		t.Fatalf("after a failed scan: %+v, %v", apps, err)
	}
	if _, err := d.resolveApp(ctx, "com.tencent.mm@999"); err != nil {
		t.Errorf("the clone kept its start: %v", err)
	}
}
