package netmux

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver/android/mdns"
	goios "github.com/danielpaulus/go-ios/ios"
)

// ServiceType is the service an iPhone or iPad advertises on the network
// for a computer it paired with while Wi-Fi connections are on for it.
const ServiceType = "_apple-mobdev2._tcp"

// LockdownPort is lockdown's port on the phone.
const LockdownPort = 62078

// Source is a search for DNS-SD services: an *mdns.Browser.
type Source interface {
	Services() []mdns.Service
	Err() error
	Close() error
}

// PairInfo is what the Finder needs of a phone's pair record.
type PairInfo struct {
	HostID  string // the computer's id for the phone, which keys the TXT record's tags
	WiFiMAC string // the phone's Wi-Fi address: "08:87:c7:2a:8a:db"
}

// Finder looks for the phones this computer paired with on the network and
// gives the Mux those that answer.
//
// A phone with Wi-Fi connections on advertises ServiceType for each
// computer it paired with. From iOS 26.4 the instance's name holds a
// private Wi-Fi address, and the TXT record says which computer the
// record is for: an identifier and tags, each the first 8 bytes of
// HMAC-SHA256(HKDF-SHA512(HostID), identifier), base64 (as Apple's
// AMDIsTXTRecordForUDID reads them; netmuxd matches the same way). Older
// phones name the instance after their Wi-Fi address, which the pair
// record holds.
//
// The records are no proof either way: they live long (the PTR record 75
// minutes), so one stays after the phone left or went to sleep, and one
// can lack its TXT strings or address for a while though the phone is
// there. So once a record matched, the Finder remembers its name and the
// phone's address, and lockdown decides: it lists a phone while lockdown
// on it takes a connection, and drops it once lockdown has not answered for
// dropAfter from the first time it did not. A phone dropped at once took
// Pinky's session on it down with it. Lockdown is asked at every look while a phone is new or has just
// stopped answering, and less often, up to every probeSteady, while it
// keeps answering or keeps not answering; the search's questions back off
// too (mdns.Options.MaxEvery), since every phone on the network answers
// them.
type Finder struct {
	Mux   *Mux
	UDIDs func() []string // the phones this computer paired with
	Log   *slog.Logger

	// Interval is how often the Finder looks; 3 s when zero.
	Interval time.Duration

	// For tests.
	BrowseFn func() (Source, error)
	PairFn   func(udid string) (PairInfo, error)
	ProbeFn  func(ctx context.Context, addr netip.Addr) bool
	NowFn    func() time.Time
}

const (
	// dropAfter is how long lockdown on a listed phone may not answer
	// before the phone is dropped: longer than the computer's Wi-Fi drops
	// for while it renews its address (8 s and 36 s on arch on a hotel
	// Wi-Fi), so that one does not end Pinky's session on the phone.
	dropAfter = 45 * time.Second
	// pairRetry is how soon a pair record that could not be read is read
	// again.
	pairRetry = time.Minute
	// browseRetry is how soon a search that could not start is started
	// again.
	browseRetry = time.Minute
	// probeTimeout bounds one lockdown connection.
	probeTimeout = 2 * time.Second
	// probeSteady is the longest lockdown on a phone goes unasked: the wait
	// doubles from the Finder's Interval while the phone keeps answering,
	// or keeps not answering. Well below dropAfter, so that a listed phone
	// that left is asked again, at every look, before it is dropped.
	probeSteady = 30 * time.Second
	// askEvery is the longest the search goes between rounds of questions
	// (mdns.Options.MaxEvery); a phone that joins the network announces
	// itself anyway.
	askEvery = time.Minute
)

