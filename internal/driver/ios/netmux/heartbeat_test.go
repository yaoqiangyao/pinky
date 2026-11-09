package netmux

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"howett.net/plist"
)

// pipeConn is one end of a net.Pipe as a heartbeatConn.
type pipeConn struct{ net.Conn }

func (c pipeConn) Reader() io.Reader { return c.Conn }
func (c pipeConn) Writer() io.Writer { return c.Conn }

func writeHB(t *testing.T, w io.Writer, v any) {
	t.Helper()
	b, err := plist.Marshal(v, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(b))), b...)); err != nil {
		t.Fatal(err)
	}
}

func readHB(t *testing.T, r io.Reader) string {
	t.Helper()
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		t.Fatal(err)
	}
	var m struct{ Command string }
	if _, err := plist.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m.Command
}

func TestAnswerHeartbeat(t *testing.T) {
	phone, host := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- answerHeartbeat(pipeConn{host}) }()

	writeHB(t, phone, map[string]any{"Command": "Marco", "Interval": 10, "SupportsSleepyTime": true})
	if got := readHB(t, phone); got != "Polo" {
		t.Fatalf("answer = %q, want Polo", got)
	}
	// Anything else is not answered; the next Marco is.
	writeHB(t, phone, map[string]any{"Command": "SleepyTime"})
	writeHB(t, phone, map[string]any{"Command": "Marco", "Interval": 10})
	if got := readHB(t, phone); got != "Polo" {
		t.Fatalf("answer = %q, want Polo", got)
	}
	phone.Close()
	if err := <-done; err == nil {
		t.Error("no error once the phone closed the connection")
	}
}

// TestSetPhonesHeartbeat: a listed phone's heartbeat runs while it is
// listed at that address, and starts again when it ends.
func TestSetPhonesHeartbeat(t *testing.T) {
	m := New("unix:///nonexistent", nil)
	m.Heartbeat = true
	var mu sync.Mutex
	conns := map[int][]net.Conn{} // phone ends, by DeviceID
	connected := make(chan int, 10)
	m.HeartbeatFn = func(p Phone, id int) (heartbeatConn, error) {
		phone, host := net.Pipe()
		mu.Lock()
		conns[id] = append(conns[id], phone)
		mu.Unlock()
		connected <- id
		return pipeConn{host}, nil
	}
	wait := func() int {
		t.Helper()
		select {
		case id := <-connected:
			return id
		case <-time.After(5 * time.Second):
			t.Fatal("no heartbeat started")
			return 0
		}
	}
	closed := func(c net.Conn) bool {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err := c.Read(make([]byte, 1))
		return errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe)
	}

	m.SetPhones([]Phone{{UDID: "A", Addr: netip.MustParseAddr("192.168.1.10")}})
	first := wait()
	mu.Lock()
	c := conns[first][0]
	mu.Unlock()
	writeHB(t, c, map[string]any{"Command": "Marco"})
	if got := readHB(t, c); got != "Polo" {
		t.Fatalf("answer = %q", got)
	}

	// Ended by the phone: started again.
	c.Close()
	if id := wait(); id != first {
		t.Fatalf("started again for DeviceID %d, want %d", id, first)
	}

	// The same phone at the same address: nothing new.
	m.SetPhones([]Phone{{UDID: "A", Addr: netip.MustParseAddr("192.168.1.10")}})
	select {
	case id := <-connected:
		t.Fatalf("another heartbeat started (%d)", id)
	case <-time.After(100 * time.Millisecond):
	}

	// Moved: the old one ends and a new one starts.
	mu.Lock()
	old := conns[first][1]
	mu.Unlock()
	m.SetPhones([]Phone{{UDID: "A", Addr: netip.MustParseAddr("192.168.1.11")}})
	if id := wait(); id == first {
		t.Fatal("the moved phone's heartbeat kept its DeviceID")
	}
	if !closed(old) {
		t.Error("the old heartbeat goes on after the phone moved")
	}

	// Gone: its heartbeat ends.
	mu.Lock()
	last := conns[m.phones["A"].id][0]
	mu.Unlock()
	m.SetPhones(nil)
	if !closed(last) {
		t.Error("the heartbeat goes on after the phone left")
	}
}

func TestPickAddr(t *testing.T) {
	a := netip.MustParseAddr
	wifi, wifi2, link := a("192.168.89.20"), a("192.168.89.30"), a("172.20.10.1")
	cases := []struct {
		name  string
		addrs []netip.Addr
		last  netip.Addr
		want  netip.Addr
		ok    bool
	}{
		{"Wi-Fi over the cable's link", []netip.Addr{link, wifi}, netip.Addr{}, wifi, true},
		{"the same in the other order", []netip.Addr{wifi, link}, link, wifi, true},
		{"the cable's link alone", []netip.Addr{link}, netip.Addr{}, link, true},
		{"its Wi-Fi record missing a moment: stays", []netip.Addr{link}, wifi, wifi, true},
		{"stays at its address", []netip.Addr{wifi, wifi2}, wifi2, wifi2, true},
		{"moves when it left that address", []netip.Addr{wifi}, wifi2, wifi, true},
		{"nothing", nil, netip.Addr{}, netip.Addr{}, false},
	}
	for _, c := range cases {
		got, ok := pickAddr(c.addrs, c.last)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: %v %v, want %v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
