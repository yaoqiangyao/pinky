//go:build linux

package secret

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// The Linux store keeps secrets in the Secret Service (GNOME Keyring,
// KWallet, KeePassXC...) over D-Bus, in a collection (a keyring) of
// Pinky's own, labelled "Pinky", which Pinky locks whenever it is not using
// it. The lock is what lets Reveal ask the person: the Secret Service hands
// out the items of an unlocked collection to any program without asking,
// and Pinky must not lock the person's login keyring, so Reveal locks
// Pinky's keyring and unlocks it again, which takes its password in the
// keyring's own prompt. The first Save creates the keyring, and the
// keyring asks the person to choose that password.
//
// The check is as strong as the person's keyring settings: a keyring
// without a password, or one set to unlock automatically at login, opens
// without asking. The system's prompt shows its own text, not Reveal's
// reason.

const (
	ssName       = "org.freedesktop.secrets"
	ssPath       = dbus.ObjectPath("/org/freedesktop/secrets")
	ssService    = "org.freedesktop.Secret.Service"
	ssCollection = "org.freedesktop.Secret.Collection"
	ssItem       = "org.freedesktop.Secret.Item"
	ssSession    = "org.freedesktop.Secret.Session"
	ssPrompt     = "org.freedesktop.Secret.Prompt"
	noPrompt     = dbus.ObjectPath("/")
)

func system() Store { return newSecretService(Service, "Pinky") }

// newSecretService returns the store for a service, whose items go in the
// collection labelled label.
func newSecretService(service, label string) Store {
	return &secretService{service: service, label: label}
}

type secretService struct {
	service string
	label   string

	mu   sync.Mutex
	conn *dbus.Conn
}

// ssSecret is the Secret Service's Secret struct, (oayays).
type ssSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// bus returns a connection to the session bus, made once and again after
// it breaks. It never starts a bus: without one there is no keyring.
func (s *secretService) bus() (*dbus.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil && s.conn.Connected() {
		return s.conn, nil
	}
	conn, err := dbus.SessionBusPrivateNoAutoStartup()
	if err != nil {
		return nil, fmt.Errorf("secret: no session bus (%v): %w", err, ErrUnsupported)
	}
	if err := conn.Auth(nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("secret: session bus: %v: %w", err, ErrUnsupported)
	}
	if err := conn.Hello(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("secret: session bus: %v: %w", err, ErrUnsupported)
	}
	s.conn = conn
	return conn, nil
}

// call calls a method of an object of the Secret Service and stores the
// replies.
func call(conn *dbus.Conn, path dbus.ObjectPath, method string, replies []any, args ...any) error {
	c := conn.Object(ssName, path).Call(method, 0, args...)
	if c.Err != nil {
		return ssError(method, c.Err)
	}
	if len(replies) > 0 {
		if err := c.Store(replies...); err != nil {
			return fmt.Errorf("secret: %s: %w", method, err)
		}
	}
	return nil
}

func ssError(method string, err error) error {
	var de dbus.Error
	if errors.As(err, &de) {
		switch de.Name {
		case "org.freedesktop.DBus.Error.ServiceUnknown", "org.freedesktop.DBus.Error.NameHasNoOwner":
			return fmt.Errorf("secret: no Secret Service on the session bus: %w", ErrUnsupported)
		case "org.freedesktop.Secret.Error.NoSuchObject", "org.freedesktop.DBus.Error.UnknownObject":
			return ErrNotFound
		}
	}
	return fmt.Errorf("secret: %s: %w", method[strings.LastIndexByte(method, '.')+1:], err)
}

// collection returns Pinky's collection, or noPrompt ("/") when there is
// none yet.
func (s *secretService) collection(conn *dbus.Conn) (dbus.ObjectPath, error) {
	v, err := conn.Object(ssName, ssPath).GetProperty(ssService + ".Collections")
	if err != nil {
		return "", ssError(ssService+".Collections", err)
	}
	paths, _ := v.Value().([]dbus.ObjectPath)
	for _, p := range paths {
		label, err := conn.Object(ssName, p).GetProperty(ssCollection + ".Label")
		if err != nil {
			continue
		}
		if l, _ := label.Value().(string); l == s.label {
			return p, nil
		}
	}
	return noPrompt, nil
}

