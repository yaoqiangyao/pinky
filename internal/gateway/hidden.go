package gateway

import (
	"context"
	"errors"
	"image"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// A blocked app on another app's screen. Its own screen never reaches an
// agent (agentView), but its notifications show in the notification shade,
// on the lock screen and as banners over other apps, and its last screen
// shows in recent apps (an iPhone's app switcher), all of it on screens
// that belong to the system UI or the launcher. Each read finds them
// (hideBlocked) and puts one hidden element in place of what each one
// shows: the agent reads that something is hidden there, and pictures of
// the screen have its place covered with a gray box, as one-time codes are
// (coverHidden). Where Pinky can tell that a blocked app's card is on the
// screen but not where it ends, the hidden element has no place, and the
// agent gets no screenshot.
//
// A notification row is the system UI's expandableNotificationRow: one
// element on phones with the companion app, which merges the row's text
// ("Wise, •, now, Card charged, €12.00"), or the elements inside one
// (uiautomator), gathered by their place. An iPhone's banner over an app
// is one element the driver lists with the app that posted it in its id
// (screen.NotificationID). A row is a blocked app's when that app is,
// when it starts with a blocked app's name, which Android shows only while
// the row is expanded, or when it says what a blocked app's notification
// says: the phone's list of notifications (driver.NotificationLister) and
// the notifications the agent was not shown give those texts. On a system
// screen, a large element named after a blocked app is its card in recent
// apps (Android's task snapshot, an iPhone's app switcher card), a widget,
// or an iPhone's notification cell, whose name comes first.
//
// Recordings, which stay on this computer and never go to agents, show the
// screen as it was.

// Labels of the hidden elements, which agents read.
const (
	hiddenNotification = "Hidden by Pinky: a notification from a blocked app"
	hiddenCard         = "Hidden by Pinky: a blocked app's card"
)

// errHiddenShot means a blocked app's card is on the screen and Pinky could
// not tell where to cover it.
var errHiddenShot = errors.New("a blocked app's card is on the screen, and Pinky could not tell where to cover it")

// hideHints are what hideParts knows of the blocked apps.
type hideHints struct {
	// system is set on the screens of the system UI and launchers and on
	// the lock screen, where cards in recent apps and widgets are looked
	// for.
	system bool
	// blockedName reports whether a display name is a blocked app's.
	blockedName func(name string) bool
	// blockedApp reports whether an app id is a blocked app's; it may be
	// nil.
	blockedApp func(id string) bool
	// texts are what blocked apps' notifications say, normalized (normText).
	texts []string
}

type partKind int

const (
	partNotification partKind = iota
	partCard
)

// part is a notification row or a card on the screen.
type part struct {
	kind  partKind
	r     screen.Rect
	idx   int // the element that is the part; -1 for rows gathered by place
	label string
	path  string
	id    string
}

// hideParts replaces the elements of blocked apps' notification rows and
// cards with one hidden element each, and returns how many it hid.
func hideParts(s *screen.Screen, h hideHints) int {
	els := s.Elements
	if len(els) == 0 || h.blockedName == nil {
		return 0
	}
	var parts []part
	for i, e := range els {
		if e.Source == screen.SourceOCR || !isRowID(e.ID) {
			continue
		}
		// An iPhone's banner may come without a place; a blocked app's is
		// then hidden without one, which withholds screenshots.
		if _, banner := screen.NotificationApp(e.ID); !e.Bounds.Empty() || banner {
			parts = append(parts, part{kind: partNotification, r: e.Bounds, idx: i, label: e.Label, path: e.Path, id: e.ID})
		}
	}
	var loose []int
	for i, e := range els {
		if e.Source == screen.SourceOCR || e.Bounds.Empty() || !inRowPath(e.Path) || isRowID(e.ID) || insideAny(parts, e.Bounds) {
			continue
		}
		loose = append(loose, i)
	}
	parts = append(parts, gatherRows(els, loose, s.Height)...)
	if h.system {
		// Cards may hold cards (an app switcher labelled as a whole): each
		// is looked at, and a blocked one inside a blocked one goes with it.
		rows := parts
		minArea := s.Width * s.Height / 50
		for i, e := range els {
			if e.Source == screen.SourceOCR || e.Label == "" || e.Hidden || e.Bounds.W*e.Bounds.H < minArea || insideAny(rows, e.Bounds) {
				continue
			}
			parts = append(parts, part{kind: partCard, r: e.Bounds, idx: i, label: e.Label, path: e.Path, id: e.ID})
		}
	}

	var blocked []part
	for _, p := range parts {
		if partBlocked(els, p, h) {
			blocked = append(blocked, p)
		}
	}
	// A part inside another blocked one goes with it.
	var outer []part
	for i, p := range blocked {
		in := false
		for j, q := range blocked {
			if i != j && rectWithin(q.r, p.r) && !(p.r == q.r && j > i) {
				in = true
				break
			}
		}
		if !in {
			outer = append(outer, p)
		}
	}

	drop := make([]bool, len(els))
	at := map[int][]screen.Element{} // hidden elements, by the index they take
	for _, p := range outer {
		first := -1
		for i, e := range els {
			if i == p.idx || (!e.Bounds.Empty() && mostlyInside(e.Bounds, p.r)) {
				drop[i] = true
				if first < 0 {
					first = i
				}
			}
		}
		if first < 0 {
			continue
		}
		label := hiddenNotification
		if p.kind == partCard {
			label = hiddenCard
		}
		at[first] = append(at[first], screen.Element{Role: screen.RoleOther, Label: label, Bounds: p.r, Source: screen.SourceTree,
			Path: p.path, ID: p.id, Hidden: true})
	}
	if h.system {
		// A blocked app's name in recent apps whose card Pinky cannot
		// place.
		for i, e := range els {
			if drop[i] || e.Source == screen.SourceOCR || e.Label == "" || !inRecents(e) || !h.blockedName(e.Label) {
				continue
			}
			drop[i] = true
			at[i] = append(at[i], screen.Element{Role: screen.RoleOther, Label: hiddenCard, Source: screen.SourceTree,
				Path: e.Path, ID: e.ID, Hidden: true})
		}
	}
	if len(at) == 0 {
		return 0
	}
	out := make([]screen.Element, 0, len(els))
	n := 0
	for i, e := range els {
		out = append(out, at[i]...)
		n += len(at[i])
		if !drop[i] {
			out = append(out, e)
		}
	}
	s.Elements = out
	return n
}

// partBlocked reports whether a part is a blocked app's: by the name that
// starts it, the app name in a notification's header, or, for a
// notification, what it says.
func partBlocked(els []screen.Element, p part, h hideHints) bool {
	if app, _ := screen.NotificationApp(p.id); app != "" && h.blockedApp != nil && h.blockedApp(app) {
		return true
	}
	names := []string{p.label, firstPart(p.label)}
	var text []string
	if p.kind == partNotification {
		text = append(text, p.label)
		for i, e := range els {
			if i == p.idx || e.Bounds.Empty() || !mostlyInside(e.Bounds, p.r) {
				continue
			}
			text = append(text, e.Label)
			if strings.HasSuffix(e.ID, "id/app_name_text") {
				names = append(names, e.Label)
			}
		}
	}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" && h.blockedName(n) {
			return true
		}
	}
	if p.kind != partNotification || len(h.texts) == 0 {
		return false
	}
	all := normText(strings.Join(text, " "))
	for _, t := range h.texts {
		if strings.Contains(all, t) {
			return true
		}
	}
	return false
}

