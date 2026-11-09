package android

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/scrcpy"
)

// systemNames names packages that have no launcher entry but often own
// the screen.
var systemNames = map[string]string{
	"com.android.systemui": "System UI",
}

const appsMaxAge = 5 * time.Minute

// cachedApps returns the launcher apps and the app clones (clones.go),
// listing them again when the cached list is older than maxAge (0 means
// appsMaxAge). On a failed refresh it keeps the old list, and the old
// clones when only finding those failed.
func (d *Device) cachedApps(ctx context.Context, maxAge time.Duration) ([]driver.App, error) {
	if maxAge <= 0 {
		maxAge = appsMaxAge
	}
	d.appsMu.Lock()
	defer d.appsMu.Unlock()
	if d.apps != nil && time.Since(d.appsAt) < maxAge {
		return d.apps, nil
	}
	scanned := make(chan cloneScan, 1)
	go func() { scanned <- d.scanClones(ctx) }()
	list, err := d.mainApps(ctx)
	scan := <-scanned
	if err != nil {
		if d.apps != nil {
			return d.apps, nil
		}
		return nil, wrapErr(err)
	}
	apps := make([]driver.App, len(list))
	for i, a := range list {
		apps[i] = driver.App{ID: a.Package, Name: a.Name, System: a.System}
	}
	if scan.err != nil {
		d.cfg.logf("android: %s: looking for app clones: %v", d.info.ID, scan.err)
		for _, a := range d.apps {
			if a.CloneOf != nil {
				apps = append(apps, a)
			}
		}
	} else {
		clones, starts := cloneApps(apps, scan)
		apps = append(apps, clones...)
		d.clones = starts
		d.logClones(starts)
	}
	d.apps, d.appsAt = apps, time.Now()
	return apps, nil
}

// mainApps lists the person's own launcher apps with the scrcpy server.
func (d *Device) mainApps(ctx context.Context) ([]scrcpy.App, error) {
	if d.listApps != nil {
		return d.listApps(ctx)
	}
	return scrcpy.ListApps(ctx, d.client, d.info.ID)
}

// logClones logs the users that hold app clones when they change.
func (d *Device) logClones(starts map[string]clone) {
	count := map[int]int{}
	for _, c := range starts {
		count[c.user]++
	}
	users := make([]int, 0, len(count))
	for u := range count {
		users = append(users, u)
	}
	sort.Ints(users)
	var parts []string
	for _, u := range users {
		parts = append(parts, fmt.Sprintf("user %d: %d", u, count[u]))
	}
	if s := strings.Join(parts, ", "); s != d.cloneUsers {
		d.cloneUsers = s
		if s == "" {
			s = "none"
		}
		d.cfg.logf("android: %s: app clones: %s", d.info.ID, s)
	}
}

// appName returns the cached display name of a package, if known.
func (d *Device) appName(pkg string) string {
	if pkg == "" {
		return ""
	}
	d.appsMu.Lock()
	defer d.appsMu.Unlock()
	for _, a := range d.apps {
		if a.ID == pkg {
			return a.Name
		}
	}
	return systemNames[pkg]
}

