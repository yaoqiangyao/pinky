package mdns

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	connectType = "_adb-tls-connect._tcp"
	pairingType = "_adb-tls-pairing._tcp"
)

// rr is a record for a test response.
type rr struct {
	name  string
	typ   dnsmessage.Type
	ttl   uint32
	flush bool
	data  string // PTR target, "target:port" for SRV, an address for A and AAAA
}

// response builds an mDNS response with the answers and additional
// records, compressed and with an NSEC and a TXT record among them, as
// Android sends them.
func response(t *testing.T, answers, extra []rr) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	b.EnableCompression()
	add := func(r rr) {
		cl := dnsmessage.ClassINET
		if r.flush {
			cl |= unicastBit
		}
		h := dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(r.name), Class: cl, TTL: r.ttl}
		var err error
		switch r.typ {
		case dnsmessage.TypePTR:
			err = b.PTRResource(h, dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(r.data)})
		case dnsmessage.TypeSRV:
			target, port, _ := strings.Cut(r.data, ":")
			var p uint16
			fmt.Sscan(port, &p)
			err = b.SRVResource(h, dnsmessage.SRVResource{Target: dnsmessage.MustNewName(target), Port: p})
		case dnsmessage.TypeA:
			err = b.AResource(h, dnsmessage.AResource{A: netip.MustParseAddr(r.data).As4()})
		case dnsmessage.TypeAAAA:
			err = b.AAAAResource(h, dnsmessage.AAAAResource{AAAA: netip.MustParseAddr(r.data).As16()})
		case dnsmessage.TypeTXT:
			err = b.TXTResource(h, dnsmessage.TXTResource{TXT: []string{r.data}})
		default:
			// An NSEC record: the next name and a type bitmap.
			h.Type = 47
			err = b.UnknownResource(h, dnsmessage.UnknownResource{Type: 47, Data: []byte{0xc0, 0x0c, 0, 1, 0x40}})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := b.StartAnswers(); err != nil {
		t.Fatal(err)
	}
	for _, r := range answers {
		add(r)
	}
	if err := b.StartAdditionals(); err != nil {
		t.Fatal(err)
	}
	for _, r := range extra {
		add(r)
	}
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

// miResponse is what the Mi 10 Pro answers for its connect service: the
// PTR record, then its TXT, AAAA, NSEC, A, NSEC and SRV records.
func miResponse(t *testing.T, ttl uint32) []byte {
	inst := "adb-3e8be48e-kIY2HE._adb-tls-connect._tcp.local."
	return response(t,
		[]rr{{name: connectType + ".local.", typ: dnsmessage.TypePTR, ttl: 4500, data: inst}},
		[]rr{
			{name: inst, typ: dnsmessage.TypeTXT, ttl: 4500, flush: true, data: ""},
			{name: "Android_X32DXQ4S.local.", typ: dnsmessage.TypeAAAA, ttl: ttl, flush: true, data: "fe80::334a:20d0:55c3:8bac"},
			{name: inst, typ: 47, ttl: ttl, flush: true},
			{name: "Android_X32DXQ4S.local.", typ: dnsmessage.TypeA, ttl: ttl, flush: true, data: "192.168.5.137"},
			{name: "Android_X32DXQ4S.local.", typ: 47, ttl: ttl, flush: true},
			{name: inst, typ: dnsmessage.TypeSRV, ttl: ttl, flush: true, data: "Android_X32DXQ4S.local.:38161"},
		})
}

func newCache() *cache {
	c := &cache{}
	c.init()
	return c
}

func addMsg(t *testing.T, c *cache, now time.Time, types []string, msg []byte) bool {
	t.Helper()
	recs, ok := parse(msg)
	if !ok {
		t.Fatal("the response did not parse")
	}
	return c.add(now, types, recs)
}