// Run looks for phones until ctx ends, and then lists none.
func (f *Finder) Run(ctx context.Context) {
	log := f.logOrDiscard()
	interval := f.interval()
	s := newFindState(f, log)
	defer func() {
		f.Mux.SetPhones(nil)
		if s.src != nil {
			s.src.Close()
		}
	}()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (f *Finder) interval() time.Duration {
	if f.Interval <= 0 {
		return 3 * time.Second
	}
	return f.Interval
}

func newFindState(f *Finder, log *slog.Logger) *findState {
	return &findState{f: f, log: log, pairs: map[string]pairEntry{}, failSince: map[string]time.Time{}, names: map[string]string{},
		listed: map[string]netip.Addr{}, seen: map[string]bool{}, probes: map[string]probeState{}}
}

func (f *Finder) logOrDiscard() *slog.Logger {
	if f.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return f.Log
}

type findState struct {
	f        *Finder
	log      *slog.Logger
	src      Source
	srcErrAt time.Time
	pairs    map[string]pairEntry
	// failSince is when lockdown on a listed phone first did not answer,
	// by UDID, until it answers again.
	failSince map[string]time.Time
	names     map[string]string // instance name to UDID, once matched
	listed    map[string]netip.Addr
	seen      map[string]bool       // found before
	probes    map[string]probeState // by UDID: when lockdown is next asked
}

// probeState is when lockdown on a phone is asked next, and what it said
// last.
type probeState struct {
	addr  netip.Addr
	ok    bool
	every time.Duration // the wait after the last time
	next  time.Time
}

type pairEntry struct {
	info   PairInfo
	failed time.Time // zero once read
}

func (s *findState) now() time.Time {
	if s.f.NowFn != nil {
		return s.f.NowFn()
	}
	return time.Now()
}

func (s *findState) tick(ctx context.Context) {
	now := s.now()
	if s.src == nil {
		if !s.srcErrAt.IsZero() && now.Sub(s.srcErrAt) < browseRetry {
			return
		}
		browse := s.f.BrowseFn
		if browse == nil {
			browse = func() (Source, error) {
				return mdns.Browse([]string{ServiceType}, mdns.Options{MulticastAnswers: true, MaxEvery: askEvery})
			}
		}
		src, err := browse()
		if err != nil {
			s.srcErrAt = now
			s.log.Warn("iphone: cannot look for iPhones and iPads on the network", "err", err)
			return
		}
		s.src = src
	}

	// The pair records of the phones this computer paired with.
	read := s.f.PairFn
	if read == nil {
		read = s.f.Mux.readPair
	}
	var udids []string
	if s.f.UDIDs != nil {
		udids = s.f.UDIDs()
	}
	known := map[string]PairInfo{}
	for _, id := range udids {
		e, ok := s.pairs[id]
		if !ok || (!e.failed.IsZero() && now.Sub(e.failed) >= pairRetry) {
			info, err := read(id)
			if err != nil || (info.HostID == "" && info.WiFiMAC == "") {
				e = pairEntry{failed: now}
				s.log.Debug("netmux: cannot read the pair record", "device", id, "err", err)
			} else {
				e = pairEntry{info: info}
			}
			s.pairs[id] = e
		}
		if e.failed.IsZero() {
			known[id] = e.info
		}
	}

	// The phones on the network, and whether they answer: those whose
	// record matched, now or before, and those listed.
	addrs := map[string][]netip.Addr{}
	for _, svc := range s.src.Services() {
		id := Match(svc, known)
		if id != "" {
			s.names[svc.Instance] = id
		} else if id = s.names[svc.Instance]; id != "" {
			s.log.Debug("netmux: a record without its TXT strings; matched by its name before", "device", id)
		}
		if id == "" {
			continue
		}
		addrs[id] = append(addrs[id], ipv4s(svc)...)
	}
	found := map[string]netip.Addr{}
	for id, list := range addrs {
		if addr, ok := pickAddr(list, s.listed[id]); ok {
			found[id] = addr
		}
	}
	for id, addr := range s.listed {
		if _, ok := found[id]; !ok {
			s.log.Debug("netmux: a listed phone's record or address is missing; asking lockdown at its last address", "device", id)
			found[id] = addr
		}
	}
	probe := s.f.ProbeFn
	if probe == nil {
		probe = probeLockdown
	}
	// Lockdown is asked about the phones whose turn it is, or whose address
	// changed; for the others its last answer stands.
	answered := map[string]bool{}
	probed := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for id, addr := range found {
		if ps, ok := s.probes[id]; ok && ps.addr == addr && now.Before(ps.next) {
			answered[id] = ps.ok
			continue
		}
		probed[id] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok := probe(ctx, addr)
			mu.Lock()
			answered[id] = ok
			mu.Unlock()
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	for id := range probed {
		s.probes[id] = s.nextProbe(now, id, found[id], answered[id])
	}
	for id := range s.probes {
		if _, ok := found[id]; !ok {
			delete(s.probes, id)
		}
	}

	next := map[string]netip.Addr{}
	for id, addr := range found {
		if answered[id] {
			delete(s.failSince, id)
			next[id] = addr
			continue
		}
		// Counted from the first time it did not answer, not from its last
		// answer, which may be probeSteady older.
		if _, ok := s.listed[id]; ok {
			since, failing := s.failSince[id]
			if !failing {
				since = now
				s.failSince[id] = now
			}
			if now.Sub(since) < dropAfter {
				s.log.Debug("netmux: lockdown did not answer; still listed", "device", id, "since", now.Sub(since).Round(time.Second))
				next[id] = s.listed[id]
			}
		}
	}
	// A phone that sleeps comes and goes every minute or so: only the
	// first time is worth a line.
	for id, addr := range next {
		if old, ok := s.listed[id]; !ok || old != addr {
			if !s.seen[id] {
				s.seen[id] = true
				s.log.Info("iphone: found on Wi-Fi", "device", id, "addr", addr)
			} else {
				s.log.Debug("iphone: back on Wi-Fi", "device", id, "addr", addr)
			}
		}
	}
	for id := range s.listed {
		if _, ok := next[id]; !ok {
			s.log.Debug("iphone: no longer on Wi-Fi", "device", id)
			delete(s.failSince, id)
			// Asked again at the next look, should it answer again.
			delete(s.probes, id)
		}
	}
	s.listed = next
	var list []Phone
	for id, addr := range next {
		list = append(list, Phone{UDID: id, Addr: addr})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UDID < list[j].UDID })
	s.f.Mux.SetPhones(list)
}

// nextProbe says when lockdown on a phone is asked again after it answered
// ok, or not: at the next look while the phone is new or a listed one has
// just stopped answering, and twice as long each time, up to probeSteady,
// while it keeps answering or keeps not answering.
func (s *findState) nextProbe(now time.Time, id string, addr netip.Addr, ok bool) probeState {
	interval := s.f.interval()
	last, had := s.probes[id]
	every := interval
	_, listed := s.listed[id]
	switch {
	case !had || last.addr != addr || last.ok != ok:
		// New, moved, or it changed its answer.
	case !ok && listed:
		// Just stopped answering: asked at every look until it is dropped.
	default:
		every = min(2*last.every, probeSteady)
	}
	return probeState{addr: addr, ok: ok, every: every, next: now.Add(every)}
}

// ipv4s returns the service's IPv4 addresses. Link-local IPv6 addresses
// need an interface to dial, which usbmuxd's clients do not give.
func ipv4s(s mdns.Service) []netip.Addr {
	var out []netip.Addr
	for _, a := range s.Addrs {
		if a.Is4() {
			out = append(out, a)
		}
	}
	return out
}

// phoneLink is the network an iPhone makes with a computer on its cable
// (and for its Personal Hotspot), where the phone is 172.20.10.1. On a
// cable the phone answers the search there too, and lockdown answers there,
// but it is not Wi-Fi and it goes with the cable: with both, the phone's
// address flipped between the two every few seconds, a new attach each
// time (Windows, "Apple Mobile Device Ethernet", 2026-11-09).
var phoneLink = netip.MustParsePrefix("172.20.10.0/28")

// pickAddr picks the address a phone is listed at among those it answered
// with: one outside phoneLink when there is one, and the one it is listed
// at (last) while that is still among the best, so it does not move.
func pickAddr(addrs []netip.Addr, last netip.Addr) (netip.Addr, bool) {
	var best []netip.Addr
	for _, a := range addrs {
		if !phoneLink.Contains(a) {
			best = append(best, a)
		}
	}
	if len(best) == 0 && last.IsValid() && !phoneLink.Contains(last) {
		// Its record on Wi-Fi is missing for a moment: lockdown at the
		// last address decides, as for a record that is missing.
		return last, true
	}
	if len(best) == 0 {
		best = addrs
	}
	if len(best) == 0 {
		return netip.Addr{}, false
	}
	if slices.Contains(best, last) {
		return last, true
	}
	slices.SortFunc(best, netip.Addr.Compare)
	return best[0], true
}

// Match returns the UDID of the phone that advertises svc for this
// computer, among the phones in pairs, or "".
func Match(svc mdns.Service, pairs map[string]PairInfo) string {
	ids := make([]string, 0, len(pairs))
	for id := range pairs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var identifier string
	var tags [][8]byte
	for _, kv := range svc.TXT {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch {
		case k == "identifier":
			identifier = v
		case k == "authTag" || strings.HasPrefix(k, "authTag#"):
			if t, ok := decodeTag(v); ok {
				tags = append(tags, t)
			}
		}
	}
	if identifier != "" && len(tags) > 0 {
		for _, id := range ids {
			if h := pairs[id].HostID; h != "" && slices.Contains(tags, AuthTag(h, identifier)) {
				return id
			}
		}
	}
	if mac, _, ok := strings.Cut(svc.Instance, "@"); ok {
		for _, id := range ids {
			if m := pairs[id].WiFiMAC; m != "" && strings.EqualFold(m, mac) {
				return id
			}
		}
	}
	return ""
}

// AuthTag is the tag a phone paired with the computer whose id is hostID
// gives in its TXT record for identifier.
func AuthTag(hostID, identifier string) [8]byte {
	key, _ := hkdf.Key(sha512.New, []byte(hostID), nil, "", 32) // 32 bytes is always a valid length
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(identifier))
	var out [8]byte
	copy(out[:], mac.Sum(nil))
	return out
}