// gatherRows gathers the elements of notification rows that came apart
// (loose, indices into els) into rows: elements whose places follow each
// other closely down the screen. A row spans the width of all of them.
func gatherRows(els []screen.Element, loose []int, height int) []part {
	if len(loose) == 0 {
		return nil
	}
	sort.SliceStable(loose, func(a, b int) bool { return els[loose[a]].Bounds.Y < els[loose[b]].Bounds.Y })
	gap := max(8, height/100)
	left, right := els[loose[0]].Bounds.X, 0
	for _, i := range loose {
		b := els[i].Bounds
		left, right = min(left, b.X), max(right, b.X+b.W)
	}
	var rows []part
	var top, bottom, first int
	flush := func() {
		rows = append(rows, part{kind: partNotification, r: screen.Rect{X: left, Y: top, W: right - left, H: bottom - top},
			idx: -1, path: els[first].Path})
	}
	for n, i := range loose {
		b := els[i].Bounds
		switch {
		case n == 0:
			top, bottom, first = b.Y, b.Y+b.H, i
		case b.Y <= bottom+gap:
			bottom = max(bottom, b.Y+b.H)
		default:
			flush()
			top, bottom, first = b.Y, b.Y+b.H, i
		}
	}
	flush()
	return rows
}

// isRowID reports whether an element's id is a notification row's: the
// Android system UI's, or one a driver gave a notification over the app
// (screen.NotificationID).
func isRowID(id string) bool {
	if id == "" {
		return false
	}
	if _, ok := screen.NotificationApp(id); ok {
		return true
	}
	return strings.HasSuffix(strings.ToLower(id), "id/expandablenotificationrow")
}