func (s *secretService) attributes(account string) map[string]string {
	return map[string]string{"service": s.service, "account": account}
}

// item returns the account's item in Pinky's collection, with the
// collection; ErrNotFound when there is none.
func (s *secretService) item(conn *dbus.Conn, account string) (coll, item dbus.ObjectPath, err error) {
	coll, err = s.collection(conn)
	if err != nil {
		return "", "", err
	}
	if coll == noPrompt {
		return "", "", ErrNotFound
	}
	// The service's search finds items of locked collections too (in its
	// second list), which a collection's own search may not; the items of
	// a collection are the objects under its path.
	var unlocked, locked []dbus.ObjectPath
	if err := call(conn, ssPath, ssService+".SearchItems", []any{&unlocked, &locked}, s.attributes(account)); err != nil {
		return "", "", err
	}
	for _, it := range append(unlocked, locked...) {
		if strings.HasPrefix(string(it), string(coll)+"/") {
			return coll, it, nil
		}
	}
	return coll, "", ErrNotFound
}

// prompt shows a prompt the Secret Service asked for and waits for the
// person's answer: ErrCancelled when they dismiss it, ctx's error when ctx
// ends first (which dismisses it).
func prompt(ctx context.Context, conn *dbus.Conn, path dbus.ObjectPath) (dbus.Variant, error) {
	if path == noPrompt || path == "" {
		return dbus.Variant{}, nil
	}
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	match := []dbus.MatchOption{dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(ssPrompt), dbus.WithMatchMember("Completed")}
	if err := conn.AddMatchSignal(match...); err != nil {
		return dbus.Variant{}, fmt.Errorf("secret: watch the prompt: %w", err)
	}
	defer conn.RemoveMatchSignal(match...)
	if err := call(conn, path, ssPrompt+".Prompt", nil, ""); err != nil {
		return dbus.Variant{}, err
	}
	for {
		select {
		case sig := <-signals:
			if sig == nil || sig.Path != path || sig.Name != ssPrompt+".Completed" || len(sig.Body) < 2 {
				continue
			}
			if dismissed, _ := sig.Body[0].(bool); dismissed {
				return dbus.Variant{}, ErrCancelled
			}
			result, _ := sig.Body[1].(dbus.Variant)
			return result, nil
		case <-ctx.Done():
			conn.Object(ssName, path).Call(ssPrompt+".Dismiss", 0)
			return dbus.Variant{}, ctx.Err()
		}
	}
}

// unlock unlocks the collection, asking the person when the Secret
// Service wants to.
func unlock(ctx context.Context, conn *dbus.Conn, coll dbus.ObjectPath) error {
	var unlocked []dbus.ObjectPath
	var p dbus.ObjectPath
	if err := call(conn, ssPath, ssService+".Unlock", []any{&unlocked, &p}, []dbus.ObjectPath{coll}); err != nil {
		return err
	}
	if len(unlocked) > 0 {
		return nil
	}
	result, err := prompt(ctx, conn, p)
	if err != nil {
		return err
	}
	if paths, ok := result.Value().([]dbus.ObjectPath); ok && len(paths) == 0 {
		return ErrCancelled
	}
	return nil
}

// lock locks the collection again. Locking needs no answer from the
// person; a Secret Service that wants one gets none, and the collection
// stays as it is. KWallet's refuses to lock a collection that is locked
// already, which is no failure.
func lock(conn *dbus.Conn, coll dbus.ObjectPath) error {
	var locked []dbus.ObjectPath
	var p dbus.ObjectPath
	err := call(conn, ssPath, ssService+".Lock", []any{&locked, &p}, []dbus.ObjectPath{coll})
	if err != nil {
		if v, perr := conn.Object(ssName, coll).GetProperty(ssCollection + ".Locked"); perr == nil {
			if isLocked, _ := v.Value().(bool); isLocked {
				return nil
			}
		}
	}
	return err
}

// openSession opens a session that carries secrets in the clear over the
// session bus, which only the user's programs reach.
func openSession(conn *dbus.Conn) (dbus.ObjectPath, error) {
	var out dbus.Variant
	var session dbus.ObjectPath
	if err := call(conn, ssPath, ssService+".OpenSession", []any{&out, &session}, "plain", dbus.MakeVariant("")); err != nil {
		return "", err
	}
	return session, nil
}

