//go:build !windows

// Pinky runs its own usbmuxd on Linux only (netmux.go); these tests serve
// it on a Unix socket in /tmp, which Windows does not have.

package netmux

import (
	"bufio"
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver/android/mdns"
	goios "github.com/danielpaulus/go-ios/ios"
	"howett.net/plist"
)

// fakeUpstream is a usbmuxd with one phone on a cable (DeviceID 5), which
// echoes what it is sent with "usb:" in front, and a pair record for
// "PAIRED".
type fakeUpstream struct {
	l       net.Listener
	addr    string
	netUDID string // a phone it lists on the network itself, when set
}

func startUpstream(t *testing.T, dir string, netUDID ...string) *fakeUpstream {
	t.Helper()
	path := filepath.Join(dir, "up.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	u := &fakeUpstream{l: l, addr: "unix://" + path}
	if len(netUDID) > 0 {
		u.netUDID = netUDID[0]
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go u.serve(c)
		}
	}()
	t.Cleanup(func() { l.Close() })
	return u
}

func (u *fakeUpstream) serve(c net.Conn) {
	defer c.Close()
	for {
		h, body, err := readMsg(c)
		if err != nil {
			return
		}
		var req struct {
			MessageType  string
			DeviceID     int
			PairRecordID string
		}
		if _, err := plist.Unmarshal(body, &req); err != nil {
			return
		}
		switch req.MessageType {
		case "ListDevices":
			list := []map[string]any{{
				"DeviceID": 5, "MessageType": "Attached",
				"Properties": map[string]any{"ConnectionType": "USB", "DeviceID": 5, "SerialNumber": "USB-PHONE", "LocationID": 7},
			}}
			if u.netUDID != "" {
				list = append(list, map[string]any{
					"DeviceID": 6, "MessageType": "Attached",
					"Properties": map[string]any{"ConnectionType": "Network", "DeviceID": 6, "SerialNumber": u.netUDID},
				})
			}
			_ = writePlist(c, h.Tag, map[string]any{"DeviceList": list})
		case "Connect":
			if req.DeviceID != 5 {
				_ = writePlist(c, h.Tag, result{MessageType: "Result", Number: 2})
				continue
			}
			_ = writePlist(c, h.Tag, result{MessageType: "Result", Number: 0})
			echo(c, "usb:")
			return
		case "ReadPairRecord":
			if req.PairRecordID != "PAIRED" {
				_ = writePlist(c, h.Tag, result{MessageType: "Result", Number: 2})
				continue
			}
			rec, _ := plist.Marshal(map[string]any{"HostID": "HOST-1", "WiFiMACAddress": "08:87:c7:2a:8a:db"}, plist.XMLFormat)
			_ = writePlist(c, h.Tag, map[string]any{"PairRecordData": rec})
		}
	}
}

// echo answers each line read from c with prefix and the line.
func echo(c net.Conn, prefix string) {
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if _, err := c.Write([]byte(prefix + line)); err != nil {
			return
		}
	}
}

// startPhone is a phone on the network: a TCP port on 127.0.0.1 that
// echoes with "wifi:" in front.
func startPhone(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				echo(c, "wifi:")
			}()
		}
	}()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