// Apps implements driver.Device: the apps that have a launcher entry,
// named in the phone's language, as the scrcpy server lists them, and the
// clones of them that the phone keeps in other users (clones.go).
func (d *Device) Apps(ctx context.Context) ([]driver.App, error) {
	apps, err := d.cachedApps(ctx, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return append([]driver.App(nil), apps...), nil
}

// Launch implements driver.Device. App is a package, an app clone's id
// (driver.CloneID) or a display name; names are matched without regard to
// case, exactly first, then by prefix, then by substring, and must be
// unambiguous. The app starts with its launcher intent, like a tap on its
// icon: through the scrcpy server, or for a clone with `am start` in the
// clone's user.
func (d *Device) Launch(ctx context.Context, app string) error {
	t, err := d.resolveApp(ctx, app)
	if err != nil {
		return err
	}
	l, err := d.inputSession(ctx)
	if err != nil {
		return err
	}
	return d.start(ctx, l, t, 0)
}

// launchTarget is an app to start: one of the person's own, by package, or
// a clone.
type launchTarget struct {
	pkg   string
	clone *clone
}

// start starts an app on a display: l's, whose id is display.
func (d *Device) start(ctx context.Context, l *live, t launchTarget, display int) error {
	if t.clone != nil {
		return d.startClone(ctx, *t.clone, display)
	}
	return l.startApp(ctx, t.pkg)
}

func (l *live) startApp(ctx context.Context, pkg string) error {
	started, err := l.s.StartApp(ctx, pkg)
	if errors.Is(err, scrcpy.ErrAppNotFound) {
		return fmt.Errorf("android: launch %q: %w", pkg, driver.ErrNotFound)
	}
	if err != nil {
		return l.mapErr(err)
	}
	l.cfg.logf("android: %s: started %s (%s) on display %d", l.serial, started.Name, started.Package, started.Display)
	return nil
}

// resolveApp turns a package, a clone's id or a display name into the app
// to start.
func (d *Device) resolveApp(ctx context.Context, query string) (launchTarget, error) {
	id, err := d.resolveAppID(ctx, query)
	if err != nil {
		return launchTarget{}, err
	}
	if _, _, ok := driver.ParseCloneID(id); !ok {
		return launchTarget{pkg: id}, nil
	}
	d.appsMu.Lock()
	c, ok := d.clones[id]
	d.appsMu.Unlock()
	if !ok {
		return launchTarget{}, fmt.Errorf("android: launch %q: no such app clone: %w", id, driver.ErrNotFound)
	}
	return launchTarget{pkg: c.pkg, clone: &c}, nil
}

// resolveAppID turns a package, a clone's id or a display name into an
// app's id.
func (d *Device) resolveAppID(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("android: launch: empty app name: %w", driver.ErrNotFound)
	}
	apps, err := d.cachedApps(ctx, 0)
	if err != nil {
		if looksLikePackage(query) {
			return query, nil
		}
		return "", err
	}
	pkg, err := matchApp(apps, query)
	if err == nil {
		return pkg, nil
	}
	if errors.Is(err, errAmbiguous) {
		return "", err
	}
	// The list may predate an install: refresh once.
	d.appsMu.Lock()
	stale := time.Since(d.appsAt) > 10*time.Second
	d.appsMu.Unlock()
	if stale {
		if apps, rerr := d.cachedApps(ctx, time.Nanosecond); rerr == nil {
			if pkg, err2 := matchApp(apps, query); err2 == nil {
				return pkg, nil
			}
		}
	}
	if looksLikePackage(query) {
		// Not a launcher app; the server still tries the package.
		return query, nil
	}
	return "", err
}

var errAmbiguous = errors.New("ambiguous app name")

// matchApp finds the one app a query means: an exact package, then a name
// that matches exactly, by prefix or by substring, ignoring case.
func matchApp(apps []driver.App, query string) (string, error) {
	for _, a := range apps {
		if a.ID == query {
			return a.ID, nil
		}
	}
	q := strings.ToLower(query)
	tests := []func(name string) bool{
		func(n string) bool { return n == q },
		func(n string) bool { return strings.HasPrefix(n, q) },
		func(n string) bool { return strings.Contains(n, q) },
	}
	for _, test := range tests {
		var hits []driver.App
		for _, a := range apps {
			if test(strings.ToLower(strings.TrimSpace(a.Name))) {
				hits = append(hits, a)
			}
		}
		switch len(hits) {
		case 0:
			continue
		case 1:
			return hits[0].ID, nil
		default:
			names := make([]string, len(hits))
			for i, h := range hits {
				names[i] = fmt.Sprintf("%s (%s)", h.Name, h.ID)
			}
			sort.Strings(names)
			return "", fmt.Errorf("android: launch %q: %w: %s", query, errAmbiguous, strings.Join(names, ", "))
		}
	}
	// An English name on a phone in another language ("Calculator" for
	// 计算器): a package whose last parts name it.
	var byID []driver.App
	for _, a := range apps {
		if a.CloneOf != nil {
			continue // its id has the parts of its original's
		}
		segs := strings.Split(strings.ToLower(a.ID), ".")
		for _, seg := range segs[1:] {
			if seg == q || len(q) >= 4 && strings.Contains(seg, q) {
				byID = append(byID, a)
				break
			}
		}
	}
	if len(byID) == 1 {
		return byID[0].ID, nil
	}
	return "", fmt.Errorf("android: launch %q: no app with that name or package: %w", query, driver.ErrNotFound)
}

func looksLikePackage(s string) bool {
	if !strings.Contains(s, ".") || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_') {
			return false
		}
	}
	return true
}