// inRowPath reports whether an element is inside a notification row.
func inRowPath(path string) bool {
	return path != "" && strings.Contains(strings.ToLower(path), "#expandablenotificationrow")
}

// inRecents reports whether an element's id or ancestry says it is part of
// recent apps. A taskbar's icons are not.
func inRecents(e screen.Element) bool {
	s := strings.ToLower(e.ID + " " + e.Path)
	for _, w := range []string{"task_view", "taskview", "task_thumbnail", "recent", "overview", "snapshot", "switcher"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// firstPart is what a merged label says first: the app's name in a
// notification row's header or an iPhone's notification.
// The companion app joins a row's texts with ", ", uiautomator's merging
// with " · ", and an iPhone with ", ".
func firstPart(label string) string {
	end := -1
	for _, sep := range []string{",", " · ", "，"} {
		if i := strings.Index(label, sep); i >= 0 && (end < 0 || i < end) {
			end = i
		}
	}
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(label[:end])
}

func insideAny(parts []part, r screen.Rect) bool {
	for _, p := range parts {
		if mostlyInside(r, p.r) {
			return true
		}
	}
	return false
}

// mostlyInside reports whether at least four fifths of r lie inside box.
func mostlyInside(r, box screen.Rect) bool {
	if r.Empty() || box.Empty() {
		return false
	}
	x0, y0 := max(r.X, box.X), max(r.Y, box.Y)
	x1, y1 := min(r.X+r.W, box.X+box.W), min(r.Y+r.H, box.Y+box.H)
	if x1 <= x0 || y1 <= y0 {
		return false
	}
	return 5*(x1-x0)*(y1-y0) >= 4*r.W*r.H
}

func rectWithin(box, r screen.Rect) bool {
	if r.Empty() {
		return false // a part without a place is hidden on its own
	}
	return r.X >= box.X && r.Y >= box.Y && r.X+r.W <= box.X+box.W && r.Y+r.H <= box.Y+box.H
}

// Texts of notifications shorter than this many letters are not matched
// against rows, which would hide too much; two will do for Chinese,
// Japanese or Korean. Longer ones are matched by their start, as rows
// may cut them.
const (
	minNoteText  = 4
	minNoteWide  = 2
	noteTextHead = 40
)

// normText prepares text for matching: lower case, one space between
// words, at most noteTextHead letters; "" when too short to match.
func normText(s string) string {
	s = strings.Join(strings.Fields(strings.ToLower(s)), " ")
	n := utf8.RuneCountInString(s)
	wide := false
	for _, r := range s {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			wide = true
			break
		}
	}
	if n < minNoteText && !(wide && n >= minNoteWide) {
		return ""
	}
	return s
}

// noteTexts are the texts of a notification to look for in rows.
func noteTexts(texts ...string) []string {
	var out []string
	for _, t := range texts {
		if t = normText(t); t == "" {
			continue
		}
		if r := []rune(t); len(r) > noteTextHead {
			t = string(r[:noteTextHead])
		}
		out = append(out, t)
	}
	return out
}

// eventTexts splits a notification event's text into what the row shows
// apart: Android's events are "Title: Text".
func eventTexts(text string) []string {
	parts := []string{text}
	if a, b, ok := strings.Cut(text, ": "); ok {
		parts = append(parts, a, b)
	}
	return noteTexts(parts...)
}

// hideState is what a device keeps for hideBlocked: the phone's apps, its
// list of notifications, and the notifications it showed of blocked apps.
type hideState struct {
	mu sync.Mutex

	apps       []driver.App
	appsAt     time.Time
	refreshing bool
	// names and byName index apps; nil when apps changed since.
	names  map[string]string       // id -> display name
	byName map[string][]driver.App // lower-case name or alias -> apps

	notes   []driver.Notification
	notesAt time.Time

	seen []seenNote
}

type seenNote struct {
	texts []string
	at    time.Time
}

const (
	hideAppsMaxAge  = 2 * time.Minute
	hideAppsTimeout = 3 * time.Second
	hideNotesMaxAge = 2 * time.Second
	hideNotesWait   = 2 * time.Second
	hideSeenMax     = 50
	hideSeenMaxAge  = 24 * time.Hour
)

// hideStateOf returns the device's hideState.
func (g *Gateway) hideStateOf(d *device) *hideState {
	g.mu.Lock()
	defer g.mu.Unlock()
	if d.hide == nil {
		d.hide = &hideState{}
	}
	return d.hide
}

// appIndex returns the display name of each app id and whether a display
// name is a blocked app's, from the phone's apps, listed again in the
// background when the list is old. The first call waits for the list.
func (g *Gateway) appIndex(ctx context.Context, hs *hideState, dev driver.Device) (name func(string) string, blockedName func(string) bool) {
	hs.mu.Lock()
	have := hs.apps != nil
	stale := !have || time.Since(hs.appsAt) > hideAppsMaxAge
	start := stale && !hs.refreshing
	if start {
		hs.refreshing = true
	}
	hs.mu.Unlock()
	if start {
		load := func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, hideAppsTimeout)
			defer cancel()
			apps, err := dev.Apps(ctx)
			hs.mu.Lock()
			defer hs.mu.Unlock()
			hs.refreshing = false
			if err != nil {
				if hs.apps == nil {
					hs.appsAt = time.Now() // try again later, not on every read
					hs.apps = []driver.App{}
				}
				return
			}
			hs.apps, hs.appsAt, hs.names = apps, time.Now(), nil
		}
		if have {
			go load(context.WithoutCancel(ctx))
		} else {
			load(ctx)
		}
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.names == nil {
		hs.names, hs.byName = map[string]string{}, map[string][]driver.App{}
		for _, a := range hs.apps {
			hs.names[a.ID] = a.Name
			for _, n := range append([]string{a.Name}, a.Aliases...) {
				if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
					hs.byName[n] = append(hs.byName[n], a)
				}
			}
		}
	}
	// The maps are replaced, never changed, once built.
	names, byName := hs.names, hs.byName
	name = func(id string) string { return names[id] }
	// The policy is asked each time: the person may block an app any time.
	blockedName = func(n string) bool {
		n = strings.TrimSpace(n)
		if n == "" {
			return false
		}
		if _, b := g.policy.Blocked(screen.App{Name: n}); b {
			return true
		}
		for _, a := range byName[strings.ToLower(n)] {
			a = a.Original() // a clone's icon is hidden with its original's
			if _, b := g.policy.Blocked(screen.App{ID: a.ID, Name: a.Name}); b {
				return true
			}
		}
		return false
	}
	return name, blockedName
}

