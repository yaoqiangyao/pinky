//go:build linux

package secret

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// The tests run the store against a fake Secret Service on a private
// session bus (dbus-daemon), whose prompts answer by themselves as the
// test says: no keyring daemon, display or person is needed.

// startBus starts a private session bus and points the process at it.
func startBus(t *testing.T) string {
	t.Helper()
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("no dbus-daemon to run a private session bus")
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "bus.conf")
	if err := os.WriteFile(config, []byte(`<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:path=`+filepath.Join(dir, "bus")+`</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(daemon, "--config-file="+config, "--nofork", "--print-address=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("dbus-daemon: %v", err)
	}
	addr := strings.TrimSpace(line)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)
	return addr
}

// fakeService is a Secret Service in memory. answer decides how its
// prompts end: "accept", "dismiss", or "wait" (until Dismiss).
type fakeService struct {
	conn *dbus.Conn

	mu          sync.Mutex
	answer      string
	lockRefuses string // "locked": Lock fails on a locked collection, as KWallet's does; "always": every Lock fails
	prompts     int    // prompts shown
	dismissed   int    // prompts dismissed by the client
	next        int
	collections map[dbus.ObjectPath]*fakeCollection
	items       map[dbus.ObjectPath]*fakeItem
	pending     map[dbus.ObjectPath]func(accept bool) dbus.Variant
}

type fakeCollection struct {
	label  string
	locked bool
}

type fakeItem struct {
	coll   dbus.ObjectPath
	attrs  map[string]string
	secret []byte
}

func startFakeService(t *testing.T, addr string) *fakeService {
	t.Helper()
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	f := &fakeService{
		conn:        conn,
		answer:      "accept",
		collections: map[dbus.ObjectPath]*fakeCollection{},
		items:       map[dbus.ObjectPath]*fakeItem{},
		pending:     map[dbus.ObjectPath]func(bool) dbus.Variant{},
	}
	for _, e := range []struct {
		v     any
		iface string
	}{
		{fakeServiceAPI{f}, ssService},
		{fakeCollectionAPI{f}, ssCollection},
		{fakeItemAPI{f}, ssItem},
		{fakePromptAPI{f}, ssPrompt},
		{fakeSessionAPI{f}, ssSession},
		{fakeProperties{f}, "org.freedesktop.DBus.Properties"},
	} {
		if err := conn.ExportSubtree(e.v, ssPath, e.iface); err != nil {
			t.Fatal(err)
		}
	}
	if reply, err := conn.RequestName(ssName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own %s: %v %v", ssName, reply, err)
	}
	return f
}

func (f *fakeService) set(answer string) {
	f.mu.Lock()
	f.answer = answer
	f.mu.Unlock()
}

func (f *fakeService) counts() (prompts, dismissed int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompts, f.dismissed
}

// newPrompt returns a prompt whose acceptance runs done; f.mu is held.
func (f *fakeService) newPrompt(done func(accept bool) dbus.Variant) dbus.ObjectPath {
	f.next++
	p := dbus.ObjectPath(fmt.Sprintf("%s/prompt/p%d", ssPath, f.next))
	f.pending[p] = done
	return p
}

func (f *fakeService) complete(p dbus.ObjectPath, accept bool) {
	f.mu.Lock()
	done := f.pending[p]
	delete(f.pending, p)
	var result dbus.Variant
	if done != nil {
		result = done(accept)
	}
	f.mu.Unlock()
	if done == nil {
		return
	}
	if !accept {
		result = dbus.MakeVariant("")
	}
	f.conn.Emit(p, ssPrompt+".Completed", !accept, result)
}

type fakeServiceAPI struct{ f *fakeService }

func (a fakeServiceAPI) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	if algorithm != "plain" {
		return dbus.Variant{}, "", dbus.NewError("org.freedesktop.DBus.Error.NotSupported", nil)
	}
	return dbus.MakeVariant(""), ssPath + "/session/s1", nil
}

func (a fakeServiceAPI) CreateCollection(props map[string]dbus.Variant, alias string) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	label, _ := props[ssCollection+".Label"].Value().(string)
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	p := a.f.newPrompt(func(accept bool) dbus.Variant {
		if !accept {
			return dbus.MakeVariant(noPrompt)
		}
		a.f.next++
		path := dbus.ObjectPath(fmt.Sprintf("%s/collection/c%d", ssPath, a.f.next))
		a.f.collections[path] = &fakeCollection{label: label}
		return dbus.MakeVariant(path)
	})
	return noPrompt, p, nil
}

func (a fakeServiceAPI) Unlock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	locked := false
	for _, o := range objects {
		if c := a.f.collections[o]; c != nil && c.locked {
			locked = true
		}
	}
	if !locked {
		return objects, noPrompt, nil
	}
	p := a.f.newPrompt(func(accept bool) dbus.Variant {
		if accept {
			for _, o := range objects {
				if c := a.f.collections[o]; c != nil {
					c.locked = false
				}
			}
		}
		return dbus.MakeVariant(objects)
	})
	return []dbus.ObjectPath{}, p, nil
}

// SearchItems finds items in every collection: those of unlocked
// collections first, then those of locked ones.
func (a fakeServiceAPI) SearchItems(attrs map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	unlocked, locked := []dbus.ObjectPath{}, []dbus.ObjectPath{}
	for path, it := range a.f.items {
		match := true
		for k, v := range attrs {
			match = match && it.attrs[k] == v
		}
		switch {
		case !match:
		case a.f.collections[it.coll].locked:
			locked = append(locked, path)
		default:
			unlocked = append(unlocked, path)
		}
	}
	return unlocked, locked, nil
}

func (a fakeServiceAPI) Lock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	for _, o := range objects {
		c := a.f.collections[o]
		if c == nil {
			continue
		}
		if a.f.lockRefuses == "always" || a.f.lockRefuses == "locked" && c.locked {
			return nil, "", dbus.NewError("org.freedesktop.DBus.Error.Failed", []any{"Can't lock object at path " + string(o)})
		}
		c.locked = true
	}
	return objects, noPrompt, nil
}

type fakeCollectionAPI struct{ f *fakeService }

func (a fakeCollectionAPI) SearchItems(msg dbus.Message, attrs map[string]string) ([]dbus.ObjectPath, *dbus.Error) {
	coll := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	found := []dbus.ObjectPath{}
	for path, it := range a.f.items {
		match := it.coll == coll
		for k, v := range attrs {
			match = match && it.attrs[k] == v
		}
		if match {
			found = append(found, path)
		}
	}
	return found, nil
}

func (a fakeCollectionAPI) CreateItem(msg dbus.Message, props map[string]dbus.Variant, secret ssSecret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	coll := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	attrs, _ := props[ssItem+".Attributes"].Value().(map[string]string)
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	if c := a.f.collections[coll]; c == nil {
		return "", "", dbus.NewError("org.freedesktop.Secret.Error.NoSuchObject", nil)
	} else if c.locked {
		return "", "", dbus.NewError("org.freedesktop.Secret.Error.IsLocked", nil)
	}
	if replace {
		for path, it := range a.f.items {
			if it.coll == coll && it.attrs["service"] == attrs["service"] && it.attrs["account"] == attrs["account"] {
				it.secret = append([]byte(nil), secret.Value...)
				return path, noPrompt, nil
			}
		}
	}
	a.f.next++
	path := dbus.ObjectPath(fmt.Sprintf("%s/i%d", coll, a.f.next))
	a.f.items[path] = &fakeItem{coll: coll, attrs: attrs, secret: append([]byte(nil), secret.Value...)}
	return path, noPrompt, nil
}

type fakeItemAPI struct{ f *fakeService }

func (a fakeItemAPI) GetSecret(msg dbus.Message, session dbus.ObjectPath) (ssSecret, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	it := a.f.items[path]
	if it == nil {
		return ssSecret{}, dbus.NewError("org.freedesktop.Secret.Error.NoSuchObject", nil)
	}
	if a.f.collections[it.coll].locked {
		return ssSecret{}, dbus.NewError("org.freedesktop.Secret.Error.IsLocked", nil)
	}
	return ssSecret{Session: session, Parameters: []byte{}, Value: it.secret, ContentType: "text/plain"}, nil
}

func (a fakeItemAPI) Delete(msg dbus.Message) (dbus.ObjectPath, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	it := a.f.items[path]
	if it == nil {
		return "", dbus.NewError("org.freedesktop.Secret.Error.NoSuchObject", nil)
	}
	if a.f.collections[it.coll].locked {
		return "", dbus.NewError("org.freedesktop.Secret.Error.IsLocked", nil)
	}
	delete(a.f.items, path)
	return noPrompt, nil
}

type fakePromptAPI struct{ f *fakeService }

func (a fakePromptAPI) Prompt(msg dbus.Message, window string) *dbus.Error {
	p := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	a.f.mu.Lock()
	a.f.prompts++
	answer := a.f.answer
	a.f.mu.Unlock()
	if answer != "wait" {
		go func() {
			time.Sleep(10 * time.Millisecond)
			a.f.complete(p, answer == "accept")
		}()
	}
	return nil
}

func (a fakePromptAPI) Dismiss(msg dbus.Message) *dbus.Error {
	p := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	a.f.mu.Lock()
	a.f.dismissed++
	a.f.mu.Unlock()
	a.f.complete(p, false)
	return nil
}

type fakeSessionAPI struct{ f *fakeService }

func (fakeSessionAPI) Close() *dbus.Error { return nil }

type fakeProperties struct{ f *fakeService }

func (a fakeProperties) Get(msg dbus.Message, iface, name string) (dbus.Variant, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	switch iface + "." + name {
	case ssService + ".Collections":
		paths := []dbus.ObjectPath{}
		for p := range a.f.collections {
			paths = append(paths, p)
		}
		return dbus.MakeVariant(paths), nil
	case ssCollection + ".Label":
		if c := a.f.collections[path]; c != nil {
			return dbus.MakeVariant(c.label), nil
		}
	case ssCollection + ".Locked":
		if c := a.f.collections[path]; c != nil {
			return dbus.MakeVariant(c.locked), nil
		}
	}
	return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", nil)
}

func TestSecretServiceSaveRevealDelete(t *testing.T) {
	f := startFakeService(t, startBus(t))
	s := newSecretService("app.pinky.desktop.test", "Pinky test")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// An item of another keyring with the same attributes is not Pinky's.
	login := ssPath + "/collection/login"
	f.mu.Lock()
	f.collections[login] = &fakeCollection{label: "Login"}
	f.items[login+"/1"] = &fakeItem{coll: login, secret: []byte("not this"),
		attrs: map[string]string{"service": "app.pinky.desktop.test", "account": "pin:a"}}
	f.mu.Unlock()

	if s.Has("pin:a") {
		t.Fatal("Has before Save")
	}
	if _, err := s.Reveal(ctx, "pin:a", "unlock A"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Reveal before Save = %v, want ErrNotFound", err)
	}
	// The first Save creates the keyring, which asks the person.
	if err := s.Save("pin:a", "1234"); err != nil {
		t.Fatal(err)
	}
	if p, _ := f.counts(); p != 1 {
		t.Fatalf("%d prompts for the first Save, want 1 (create the keyring)", p)
	}
	if !s.Has("pin:a") || s.Has("pin:b") {
		t.Fatalf("Has: pin:a %t, pin:b %t", s.Has("pin:a"), s.Has("pin:b"))
	}
	// The keyring is locked after Save, and Reveal asks to unlock it.
	got, err := s.Reveal(ctx, "pin:a", "unlock A")
	if err != nil || got != "1234" {
		t.Fatalf("Reveal = %q, %v", got, err)
	}
	if p, _ := f.counts(); p != 2 {
		t.Fatalf("%d prompts after Reveal, want 2", p)
	}
	// Saving again replaces the secret (and asks to unlock).
	if err := s.Save("pin:a", "5678"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Reveal(ctx, "pin:a", "unlock A"); err != nil || got != "5678" {
		t.Fatalf("Reveal after the second Save = %q, %v", got, err)
	}

	// Cancelling the prompt is ErrCancelled.
	f.set("dismiss")
	if _, err := s.Reveal(ctx, "pin:a", "unlock A"); !errors.Is(err, ErrCancelled) {
		t.Fatalf("Reveal with a dismissed prompt = %v, want ErrCancelled", err)
	}
	// A context that ends takes the prompt away.
	f.set("wait")
	short, cancelShort := context.WithTimeout(ctx, 200*time.Millisecond)
	_, err = s.Reveal(short, "pin:a", "unlock A")
	cancelShort()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Reveal past its context = %v, want DeadlineExceeded", err)
	}
	if _, d := f.counts(); d != 1 {
		t.Fatalf("%d prompts dismissed, want 1", d)
	}

	f.set("accept")
	if err := s.Delete("pin:a"); err != nil {
		t.Fatal(err)
	}
	if s.Has("pin:a") {
		t.Fatal("Has after Delete")
	}
	if err := s.Delete("pin:a"); err != nil {
		t.Fatalf("Delete of nothing: %v", err)
	}
}

// KWallet's Secret Service refuses to lock a collection that is locked
// already; Reveal still unlocks it, asking the person.
func TestSecretServiceLockRefusedWhenLocked(t *testing.T) {
	f := startFakeService(t, startBus(t))
	s := newSecretService("app.pinky.desktop.test", "Pinky test")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.Save("pin:a", "1234"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.lockRefuses = "locked"
	f.mu.Unlock()
	got, err := s.Reveal(ctx, "pin:a", "unlock A")
	if err != nil || got != "1234" {
		t.Fatalf("Reveal of a locked keyring = %q, %v", got, err)
	}
	if p, _ := f.counts(); p != 2 {
		t.Fatalf("%d prompts after Reveal, want 2", p)
	}
	// An open keyring is locked first, so Reveal asks all the same.
	f.mu.Lock()
	for _, c := range f.collections {
		c.locked = false
	}
	f.mu.Unlock()
	if got, err := s.Reveal(ctx, "pin:a", "unlock A"); err != nil || got != "1234" {
		t.Fatalf("Reveal of an open keyring = %q, %v", got, err)
	}
	if p, _ := f.counts(); p != 3 {
		t.Fatalf("%d prompts after Reveal of an open keyring, want 3", p)
	}
}

// A keyring that stays open because the service won't lock it is not
// revealed without asking.
func TestSecretServiceLockRefusedWhenOpen(t *testing.T) {
	f := startFakeService(t, startBus(t))
	s := newSecretService("app.pinky.desktop.test", "Pinky test")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.Save("pin:a", "1234"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.lockRefuses = "always"
	for _, c := range f.collections {
		c.locked = false
	}
	f.mu.Unlock()
	if got, err := s.Reveal(ctx, "pin:a", "unlock A"); err == nil {
		t.Fatalf("Reveal of a keyring that won't lock = %q, want an error", got)
	}
}

func TestSecretServiceCancelCreate(t *testing.T) {
	f := startFakeService(t, startBus(t))
	f.set("dismiss")
	s := newSecretService("app.pinky.desktop.test", "Pinky test")
	if err := s.Save("pin:a", "1234"); !errors.Is(err, ErrCancelled) {
		t.Fatalf("Save with the keyring's creation dismissed = %v, want ErrCancelled", err)
	}
	if s.Has("pin:a") {
		t.Fatal("Has after a cancelled Save")
	}
}

func TestSecretServiceMissing(t *testing.T) {
	startBus(t) // a bus without a Secret Service
	s := newSecretService("app.pinky.desktop.test", "Pinky test")
	if s.Has("pin:a") {
		t.Fatal("Has without a Secret Service")
	}
	if err := s.Save("pin:a", "1234"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Save without a Secret Service = %v, want ErrUnsupported", err)
	}
}

func TestSecretServiceNoBus(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	s := newSecretService("app.pinky.desktop.test", "Pinky test")
	if s.Has("pin:a") {
		t.Fatal("Has without a session bus")
	}
	if err := s.Save("pin:a", "1234"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Save without a session bus = %v, want ErrUnsupported", err)
	}
}