func TestParseAndCache(t *testing.T) {
	c := newCache()
	now := time.Now()
	types := []string{connectType, pairingType}
	if unresolved := addMsg(t, c, now, types, miResponse(t, 120)); unresolved {
		t.Error("a complete answer left something to ask for")
	}
	svcs := c.services(now, types)
	if len(svcs) != 1 {
		t.Fatalf("services = %v", svcs)
	}
	s := svcs[0]
	if s.Instance != "adb-3e8be48e-kIY2HE" || s.Type != connectType || s.Port != 38161 ||
		s.Host != "Android_X32DXQ4S.local." || s.Addr() != "192.168.5.137:38161" {
		t.Errorf("service = %+v, addr %q", s, s.Addr())
	}
	if len(s.Addrs) != 2 || !s.Addrs[0].Is4() {
		t.Errorf("addresses = %v, want IPv4 first", s.Addrs)
	}
	// The records expire with their TTLs: the SRV and address after two
	// minutes, the PTR record after 75.
	later := now.Add(121 * time.Second)
	if svcs := c.services(later, types); len(svcs) != 1 || svcs[0].Addr() != "" {
		t.Errorf("after the SRV expired: %v", svcs)
	}
	srv, hosts := c.missing(later, types)
	if len(srv) != 1 || srv[0] != "adb-3e8be48e-kIY2HE._adb-tls-connect._tcp.local." || len(hosts) != 0 {
		t.Errorf("missing = %q, %q", srv, hosts)
	}
	if svcs := c.services(now.Add(4501*time.Second), types); len(svcs) != 0 {
		t.Errorf("after everything expired: %v", svcs)
	}
}

func TestGoodbye(t *testing.T) {
	c := newCache()
	now := time.Now()
	types := []string{pairingType}
	inst := "pinky-SShhWu4UcC._adb-tls-pairing._tcp.local."
	addMsg(t, c, now, types, response(t,
		[]rr{{name: pairingType + ".local.", typ: dnsmessage.TypePTR, ttl: 4500, data: inst}},
		[]rr{
			{name: inst, typ: dnsmessage.TypeSRV, ttl: 120, flush: true, data: "Android.local.:41997"},
			{name: "Android.local.", typ: dnsmessage.TypeA, ttl: 120, flush: true, data: "192.168.5.137"},
		}))
	if svcs := c.services(now, types); len(svcs) != 1 || svcs[0].Addr() != "192.168.5.137:41997" || svcs[0].Instance != "pinky-SShhWu4UcC" {
		t.Fatalf("services = %v", svcs)
	}
	// The phone closes the pairing dialog: a goodbye, TTL 0.
	addMsg(t, c, now.Add(time.Second), types, response(t,
		[]rr{{name: pairingType + ".local.", typ: dnsmessage.TypePTR, ttl: 0, data: inst}}, nil))
	if svcs := c.services(now.Add(time.Second), types); len(svcs) != 0 {
		t.Errorf("after the goodbye: %v", svcs)
	}
}

