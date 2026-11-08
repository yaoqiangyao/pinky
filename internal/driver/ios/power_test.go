package ios

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// fakePhone is a phone on its lock screen, for the unlock logic.
type fakePhone struct {
	on          bool
	wakeOnHome  bool
	isLocked    bool
	passcode    string
	padOnSwipe  bool
	padAt       int  // the pad shows from this swipe on (0: padOnSwipe decides)
	sleepsOff   int  // the screen goes dark again after this many swipes (0: never)
	faceID      bool // the swipe unlocks it
	unlockAt    int  // Face ID unlocks it after this many keys (0: never)
	noBacklight bool // its display info has no backlight state

	home, swipes int
	keys         []keyStroke
	entry        []keyStroke // keys since the pad last cleared
	pad          bool
	slept        time.Duration
	fresh        int // accessibility sessions dropped
	lookErrs     int // looks for the pad that fail before one works
}

func (f *fakePhone) freshAX() { f.fresh++ }

func (f *fakePhone) screenOn(context.Context) (bool, error) {
	if f.noBacklight {
		return screenAwake("")
	}
	return f.on, nil
}
func (f *fakePhone) locked(context.Context) (bool, error) { return f.isLocked, nil }

func (f *fakePhone) pressHome(context.Context) error {
	f.home++
	if f.wakeOnHome {
		f.on = true
	}
	return nil
}

func (f *fakePhone) swipeUp(context.Context) error {
	if !f.on {
		panic("swiped on a dark screen")
	}
	f.swipes++
	if f.faceID {
		f.isLocked = false
		return nil
	}
	f.pad = f.padOnSwipe
	if f.padAt > 0 {
		f.pad = f.swipes >= f.padAt
	}
	if f.sleepsOff > 0 && f.swipes == f.sleepsOff && !f.pad {
		f.on = false
	}
	return nil
}

func (f *fakePhone) keypadShown(context.Context) (bool, error) {
	if f.lookErrs > 0 {
		f.lookErrs--
		return false, errors.New("the accessibility session went away")
	}
	return f.pad, nil
}

func (f *fakePhone) pressKey(_ context.Context, k keyStroke) error {
	if !f.isLocked {
		panic("a passcode key was typed into an unlocked phone")
	}
	f.keys = append(f.keys, k)
	if f.unlockAt > 0 && len(f.keys) == f.unlockAt {
		f.isLocked = false
		return nil
	}
	want, _ := pinStrokes(f.passcode)
	if k.usage == keyEnter || (submitsItself(f.passcode) && len(f.entry)+1 == len(want)) {
		if k.usage != keyEnter {
			f.entry = append(f.entry, k)
		}
		if equalStrokes(f.entry, want) {
			f.isLocked = false
		}
		f.entry = nil // a wrong passcode clears the pad
		return nil
	}
	f.entry = append(f.entry, k)
	return nil
}

func (f *fakePhone) sleep(_ context.Context, d time.Duration) error {
	f.slept += d
	return nil
}

func equalStrokes(a, b []keyStroke) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func lockedPhone(passcode string) *fakePhone {
	return &fakePhone{on: false, wakeOnHome: true, isLocked: true, passcode: passcode, padOnSwipe: true}
}