func closeSession(conn *dbus.Conn, session dbus.ObjectPath) {
	conn.Object(ssName, session).Call(ssSession+".Close", 0)
}

// Supported reports whether a session bus answers, where a keyring such
// as GNOME Keyring or KWallet serves the Secret Service.
func (s *secretService) Supported() bool {
	_, err := s.bus()
	return err == nil
}

func (s *secretService) Has(account string) bool {
	conn, err := s.bus()
	if err != nil {
		return false
	}
	_, _, err = s.item(conn, account)
	return err == nil
}

func (s *secretService) Save(account, secret string) error {
	conn, err := s.bus()
	if err != nil {
		return err
	}
	// Creating and unlocking the keyring may ask the person; give them
	// time.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	coll, err := s.collection(conn)
	if err != nil {
		return err
	}
	if coll == noPrompt {
		if coll, err = s.createCollection(ctx, conn); err != nil {
			return err
		}
	}
	if err := unlock(ctx, conn, coll); err != nil {
		return err
	}
	defer lock(conn, coll)
	session, err := openSession(conn)
	if err != nil {
		return err
	}
	defer closeSession(conn, session)
	props := map[string]dbus.Variant{
		ssItem + ".Label":      dbus.MakeVariant("Pinky (" + account + ")"),
		ssItem + ".Attributes": dbus.MakeVariant(s.attributes(account)),
	}
	value := ssSecret{Session: session, Parameters: []byte{}, Value: []byte(secret), ContentType: "text/plain; charset=utf8"}
	var item, p dbus.ObjectPath
	if err := call(conn, coll, ssCollection+".CreateItem", []any{&item, &p}, props, value, true); err != nil {
		return err
	}
	_, err = prompt(ctx, conn, p)
	return err
}

// createCollection creates Pinky's collection, for which the keyring asks
// the person to choose a password.
func (s *secretService) createCollection(ctx context.Context, conn *dbus.Conn) (dbus.ObjectPath, error) {
	props := map[string]dbus.Variant{ssCollection + ".Label": dbus.MakeVariant(s.label)}
	var coll, p dbus.ObjectPath
	if err := call(conn, ssPath, ssService+".CreateCollection", []any{&coll, &p}, props, ""); err != nil {
		return "", err
	}
	if coll != noPrompt {
		return coll, nil
	}
	result, err := prompt(ctx, conn, p)
	if err != nil {
		return "", err
	}
	coll, _ = result.Value().(dbus.ObjectPath)
	if coll == "" || coll == noPrompt {
		return "", ErrCancelled
	}
	return coll, nil
}

func (s *secretService) Delete(account string) error {
	conn, err := s.bus()
	if err != nil {
		return err
	}
	coll, item, err := s.item(conn, account)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var p dbus.ObjectPath
	err = call(conn, item, ssItem+".Delete", []any{&p})
	if err != nil && !errors.Is(err, ErrNotFound) {
		// A locked keyring may refuse: unlock it (asking the person)
		// and try again.
		if uerr := unlock(ctx, conn, coll); uerr != nil {
			return uerr
		}
		defer lock(conn, coll)
		err = call(conn, item, ssItem+".Delete", []any{&p})
	}
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = prompt(ctx, conn, p)
	return err
}

func (s *secretService) Reveal(ctx context.Context, account, reason string) (string, error) {
	conn, err := s.bus()
	if err != nil {
		return "", err
	}
	coll, item, err := s.item(conn, account)
	if err != nil {
		return "", err
	}
	// Lock first, so that unlocking asks the person even when the keyring
	// was left open (by another program, or a Pinky that stopped while it
	// was open).
	if err := lock(conn, coll); err != nil {
		return "", err
	}
	if err := unlock(ctx, conn, coll); err != nil {
		return "", err
	}
	defer lock(conn, coll)
	session, err := openSession(conn)
	if err != nil {
		return "", err
	}
	defer closeSession(conn, session)
	var v ssSecret
	if err := call(conn, item, ssItem+".GetSecret", []any{&v}, session); err != nil {
		return "", err
	}
	secret := string(v.Value)
	clear(v.Value)
	return secret, nil
}