func TestOtherTypesAndQueries(t *testing.T) {
	c := newCache()
	now := time.Now()
	types := []string{connectType}
	// A query (from another computer) and another service's answer.
	q, err := query([]string{connectType}, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parse(q); ok {
		t.Error("a query parsed as a response")
	}
	addMsg(t, c, now, types, response(t,
		[]rr{{name: "_airplay._tcp.local.", typ: dnsmessage.TypePTR, ttl: 4500, data: "TV._airplay._tcp.local."}},
		[]rr{
			{name: "TV._airplay._tcp.local.", typ: dnsmessage.TypeSRV, ttl: 120, data: "tv.local.:7000"},
			{name: "tv.local.", typ: dnsmessage.TypeA, ttl: 120, data: "192.168.5.143"},
		}))
	if len(c.ptrs) != 0 || len(c.srvs) != 0 || len(c.addrs) != 0 {
		t.Errorf("kept another type's records: %v %v %v", c.ptrs, c.srvs, c.addrs)
	}
}

// TestCacheFlush: a phone that got a new address announces it with the
// cache-flush bit, and the old one goes.
func TestCacheFlush(t *testing.T) {
	c := newCache()
	now := time.Now()
	types := []string{connectType}
	addMsg(t, c, now, types, miResponse(t, 120))
	addMsg(t, c, now.Add(5*time.Second), types, response(t, nil, []rr{
		{name: "Android_X32DXQ4S.local.", typ: dnsmessage.TypeA, ttl: 120, flush: true, data: "192.168.5.200"},
	}))
	s := c.services(now.Add(5*time.Second), types)
	if len(s) != 1 || s[0].Addr() != "192.168.5.200:38161" {
		t.Fatalf("services = %v", s)
	}
	for _, a := range s[0].Addrs {
		if a.String() == "192.168.5.137" {
			t.Errorf("the flushed address stayed: %v", s[0].Addrs)
		}
	}
}

func TestQueryQuestions(t *testing.T) {
	msg, err := query([]string{connectType}, []string{"adb-x._adb-tls-connect._tcp.local."}, []string{"phone.local."}, true)
	if err != nil {
		t.Fatal(err)
	}
	var p dnsmessage.Parser
	if _, err := p.Start(msg); err != nil {
		t.Fatal(err)
	}
	qs, err := p.AllQuestions()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, q := range qs {
		if q.Class&unicastBit == 0 {
			t.Errorf("%v without the unicast-response bit", q)
		}
		got = append(got, q.Type.String()+" "+q.Name.String())
	}
	want := "TypePTR _adb-tls-connect._tcp.local.|TypeSRV adb-x._adb-tls-connect._tcp.local.|TypeA phone.local.|TypeAAAA phone.local."
	if strings.Join(got, "|") != want {
		t.Errorf("questions = %q", got)
	}
}

func TestSendErr(t *testing.T) {
	op := &net.OpError{Op: "write", Net: "udp", Err: syscall.EPERM}
	if !errors.Is(sendErr(op), ErrNotAllowed) {
		t.Errorf("EPERM: %v", sendErr(op))
	}
	if errors.Is(sendErr(&net.OpError{Op: "write", Err: syscall.ENETDOWN}), ErrNotAllowed) {
		t.Error("ENETDOWN counted as not allowed")
	}
	if sendErr(nil) != nil {
		t.Error("nil error")
	}
}

// fakeResponder answers mDNS questions sent to it over unicast, one record
// type at a time as some responders do, so the browser has to ask for the
// SRV and address records itself.
type fakeResponder struct {
	t    *testing.T
	conn net.PacketConn

	mu       sync.Mutex
	asked    []string
	messages int
	ptrOnly  bool // answers every question with the PTR record alone
}

func newResponder(t *testing.T) *fakeResponder {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeResponder{t: t, conn: conn}
	t.Cleanup(func() { conn.Close() })
	go f.serve()
	return f
}

func (f *fakeResponder) addr() netip.AddrPort {
	return f.conn.LocalAddr().(*net.UDPAddr).AddrPort()
}

func (f *fakeResponder) serve() {
	buf := make([]byte, 9000)
	inst := "adb-R5CN20ABC-x1y2z3._adb-tls-connect._tcp.local."
	for {
		n, src, err := f.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		var p dnsmessage.Parser
		if _, err := p.Start(buf[:n]); err != nil {
			continue
		}
		qs, _ := p.AllQuestions()
		f.mu.Lock()
		f.messages++
		ptrOnly := f.ptrOnly
		f.mu.Unlock()
		var answers []rr
		if ptrOnly && len(qs) > 0 {
			// A broken responder: the PTR record whatever it is asked.
			f.conn.WriteTo(response(f.t, []rr{{name: connectType + ".local.", typ: dnsmessage.TypePTR, ttl: 4500, data: inst}}, nil), src)
			continue
		}
		for _, q := range qs {
			f.mu.Lock()
			f.asked = append(f.asked, q.Type.String())
			f.mu.Unlock()
			switch {
			case q.Type == dnsmessage.TypePTR && q.Name.String() == connectType+".local.":
				answers = append(answers, rr{name: connectType + ".local.", typ: dnsmessage.TypePTR, ttl: 4500, data: inst})
			case q.Type == dnsmessage.TypeSRV && q.Name.String() == inst:
				answers = append(answers, rr{name: inst, typ: dnsmessage.TypeSRV, ttl: 120, flush: true, data: "Pixel-9.local.:41000"})
			case q.Type == dnsmessage.TypeA && q.Name.String() == "Pixel-9.local.":
				answers = append(answers, rr{name: "Pixel-9.local.", typ: dnsmessage.TypeA, ttl: 120, flush: true, data: "192.168.1.42"})
			}
		}
		if len(answers) > 0 {
			f.conn.WriteTo(response(f.t, answers, nil), src)
		}
	}
}

// TestBrowse runs a real browser against the fake responder over the
// loopback: it finds the instance, then asks for its SRV record and its
// host's address by itself.
func TestBrowse(t *testing.T) {
	f := newResponder(t)
	b, err := Browse([]string{connectType}, Options{Dest: f.addr()})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		svcs := b.Services()
		if len(svcs) == 1 && svcs[0].Addr() == "192.168.1.42:41000" {
			if svcs[0].Instance != "adb-R5CN20ABC-x1y2z3" {
				t.Errorf("instance = %q", svcs[0].Instance)
			}
			break
		}
		if time.Now().After(deadline) {
			f.mu.Lock()
			t.Fatalf("services = %v; asked %q", svcs, f.asked)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := b.Err(); err != nil {
		t.Errorf("Err = %v", err)
	}
	if err := b.Close(); err != nil {
		t.Error(err)
	}
}

// TestBrowseFollowUpsBounded: a responder that answers every question,
// the follow-ups for the SRV record too, with the PTR record alone is
// asked again at most about once a second, not in a loop.
func TestBrowseFollowUpsBounded(t *testing.T) {
	f := newResponder(t)
	f.mu.Lock()
	f.ptrOnly = true
	f.mu.Unlock()
	b, err := Browse([]string{connectType}, Options{Dest: f.addr()})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	b.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	// Rounds at 0, 1 and 2 s, and at most one follow-up a second.
	if f.messages > 7 {
		t.Errorf("%d messages in 3 s", f.messages)
	}
	if svcs := b.Services(); len(svcs) != 1 || svcs[0].Port != 0 {
		t.Errorf("services = %v", svcs)
	}
}

// TestSchedule: the quick schedule for searches that last seconds, and
// RFC 6762 §5.2's for one that runs for long: each wait twice the last, up
// to MaxEvery, from the start again after restart.
func TestSchedule(t *testing.T) {
	waits := func(s *schedule, n int) []time.Duration {
		var out []time.Duration
		for range n {
			out = append(out, s.next())
		}
		return out
	}
	s := schedule{}
	if got, want := waits(&s, 7), []time.Duration{time.Second, time.Second, 2 * time.Second, 2 * time.Second, 4 * time.Second, steadyEvery, steadyEvery}; !slices.Equal(got, want) {
		t.Errorf("quick: %v, want %v", got, want)
	}
	s = schedule{max: time.Minute}
	sec := time.Second
	if got, want := waits(&s, 9), []time.Duration{sec, 2 * sec, 4 * sec, 8 * sec, 16 * sec, 32 * sec, time.Minute, time.Minute, time.Minute}; !slices.Equal(got, want) {
		t.Errorf("backing off: %v, want %v", got, want)
	}
	s.restart()
	if got := waits(&s, 2); !slices.Equal(got, []time.Duration{sec, 2 * sec}) {
		t.Errorf("after restart: %v", got)
	}
}

// TestBrowseBacksOff: a long search asks once, then not again for a
// second, and at once when the computer's network addresses change.
func TestBrowseBacksOff(t *testing.T) {
	f := newResponder(t) // it answers nothing for this type: no follow-ups
	oldWatch, oldAddrs := netWatch, interfaceAddrs
	var mu sync.Mutex
	addr := "192.168.1.10/24"
	netWatch = 20 * time.Millisecond
	interfaceAddrs = func(*net.Interface) ([]net.Addr, error) {
		mu.Lock()
		defer mu.Unlock()
		_, n, _ := net.ParseCIDR(addr)
		return []net.Addr{n}, nil
	}
	defer func() { netWatch, interfaceAddrs = oldWatch, oldAddrs }()
	b, err := Browse([]string{"_other._tcp"}, Options{Dest: f.addr(), MaxEvery: time.Hour,
		Interfaces: func() ([]net.Interface, error) { return []net.Interface{{Index: 1, Name: "wlan0"}}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	messages := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.messages
	}
	time.Sleep(400 * time.Millisecond)
	if n := messages(); n != 1 {
		t.Fatalf("%d rounds in 0.4 s, want 1", n)
	}
	mu.Lock()
	addr = "10.0.0.7/24"
	mu.Unlock()
	time.Sleep(400 * time.Millisecond)
	if n := messages(); n != 2 {
		t.Errorf("%d rounds 0.4 s after the network changed, want 2", n)
	}
}

// TestBrowseNoNetwork reports a search without interfaces.
func TestBrowseNoNetwork(t *testing.T) {
	b, err := Browse([]string{connectType}, Options{NoGroup: true,
		Interfaces: func() ([]net.Interface, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	deadline := time.Now().Add(2 * time.Second)
	for !errors.Is(b.Err(), ErrNoNetwork) {
		if time.Now().After(deadline) {
			t.Fatalf("Err = %v", b.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServiceAddr(t *testing.T) {
	s := Service{Port: 5555, Addrs: []netip.Addr{netip.MustParseAddr("fe80::1")}}
	if s.Addr() != "" {
		t.Errorf("a link-local address: %q", s.Addr())
	}
	s.Addrs = append(s.Addrs, netip.MustParseAddr("2001:db8::7"))
	if s.Addr() != "[2001:db8::7]:5555" {
		t.Errorf("IPv6: %q", s.Addr())
	}
}

// TestEchoCheck: questions to the group that never come back mean the
// system drops this process's local network traffic.
func TestEchoCheck(t *testing.T) {
	now := time.Unix(1000, 0)
	b := &Browser{opt: Options{Now: func() time.Time { return now }}}
	b.expectEcho([]byte("q1"))
	now = now.Add(time.Second)
	b.expectEcho([]byte("q2"))
	if err := b.Err(); err != nil {
		t.Fatalf("after 1 s: %v", err)
	}
	now = now.Add(time.Second)
	b.expectEcho([]byte("q3"))
	if err := b.Err(); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("after 2 s without an echo: %v", err)
	}
	if b.echoed([]byte("another computer's question")) {
		t.Error("someone else's message counted as an echo")
	}
	if !b.echoed([]byte("q2")) {
		t.Fatal("q2 did not count as an echo")
	}
	if err := b.Err(); err != nil {
		t.Errorf("after the echo: %v", err)
	}
}

func TestTXT(t *testing.T) {
	const typ = "_apple-mobdev2._tcp"
	inst := "fa:df:90:f6:dd:2a@fe80::f8df:90ff:fef6:dd2a-supportsRP-26." + typ + ".local."
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	b.EnableCompression()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.StartAnswers())
	must(b.PTRResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(typ + ".local."), Class: dnsmessage.ClassINET, TTL: 4500},
		dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(inst)}))
	must(b.StartAdditionals())
	must(b.TXTResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(inst), Class: dnsmessage.ClassINET | unicastBit, TTL: 4500},
		dnsmessage.TXTResource{TXT: []string{"identifier=abc", "authTag=AAAAAAAAAAA="}}))
	must(b.SRVResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(inst), Class: dnsmessage.ClassINET | unicastBit, TTL: 120},
		dnsmessage.SRVResource{Target: dnsmessage.MustNewName("iphone.local."), Port: 32498}))
	must(b.AResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("iphone.local."), Class: dnsmessage.ClassINET | unicastBit, TTL: 120},
		dnsmessage.AResource{A: [4]byte{192, 168, 33, 187}}))
	msg, err := b.Finish()
	must(err)

	c := newCache()
	now := time.Now()
	addMsg(t, c, now, []string{typ}, msg)
	svcs := c.services(now, []string{typ})
	if len(svcs) != 1 || strings.Join(svcs[0].TXT, "|") != "identifier=abc|authTag=AAAAAAAAAAA=" || svcs[0].Addr() != "192.168.33.187:32498" {
		t.Fatalf("services = %+v", svcs)
	}
	if svcs := c.services(now.Add(4501*time.Second), []string{typ}); len(svcs) != 0 {
		t.Errorf("after the TTLs: %+v", svcs)
	}

	// A goodbye for the TXT record drops it, and the service stays.
	bye := response(t, nil, []rr{{name: inst, typ: dnsmessage.TypeTXT, ttl: 0, data: "identifier=abc"}})
	addMsg(t, c, now, []string{typ}, bye)
	if svcs := c.services(now, []string{typ}); len(svcs) != 1 || svcs[0].TXT != nil {
		t.Errorf("after the goodbye: %+v", svcs)
	}
}