func decodeTag(v string) ([8]byte, bool) {
	var out [8]byte
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
	if err != nil || len(b) != len(out) {
		return out, false
	}
	copy(out[:], b)
	return out, true
}

// probeLockdown reports whether lockdown on the phone takes a connection.
func probeLockdown(ctx context.Context, addr netip.Addr) bool {
	d := net.Dialer{Timeout: probeTimeout}
	c, err := d.DialContext(ctx, "tcp", netip.AddrPortFrom(addr, LockdownPort).String())
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// readPair reads a phone's pair record from the upstream usbmuxd, which
// keeps them also for phones that are not attached, or from PairDir while
// it is not running.
func (m *Mux) readPair(udid string) (PairInfo, error) {
	r, err := m.upstreamPair(udid)
	if err != nil {
		data, ferr := m.pairFile(udid)
		if ferr != nil {
			return PairInfo{}, err
		}
		r = goios.PairRecordfromBytes(data)
	}
	return PairInfo{HostID: r.HostID, WiFiMAC: r.WiFiMACAddress}, nil
}

func (m *Mux) upstreamPair(udid string) (goios.PairRecord, error) {
	dc, err := goios.NewDeviceConnection(m.upstream)
	if err != nil {
		return goios.PairRecord{}, err
	}
	defer dc.Close()
	return goios.NewUsbMuxConnection(dc).ReadPair(udid)
}

// PairedUDIDs returns the UDIDs of the pair records in dir, where
// libimobiledevice's usbmuxd keeps them (/var/lib/lockdown), or none when
// dir cannot be read.
func PairedUDIDs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".plist")
		if !ok || e.IsDir() || name == "SystemConfiguration" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// LockdownDir is where the system's usbmuxd keeps pair records:
// libimobiledevice's on Linux, Apple's (Apple Devices, iTunes) on Windows.
var LockdownDir = lockdownDir()

func lockdownDir() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "Apple", "Lockdown")
	}
	return filepath.FromSlash("/var/lib/lockdown")
}