func TestUnlock(t *testing.T) {
	ctx := context.Background()
	t.Run("already unlocked", func(t *testing.T) {
		f := &fakePhone{on: true}
		if err := unlock(ctx, f, "123456"); err != nil {
			t.Fatal(err)
		}
		if f.home != 0 || f.swipes != 0 || len(f.keys) != 0 {
			t.Errorf("touched an unlocked phone: home %d, swipes %d, keys %d", f.home, f.swipes, len(f.keys))
		}
	})
	t.Run("six digits", func(t *testing.T) {
		f := lockedPhone("123456")
		if err := unlock(ctx, f, "123456"); err != nil {
			t.Fatal(err)
		}
		if f.home != 1 || f.swipes != 1 || len(f.keys) != 6 {
			t.Errorf("home %d, swipes %d, keys %d; want 1, 1, 6 (no Return)", f.home, f.swipes, len(f.keys))
		}
	})
	t.Run("wrong passcode", func(t *testing.T) {
		f := lockedPhone("123456")
		err := unlock(ctx, f, "654321")
		if !errors.Is(err, driver.ErrLocked) {
			t.Fatalf("err = %v, want ErrLocked", err)
		}
		if f.swipes != 1 || len(f.keys) != 6 {
			t.Errorf("swipes %d, keys %d; want one attempt of 6 keys", f.swipes, len(f.keys))
		}
	})
	t.Run("alphanumeric", func(t *testing.T) {
		f := lockedPhone("Pinky7!")
		f.on = true
		if err := unlock(ctx, f, "Pinky7!"); err != nil {
			t.Fatal(err)
		}
		if f.home != 0 || len(f.keys) != 8 || f.keys[7].usage != keyEnter || f.keys[0].mod != keyLeftShift {
			t.Errorf("home %d, keys %v; want no Home, 7 keys with Shift for P, then Return", f.home, f.keys)
		}
	})
	t.Run("custom numeric", func(t *testing.T) {
		f := lockedPhone("12345")
		if err := unlock(ctx, f, "12345"); err != nil {
			t.Fatal(err)
		}
		if len(f.keys) != 6 || f.keys[5].usage != keyEnter {
			t.Errorf("keys %v; want 5 digits and Return", f.keys)
		}
	})
	t.Run("no passcode pad", func(t *testing.T) {
		f := lockedPhone("123456")
		f.padOnSwipe = false
		err := unlock(ctx, f, "123456")
		if !errors.Is(err, driver.ErrPINNotEntered) || errors.Is(err, driver.ErrLocked) || len(f.keys) != 0 {
			t.Errorf("err %v, keys %d; want ErrPINNotEntered and nothing typed", err, len(f.keys))
		}
		if f.swipes != unlockSwipes {
			t.Errorf("%d swipes; want %d", f.swipes, unlockSwipes)
		}
	})
	t.Run("the pad comes on the second swipe", func(t *testing.T) {
		// A lock screen with notifications took the first swipe for its
		// notification list.
		f := lockedPhone("123456")
		f.padAt = 2
		if err := unlock(ctx, f, "123456"); err != nil {
			t.Fatal(err)
		}
		if f.swipes != 2 || len(f.keys) != 6 {
			t.Errorf("swipes %d, keys %d; want 2 swipes and one passcode", f.swipes, len(f.keys))
		}
	})
	t.Run("the screen goes dark between swipes", func(t *testing.T) {
		f := lockedPhone("123456")
		f.padAt, f.sleepsOff = 2, 1
		if err := unlock(ctx, f, "123456"); err != nil {
			t.Fatal(err)
		}
		if f.home != 2 || f.swipes != 2 || len(f.keys) != 6 {
			t.Errorf("home %d, swipes %d, keys %d; want Home again before the second swipe", f.home, f.swipes, len(f.keys))
		}
	})
	t.Run("a just lit screen gets a moment before the swipe", func(t *testing.T) {
		f := lockedPhone("123456")
		f.padOnSwipe = false
		unlock(ctx, f, "123456")
		g := lockedPhone("123456")
		g.on = true
		g.padOnSwipe = false
		unlock(ctx, g, "123456")
		if f.slept-g.slept < afterWake {
			t.Errorf("slept %v after waking, %v when already on; want %v more", f.slept, g.slept, afterWake)
		}
	})
	t.Run("the pad is found in a fresh session after errors", func(t *testing.T) {
		f := lockedPhone("123456")
		f.lookErrs = 2
		if err := unlock(ctx, f, "123456"); err != nil {
			t.Fatalf("unlock: %v", err)
		}
		if f.fresh < 3 {
			t.Errorf("accessibility sessions dropped %d times; want one at the start and one per failed look", f.fresh)
		}
	})
	t.Run("face id on the swipe", func(t *testing.T) {
		f := lockedPhone("123456")
		f.faceID = true
		if err := unlock(ctx, f, "123456"); err != nil || len(f.keys) != 0 {
			t.Errorf("err %v, keys %d; want nil and nothing typed", err, len(f.keys))
		}
	})
	t.Run("unlocked while typing", func(t *testing.T) {
		f := lockedPhone("123456")
		f.unlockAt = 2
		if err := unlock(ctx, f, "123456"); err != nil {
			t.Fatal(err)
		}
		if len(f.keys) != 2 {
			t.Errorf("%d keys typed; want typing to stop once unlocked", len(f.keys))
		}
	})
	t.Run("screen stays off", func(t *testing.T) {
		f := lockedPhone("123456")
		f.wakeOnHome = false
		if err := unlock(ctx, f, "123456"); err == nil || f.swipes != 0 || len(f.keys) != 0 {
			t.Errorf("err %v, swipes %d, keys %d; want an error and nothing else", err, f.swipes, len(f.keys))
		}
	})
	t.Run("no backlight state", func(t *testing.T) {
		f := lockedPhone("123456")
		f.noBacklight = true
		if err := unlock(ctx, f, "123456"); err != nil {
			t.Fatal(err)
		}
		if f.home != 1 || f.swipes != 1 || len(f.keys) != 6 || f.slept < wakeWait {
			t.Errorf("home %d, swipes %d, keys %d, slept %v; want Home, a wait, one swipe and one passcode", f.home, f.swipes, len(f.keys), f.slept)
		}
	})
	t.Run("no backlight state, the pad on the second swipe", func(t *testing.T) {
		f := lockedPhone("123456")
		f.noBacklight = true
		f.padAt = 2
		if err := unlock(ctx, f, "123456"); err != nil {
			t.Fatal(err)
		}
		if f.home != 2 || f.swipes != 2 || len(f.keys) != 6 {
			t.Errorf("home %d, swipes %d, keys %d; want Home before each swipe", f.home, f.swipes, len(f.keys))
		}
	})
	t.Run("no backlight state, already unlocked", func(t *testing.T) {
		f := &fakePhone{on: true, noBacklight: true}
		if err := unlock(ctx, f, "123456"); err != nil {
			t.Fatal(err)
		}
		if f.home != 0 || f.swipes != 0 || len(f.keys) != 0 {
			t.Errorf("touched an unlocked phone: home %d, swipes %d, keys %d", f.home, f.swipes, len(f.keys))
		}
	})
	t.Run("untypable passcode", func(t *testing.T) {
		f := lockedPhone("密码1234")
		err := unlock(ctx, f, "密码1234")
		if !errors.Is(err, driver.ErrUnsupported) || f.home != 0 {
			t.Errorf("err %v, home %d; want ErrUnsupported before anything", err, f.home)
		}
		if err := unlock(ctx, f, ""); err == nil {
			t.Error("an empty passcode was accepted")
		}
	})
}