// shortDir is a directory for sockets: macOS limits their paths to 104
// bytes, which t.TempDir's often exceed.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "netmux")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// startMux serves a Mux in front of up and points go-ios at it.
func startMux(t *testing.T, dir string, up *fakeUpstream) *Mux {
	t.Helper()
	m := New(up.addr, nil)
	path := filepath.Join(dir, "mux.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	go m.Serve(l)
	t.Cleanup(func() { l.Close() })
	t.Setenv("USBMUXD_SOCKET_ADDRESS", "unix://"+path)
	return m
}

// talk connects to port on the device through go-ios, sends a line and
// returns the answer.
func talk(t *testing.T, deviceID int, port uint16) (string, error) {
	t.Helper()
	mux, err := goios.NewUsbMuxConnectionSimple()
	if err != nil {
		t.Fatal(err)
	}
	if err := mux.Connect(deviceID, port); err != nil {
		mux.Close()
		return "", err
	}
	conn := mux.ReleaseDeviceConnection() // and closed below; mux.Close would now fail
	defer conn.Close()
	if _, err := conn.Writer().Write([]byte("hi\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn.Reader()).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(line), nil
}

func TestMux(t *testing.T) {
	dir := shortDir(t)
	up := startUpstream(t, dir)
	m := startMux(t, dir, up)
	port := startPhone(t)
	m.SetPhones([]Phone{{UDID: "NET-PHONE", Addr: netip.MustParseAddr("127.0.0.1")}})

	list, err := goios.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	netID := 0
	for _, d := range list.DeviceList {
		got = append(got, d.Properties.SerialNumber+" "+d.Properties.ConnectionType)
		if d.Properties.ConnectionType == "Network" {
			netID = d.DeviceID
			if d.Properties.DeviceID != d.DeviceID {
				t.Errorf("properties' DeviceID %d, entry's %d", d.Properties.DeviceID, d.DeviceID)
			}
		}
	}
	if strings.Join(got, ", ") != "USB-PHONE USB, NET-PHONE Network" || netID < firstNetworkID {
		t.Fatalf("devices = %v, network DeviceID %d", got, netID)
	}

	// A connection to the phone on the network goes to its address.
	if answer, err := talk(t, netID, port); err != nil || answer != "wifi:hi" {
		t.Errorf("over Wi-Fi: %q, %v", answer, err)
	}
	// One to the phone on the cable goes through the upstream.
	if answer, err := talk(t, 5, 62078); err != nil || answer != "usb:hi" {
		t.Errorf("on the cable: %q, %v", answer, err)
	}
	// A port the phone does not listen on is refused.
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	closedPort := uint16(closed.Addr().(*net.TCPAddr).Port)
	closed.Close()
	if _, err := talk(t, netID, closedPort); err == nil {
		t.Error("a closed port connected")
	}
	// A device usbmuxd does not know is the upstream's to refuse.
	if _, err := talk(t, 99, 62078); err == nil {
		t.Error("an unknown device connected")
	}

	// Pair records come from the upstream.
	rec, err := goios.ReadPairRecord("PAIRED")
	if err != nil || rec.HostID != "HOST-1" {
		t.Errorf("pair record = %+v, %v", rec, err)
	}
	if info, err := m.readPair("PAIRED"); err != nil || info != (PairInfo{HostID: "HOST-1", WiFiMAC: "08:87:c7:2a:8a:db"}) {
		t.Errorf("readPair = %+v, %v", info, err)
	}

	// Without the upstream and without phones on the network, listing
	// fails, as without a usbmuxd.
	up.l.Close()
	m.SetPhones(nil)
	if _, err := goios.ListDevices(); err == nil {
		t.Error("listed devices without a usbmuxd")
	}
}

// libimobiledevice's usbmuxd exits once no phone is on a cable (usbmuxd
// --systemd): the phones on the network stay listed and usable, with their
// pair records read from PairDir. On arch both phones went offline the
// second it exited.
func TestMuxWithoutUpstream(t *testing.T) {
	dir := shortDir(t)
	up := startUpstream(t, dir)
	m := startMux(t, dir, up)
	m.PairDir = filepath.Join(dir, "lockdown")
	if err := os.Mkdir(m.PairDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec, _ := plist.Marshal(map[string]any{"HostID": "HOST-2", "WiFiMACAddress": "f0:2f:4b:e8:d3:4a"}, plist.XMLFormat)
	if err := os.WriteFile(filepath.Join(m.PairDir, "NET-PHONE.plist"), rec, 0o644); err != nil {
		t.Fatal(err)
	}
	port := startPhone(t)
	m.SetPhones([]Phone{{UDID: "NET-PHONE", Addr: netip.MustParseAddr("127.0.0.1")}})
	up.l.Close() // the last phone left the cable

	list, err := goios.ListDevices()
	if err != nil || len(list.DeviceList) != 1 || list.DeviceList[0].Properties.SerialNumber != "NET-PHONE" {
		t.Fatalf("devices = %+v, %v", list.DeviceList, err)
	}
	if answer, err := talk(t, list.DeviceList[0].DeviceID, port); err != nil || answer != "wifi:hi" {
		t.Errorf("over Wi-Fi: %q, %v", answer, err)
	}
	if r, err := goios.ReadPairRecord("NET-PHONE"); err != nil || r.HostID != "HOST-2" {
		t.Errorf("pair record = %+v, %v", r, err)
	}
	if _, err := goios.ReadPairRecord("OTHER"); err == nil {
		t.Error("read a pair record that is not there")
	}
	if _, err := goios.ReadPairRecord("../NET-PHONE"); err == nil {
		t.Error("read a pair record outside PairDir")
	}
	if info, err := m.readPair("NET-PHONE"); err != nil || info.HostID != "HOST-2" {
		t.Errorf("readPair = %+v, %v", info, err)
	}
}

func TestSetPhonesIDs(t *testing.T) {
	m := New("unix:///nonexistent", nil)
	a := netip.MustParseAddr("192.168.1.10")
	m.SetPhones([]Phone{{UDID: "A", Addr: a}})
	first := m.phones["A"].id
	m.SetPhones([]Phone{{UDID: "A", Addr: a}, {UDID: "B", Addr: netip.MustParseAddr("192.168.1.11")}})
	if m.phones["A"].id != first {
		t.Error("a phone that stayed got a new DeviceID")
	}
	m.SetPhones(nil)
	m.SetPhones([]Phone{{UDID: "A", Addr: a}})
	if m.phones["A"].id == first {
		t.Error("a phone that came back kept its DeviceID")
	}
	m.SetPhones([]Phone{{UDID: "A", Addr: netip.MustParseAddr("192.168.1.12")}})
	if got := m.Phones(); len(got) != 1 || got[0].Addr.String() != "192.168.1.12" {
		t.Errorf("phones = %v", got)
	}
}

func TestMuxLeavesTheUpstreamsNetworkPhones(t *testing.T) {
	dir := shortDir(t)
	up := startUpstream(t, dir, "BOTH")
	m := startMux(t, dir, up)
	m.SetPhones([]Phone{{UDID: "BOTH", Addr: netip.MustParseAddr("127.0.0.1")}, {UDID: "OURS", Addr: netip.MustParseAddr("127.0.0.1")}})
	list, err := goios.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range list.DeviceList {
		got = append(got, d.Properties.SerialNumber+" "+strconv.Itoa(d.DeviceID))
	}
	if len(got) != 3 || got[1] != "BOTH 6" || !strings.HasPrefix(got[2], "OURS ") {
		t.Errorf("devices = %v", got)
	}
}

// The computer's Wi-Fi drops for a while (a DHCP renew: 8 s and 36 s on
// arch), with no phone on a cable, so the system's usbmuxd is not running:
// lockdown does not answer and the records lapse. The list through the Mux
// keeps the phone all along; on arch it came back empty (EOF) 8 s into
// such a drop, and Pinky closed its session on the iPad.
func TestMuxKeepsAPhoneThroughABlip(t *testing.T) {
	dir := shortDir(t)
	up := startUpstream(t, dir)
	m := startMux(t, dir, up)
	up.l.Close()
	src := &fakeSource{svcs: []mdns.Service{iphoneService("127.0.0.1")}}
	now := time.Unix(1_000_000, 0)
	answers := true
	f := &Finder{
		Mux:      m,
		UDIDs:    func() []string { return []string{"IPHONE"} },
		BrowseFn: func() (Source, error) { return src, nil },
		PairFn:   func(string) (PairInfo, error) { return PairInfo{HostID: testHostID}, nil },
		ProbeFn:  func(context.Context, netip.Addr) bool { return answers },
		NowFn:    func() time.Time { return now },
	}
	s := newFindState(f, f.logOrDiscard())
	s.tick(context.Background())

	answers = false
	src.mu.Lock()
	src.svcs = nil
	src.mu.Unlock()
	for i := 0; i < 14; i++ { // 42 s, a tick every 3 s
		now = now.Add(3 * time.Second)
		s.tick(context.Background())
		list, err := goios.ListDevices()
		if err != nil || len(list.DeviceList) != 1 {
			t.Fatalf("%v into the drop: devices %+v, %v", time.Duration(i+1)*3*time.Second, list.DeviceList, err)
		}
	}
	answers = true
	now = now.Add(3 * time.Second)
	s.tick(context.Background())
	if len(m.Phones()) != 1 {
		t.Error("not listed once it answers again")
	}
}