// notifications returns the phone's notifications, read again when the
// last list is a moment old; nil when the driver cannot list them.
func (hs *hideState) notifications(ctx context.Context, dev driver.Device) []driver.Notification {
	nl, ok := dev.(driver.NotificationLister)
	if !ok {
		return nil
	}
	hs.mu.Lock()
	if hs.notes != nil && time.Since(hs.notesAt) < hideNotesMaxAge {
		defer hs.mu.Unlock()
		return hs.notes
	}
	hs.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, hideNotesWait)
	defer cancel()
	list, err := nl.Notifications(ctx)
	if err != nil {
		return nil
	}
	if list == nil {
		list = []driver.Notification{}
	}
	hs.mu.Lock()
	hs.notes, hs.notesAt = list, time.Now()
	hs.mu.Unlock()
	return list
}

// remember keeps what a blocked app's notification said.
func (hs *hideState) remember(texts []string) {
	if len(texts) == 0 {
		return
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.seen = append(hs.seen, seenNote{texts: texts, at: time.Now()})
	if n := len(hs.seen); n > hideSeenMax {
		hs.seen = append([]seenNote(nil), hs.seen[n-hideSeenMax:]...)
	}
}

// seenTexts returns what the blocked apps' notifications it remembers said.
func (hs *hideState) seenTexts() []string {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	var out []string
	for _, n := range hs.seen {
		if time.Since(n.at) < hideSeenMaxAge {
			out = append(out, n.texts...)
		}
	}
	return out
}

// hideBlocked hides blocked apps' notification rows and cards on a fresh
// read (see hideParts), and returns the display names of app ids, for
// filtering the read's notification events (policy.FilterEventsNamed).
func (g *Gateway) hideBlocked(ctx context.Context, d *device, dev driver.Device, s *screen.Screen) (name func(string) string) {
	notes := false
	for _, ev := range s.Events {
		if ev.Kind == screen.EventNotification {
			notes = true
			break
		}
	}
	rows := false
	for _, e := range s.Elements {
		if isRowID(e.ID) || inRowPath(e.Path) {
			rows = true
			break
		}
	}
	system := s.Locked || g.policy.IsSystem(s.App)
	if !notes && !rows && !(system && mayHideCard(s)) {
		return nil
	}
	hs := g.hideStateOf(d)
	name, blockedName := g.appIndex(ctx, hs, dev)
	for _, ev := range s.Events {
		if ev.Kind == screen.EventNotification && g.policy.NotificationBlocked(ev, name) {
			hs.remember(eventTexts(ev.Text))
		}
	}
	h := hideHints{system: system, blockedName: blockedName, blockedApp: func(id string) bool {
		_, b := g.policy.Blocked(screen.App{ID: id, Name: name(id)})
		return b
	}}
	if rows {
		h.texts = hs.seenTexts()
		for _, n := range hs.notifications(ctx, dev) {
			if _, b := g.policy.Blocked(screen.App{ID: n.App, Name: name(n.App)}); b {
				h.texts = append(h.texts, noteTexts(n.Texts...)...)
			}
		}
	}
	hideParts(s, h)
	return name
}

// mayHideCard reports whether a system screen has an element that could
// be a blocked app's card: a large one with a label, or one in recent
// apps.
func mayHideCard(s *screen.Screen) bool {
	minArea := s.Width * s.Height / 50
	for _, e := range s.Elements {
		if e.Source != screen.SourceOCR && e.Label != "" && (e.Bounds.W*e.Bounds.H >= minArea || inRecents(e)) {
			return true
		}
	}
	return false
}

// hiddenPlaces returns where reads hid blocked apps' parts, in the pixels
// of a w x h picture of the screen, and whether one had no place.
func hiddenPlaces(w, h int, reads ...*screen.Screen) (boxes []image.Rectangle, unplaced bool) {
	for _, s := range reads {
		if s == nil {
			continue
		}
		for _, e := range s.Elements {
			if !e.Hidden {
				continue
			}
			if e.Bounds.Empty() || s.Width <= 0 || s.Height <= 0 {
				unplaced = true
				continue
			}
			if r := scaleRect(e.Bounds, s.Width, s.Height, w, h); !r.Empty() {
				boxes = append(boxes, r)
			}
		}
	}
	return boxes, unplaced
}

// scaleRect turns bounds on a sw x sh screen into a box on a w x h picture
// of it, rounded outwards.
func scaleRect(r screen.Rect, sw, sh, w, h int) image.Rectangle {
	x0, y0 := r.X*w/sw, r.Y*h/sh
	x1, y1 := ((r.X+r.W)*w+sw-1)/sw, ((r.Y+r.H)*h+sh-1)/sh
	return image.Rect(x0, y0, x1, y1).Intersect(image.Rect(0, 0, w, h))
}

// coverHidden covers in a picture what reads of the screen hid. It
// returns the picture (a copy when anything is covered) and how many boxes
// it covered, or errHiddenShot when a hidden part had no place.
func coverHidden(img image.Image, reads ...*screen.Screen) (image.Image, int, error) {
	if img == nil {
		return nil, 0, nil
	}
	b := img.Bounds()
	boxes, unplaced := hiddenPlaces(b.Dx(), b.Dy(), reads...)
	if unplaced {
		return nil, 0, errHiddenShot
	}
	if len(boxes) == 0 {
		return img, 0, nil
	}
	return coverBoxes(img, boxes), len(boxes), nil
}