func TestWake(t *testing.T) {
	ctx := context.Background()
	if err := wake(ctx, &fakePhone{noBacklight: true, wakeOnHome: true}); !errors.Is(err, errUnknownBacklight) {
		t.Errorf("no backlight state: %v; want errUnknownBacklight, and no Home outside unlocking", err)
	}
	f := &fakePhone{on: true, wakeOnHome: true}
	if err := wake(ctx, f); err != nil || f.home != 0 {
		t.Errorf("screen on: err %v, home %d; want nothing pressed", err, f.home)
	}
	f.on = false
	if err := wake(ctx, f); err != nil || f.home != 1 {
		t.Errorf("screen off: err %v, home %d; want one Home", err, f.home)
	}
	f = &fakePhone{}
	if err := wake(ctx, f); err == nil || f.home != 1 || f.slept < wakeWait {
		t.Errorf("never wakes: err %v, home %d, waited %v; want an error after %v", err, f.home, f.slept, wakeWait)
	}
}

func TestScreenAwake(t *testing.T) {
	for state, want := range map[string]bool{"activeOn": true, "activeDimmed": true, "inactiveOn": false, "off": false} {
		got, err := screenAwake(state)
		if err != nil || got != want {
			t.Errorf("%s: %v, %v; want %v", state, got, err, want)
		}
	}
	if _, err := screenAwake(""); !errors.Is(err, errUnknownBacklight) {
		t.Errorf("an empty state: %v; want errUnknownBacklight", err)
	}
}

func TestPasscodePad(t *testing.T) {
	var pad []raxElement
	for i, l := range []string{"1", "2, ABC", "3, DEF", "4, GHI", "5, JKL", "6, MNO", "7, PQRS", "8, TUV", "9, WXYZ", "0"} {
		pad = append(pad, raxElement{Label: l, Type: xcButton, Object: &axpObject{ID: uint64(i)}})
	}
	if !passcodePad(pad) {
		t.Error("the pad was not recognized")
	}
	if passcodePad(pad[1:]) {
		t.Error("a pad without 1 was recognized")
	}
	if passcodePad([]raxElement{{Label: "10:42"}, {Label: "2025"}}) {
		t.Error("a clock was taken for the pad")
	}
	if !passcodePad([]raxElement{{Type: xcSecureTextField}}) {
		t.Error("the alphanumeric passcode field was not recognized")
	}
}

func TestSubmitsItself(t *testing.T) {
	for pin, want := range map[string]bool{"1234": true, "123456": true, "12345": false, "1234567": false, "12a456": false, "abcd": false} {
		if got := submitsItself(pin); got != want {
			t.Errorf("%q: %v, want %v", pin, got, want)
		}
	}
}

func TestIsClosedErr(t *testing.T) {
	for _, m := range []string{"ReceiveOnServerClientStream: could not read frame. EOF", "received GOAWAY", "write: broken pipe", "connection reset by peer", "use of closed network connection"} {
		if !isClosedErr(errors.New(m)) {
			t.Errorf("%q: not taken for a closed connection", m)
		}
	}
	if isClosedErr(errors.New("CoreDevice error 9021")) || isClosedErr(nil) {
		t.Error("a device's answer was taken for a closed connection")
	}
}