// TestBadTXT: a TXT record that does not parse is skipped, and the records
// after it in the same response still count, as before TXT records were
// read at all.
func TestBadTXT(t *testing.T) {
	const typ = "_apple-mobdev2._tcp"
	inst := "fa:df:90:f6:dd:2a@fe80::f8df:90ff:fef6:dd2a-supportsRP-26." + typ + ".local."
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.StartAnswers())
	must(b.PTRResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(typ + ".local."), Class: dnsmessage.ClassINET, TTL: 4500},
		dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(inst)}))
	must(b.StartAdditionals())
	must(b.TXTResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(inst), Class: dnsmessage.ClassINET, TTL: 4500},
		dnsmessage.TXTResource{TXT: []string{"identifier=abc"}}))
	must(b.SRVResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(inst), Class: dnsmessage.ClassINET, TTL: 120},
		dnsmessage.SRVResource{Target: dnsmessage.MustNewName("iphone.local."), Port: 32498}))
	must(b.AResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("iphone.local."), Class: dnsmessage.ClassINET, TTL: 120},
		dnsmessage.AResource{A: [4]byte{192, 168, 33, 187}}))
	msg, err := b.Finish()
	must(err)
	// The TXT string's length byte says more than the record holds.
	i := bytes.Index(msg, []byte("\x0eidentifier=abc"))
	if i < 0 {
		t.Fatal("no TXT string in the message")
	}
	msg[i] = 0x40

	recs, ok := parse(msg)
	if !ok {
		t.Fatal("parse gave up")
	}
	var types []dnsmessage.Type
	for _, r := range recs {
		types = append(types, r.typ)
	}
	if want := []dnsmessage.Type{dnsmessage.TypePTR, dnsmessage.TypeSRV, dnsmessage.TypeA}; !slices.Equal(types, want) {
		t.Fatalf("records %v, want %v", types, want)
	}
	c := newCache()
	now := time.Now()
	addMsg(t, c, now, []string{typ}, msg)
	if svcs := c.services(now, []string{typ}); len(svcs) != 1 || svcs[0].TXT != nil || svcs[0].Addr() != "192.168.33.187:32498" {
		t.Fatalf("services = %+v", svcs)
	}
}

func TestQueryMulticastAnswers(t *testing.T) {
	msg, err := query([]string{connectType}, []string{"adb-x._adb-tls-connect._tcp.local."}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var p dnsmessage.Parser
	if _, err := p.Start(msg); err != nil {
		t.Fatal(err)
	}
	qs, err := p.AllQuestions()
	if err != nil || len(qs) != 2 {
		t.Fatal(qs, err)
	}
	for _, q := range qs {
		if q.Class != dnsmessage.ClassINET {
			t.Errorf("%v asks for a unicast answer", q)
		}
	}
}
